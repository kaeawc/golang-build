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
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
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
	"github.com/kaeawc/golang-build/internal/telemetry"
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
	// LogHandler adds trace_id/span_id to records logged with a span in
	// their context.
	base := logger.NewSlog(logger.Config{Writer: logOut, Format: cfg.LogFormat, Level: cfg.LogLevel})
	log := slog.New(telemetry.LogHandler(base.Handler()))
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

	// Opt-in: a no-op unless OTEL_EXPORTER_OTLP_ENDPOINT (or a per-signal
	// endpoint) is set. Registered first so it runs last and flushes the
	// spans the other hooks produce.
	stopTelemetry, err := telemetry.Setup(ctx, telemetry.Service{
		Name:        "golang-build-server",
		Version:     buildVersion(),
		Environment: string(cfg.Environment),
		Logger:      log,
	})
	if err != nil {
		return err
	}
	coord.Register("telemetry", stopTelemetry)

	if _, err := startDebugServer(ctx, cfg, log, coord); err != nil {
		return err
	}

	pool, c, err := bootResources(ctx, cfg)
	if err != nil {
		return fmt.Errorf("boot: %w", err)
	}
	coord.Register("cache", func(context.Context) error { c.Close(); return nil })
	coord.Register("db-pool", func(context.Context) error { pool.Close(); return nil })

	if err := startJobs(ctx, pool, log, coord); err != nil {
		return err
	}

	router := buildRouter(log, pool, db.New(pool), c)

	log.Info("server starting", startupAttrs(cfg)...)

	srv := httpserver.New(httpserver.Config{
		Addr:         ":" + cfg.Port,
		Handler:      instrument(router),
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

// startJobs migrates River's schema, starts the client and registers its
// graceful stop as a shutdown hook.
func startJobs(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger, coord *shutdown.Coordinator) error {
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
	return nil
}

// instrument wraps the router in OpenTelemetry HTTP server tracing and
// metrics. otelhttp names spans "{METHOD} {route}" from the matched
// ServeMux pattern (just the method when nothing matched, "HTTP" for
// non-standard methods). /healthz is not traced: Fly probes it every ~15s.
func instrument(h http.Handler, opts ...otelhttp.Option) http.Handler {
	opts = append([]otelhttp.Option{
		otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/healthz" }),
	}, opts...)
	// ServeMux sets r.Pattern on the request it is given, so read it after
	// routing to add http.route to the span (otelhttp only adds it to
	// metrics). Deferred so a request that panics past the handler still
	// gets its route and name; otelhttp's own rename is skipped then.
	routed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			route := httpRoute(r.Pattern)
			if route == "" {
				return
			}
			span := trace.SpanFromContext(r.Context())
			span.SetAttributes(semconv.HTTPRoute(route))
			span.SetName(spanMethod(r.Method) + " " + route)
		}()
		h.ServeHTTP(w, r)
	})
	return otelhttp.NewHandler(routed, "http.server", opts...)
}

// spanMethod mirrors otelhttp's span naming: standard methods as-is,
// anything else as "HTTP", so clients cannot mint span names.
func spanMethod(m string) string {
	switch m = strings.ToUpper(m); m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return m
	}
	return "HTTP"
}

// httpRoute strips the method and host from a ServeMux pattern:
// "GET example.com/api/{id}" → "/api/{id}".
func httpRoute(pattern string) string {
	if i := strings.IndexByte(pattern, '/'); i >= 0 {
		return pattern[i:]
	}
	return ""
}

// buildVersion reports the module version, or the VCS revision for local
// builds, for the service.version resource attribute.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return "dev"
}

func startupAttrs(cfg *config.Config) []any {
	attrs := []any{"port", cfg.Port, "env", cfg.Environment}
	if cfg.Fly.OnFly() {
		attrs = append(attrs, "fly_app", cfg.Fly.AppName, "fly_region", cfg.Fly.Region, "fly_machine", cfg.Fly.MachineID)
	}
	return attrs
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
		pcfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
		if err != nil {
			return err
		}
		pcfg.ConnConfig.Tracer = otelpgx.NewTracer()
		p, err := pgxpool.NewWithConfig(gctx, pcfg)
		if err != nil {
			return err
		}
		pool = p
		return otelpgx.RecordStats(p)
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
