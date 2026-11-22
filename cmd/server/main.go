package main

import (
	"context"
	"log"
	"net/http"
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
	"github.com/kaeawc/golang-build/internal/jobs"
	"github.com/kaeawc/golang-build/internal/jsonresp"
	"github.com/kaeawc/golang-build/internal/middleware"
	"github.com/kaeawc/golang-build/internal/shutdown"
)

func main() {
	cfg, err := config.Load(env.Default)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.ValkeyAddr == "" {
		log.Fatal("VALKEY_ADDR is required")
	}

	ctx := context.Background()
	coord := shutdown.New(10 * time.Second)

	pool, c, err := bootResources(ctx, cfg)
	if err != nil {
		log.Fatalf("boot: %v", err)
	}
	coord.Register("cache", func(context.Context) error { c.Close(); return nil })
	coord.Register("db-pool", func(context.Context) error { pool.Close(); return nil })

	if err := jobs.Migrate(ctx, pool); err != nil {
		log.Fatalf("river migrate: %v", err)
	}
	riverClient, err := jobs.NewClient(pool, jobs.NewWorkers())
	if err != nil {
		log.Fatalf("river client: %v", err)
	}
	if err := riverClient.Start(ctx); err != nil {
		log.Fatalf("river start: %v", err)
	}
	coord.Register("river", jobs.Stop(riverClient))

	router := buildRouter(pool, db.New(pool), c)

	if cfg.Fly.OnFly() {
		log.Printf("Server starting on :%s (env=%s, fly app=%s region=%s machine=%s)",
			cfg.Port, cfg.Environment, cfg.Fly.AppName, cfg.Fly.Region, cfg.Fly.MachineID)
	} else {
		log.Printf("Server starting on :%s (env=%s)", cfg.Port, cfg.Environment)
	}

	srv := httpserver.New(httpserver.Config{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
		OnShutdown:   coord.Shutdown,
	})
	if err := srv.Run(ctx); err != nil {
		log.Fatalf("server: %v", err)
	}
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
func buildRouter(pool handlers.Pinger, users handlers.UserQuerier, c cache.Cache) *http.ServeMux {
	traffic := middleware.NewTrafficRecorder()
	api := httpmw.Compose(
		middleware.Logging,
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
