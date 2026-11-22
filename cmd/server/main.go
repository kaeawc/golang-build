package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/kaeawc/golang-build/internal/cache"
	"github.com/kaeawc/golang-build/internal/config"
	"github.com/kaeawc/golang-build/internal/db"
	"github.com/kaeawc/golang-build/internal/env"
	"github.com/kaeawc/golang-build/internal/handlers"
	"github.com/kaeawc/golang-build/internal/httpmw"
	"github.com/kaeawc/golang-build/internal/httpserver"
	"github.com/kaeawc/golang-build/internal/idgen"
	"github.com/kaeawc/golang-build/internal/jobs"
	"github.com/kaeawc/golang-build/internal/jsonresp"
	"github.com/kaeawc/golang-build/internal/logger"
	"github.com/kaeawc/golang-build/internal/middleware"
	"github.com/kaeawc/golang-build/internal/shutdown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, env.Default, os.Stderr)
	stop()
	if err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// run boots the server and serves until ctx is canceled (SIGINT/SIGTERM)
// or the server fails. Every resource it acquires is released before it
// returns, on success and on error, because main's only exit is after
// run returns.
func run(ctx context.Context, r env.Reader, logOut io.Writer) (err error) {
	cfg, err := config.Load(r)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := logger.NewSlog(logger.Config{Writer: logOut, Format: cfg.LogFormat, Level: cfg.LogLevel})
	// Also routes the standard log package (used by some dependencies)
	// through this handler.
	slog.SetDefault(log)
	if cfg.ValkeyAddr == "" {
		return errors.New("config: VALKEY_ADDR is required")
	}

	coord := shutdown.New(10 * time.Second)
	coord.OnHookError(func(name string, err error) {
		log.Error("shutdown hook failed", "hook", name, "err", err)
	})
	defer func() {
		// The only place hooks run: after the HTTP server has drained
		// (srv.Run returned) or after boot failed part-way. Its own budget,
		// separate from the HTTP drain, so a slow drain cannot starve the
		// River/pool/cache hooks.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		err = errors.Join(err, coord.Shutdown(shutdownCtx))
		if stoppedBySignal(ctx, err) {
			log.Info("stopped during startup", "err", err)
			err = nil
		}
	}()

	pool, c, err := bootResources(ctx, cfg)
	if err != nil {
		return fmt.Errorf("boot: %w", err)
	}
	coord.Register("cache", func(context.Context) error { c.Close(); return nil })
	coord.Register("db-pool", func(context.Context) error { pool.Close(); return nil })

	if err := jobs.Migrate(ctx, pool); err != nil {
		return err
	}
	riverClient, err := jobs.NewClient(pool, jobs.NewWorkers(log), log)
	if err != nil {
		return fmt.Errorf("river client: %w", err)
	}
	// River treats cancellation of Start's ctx as an abrupt stop that
	// cancels running jobs; the "river" hook does the graceful stop.
	if err := riverClient.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("river start: %w", err)
	}
	coord.Register("river", jobs.Stop(riverClient, log))

	router := buildRouter(log, pool, db.New(pool), c)

	attrs := []any{"port", cfg.Port, "env", cfg.Environment}
	if cfg.Fly.OnFly() {
		attrs = append(attrs, "fly_app", cfg.Fly.AppName, "fly_region", cfg.Fly.Region, "fly_machine", cfg.Fly.MachineID)
	}
	log.Info("server starting", attrs...)

	srv := httpserver.New(httpserver.Config{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
		Signals:      []os.Signal{}, // ctx carries SIGINT/SIGTERM (see main)
	})
	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	log.Info("server stopped")
	return nil
}

// stoppedBySignal reports whether err is only the cancellation of ctx
// (SIGINT/SIGTERM during boot), which is a requested stop, not a failure.
func stoppedBySignal(ctx context.Context, err error) bool {
	return err != nil && ctx.Err() != nil && errors.Is(err, context.Canceled)
}

// bootResources runs independent startup work in parallel: app DB migration,
// pgx pool dial, and Valkey dial. Saves a full RTT-to-Neon plus Valkey dial
// on every cold start.
func bootResources(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, cache.Cache, error) {
	var (
		pool *pgxpool.Pool
		c    cache.Cache
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return db.Migrate("file://sql/migrations", cfg.MigrationDatabaseURL)
	})
	g.Go(func() error {
		p, err := pgxpool.New(gctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		pool = p
		return nil
	})
	g.Go(func() error {
		cc, err := cache.New(cfg.ValkeyAddr)
		if err != nil {
			return err
		}
		c = cc
		return nil
	})
	if err := g.Wait(); err != nil {
		if pool != nil {
			pool.Close()
		}
		if c != nil {
			c.Close()
		}
		return nil, nil, err
	}
	return pool, c, nil
}

// buildRouter wires the routing table with net/http patterns. A GET
// pattern also serves HEAD. The SPA is registered as "GET /", so it
// catches every unmatched GET while a wrong method on any route gets
// 405 with an Allow header; unknown /api paths get a JSON 404 instead
// of the SPA. Register API routes with handleAPI so they get the /api
// middleware; unmatched requests never reach it (the traffic recorder
// only sees real routes).
func buildRouter(log *slog.Logger, pool handlers.Pinger, users handlers.UserQuerier, c cache.Cache) *http.ServeMux {
	traffic := middleware.NewTrafficRecorder()
	api := httpmw.Compose(
		httpmw.RequestID(idgen.UUID{}),
		middleware.Logging(log),
		middleware.Recover,
		traffic.Middleware,
		middleware.Gzip,
		middleware.ContentType, // inside Gzip so it sniffs the plain body
	)

	mux := http.NewServeMux()
	handleAPI := func(pattern string, h http.Handler) { mux.Handle(pattern, api(h)) }

	mux.Handle("GET /healthz", handlers.Health(pool))
	handleAPI("GET /api/users", handlers.GetUsers(users, c))
	handleAPI("GET /api/admin/traffic", handlers.AdminTraffic(traffic))
	handleAPI("GET /api/admin/healthchecks", handlers.AdminHealthchecks(pool, c))
	mux.HandleFunc("GET /api/", apiNotFound)
	mux.Handle("GET /ws", handlers.WebSocket())
	mux.Handle("GET /", handlers.NewSPAHandler("web/dist", "index.html"))
	return mux
}

// apiNotFound answers unknown /api paths with JSON instead of the SPA's
// index.html, so API clients see a real 404.
func apiNotFound(w http.ResponseWriter, r *http.Request) {
	_ = jsonresp.WriteError(w, r, http.StatusNotFound, "not_found", "no such API endpoint")
}
