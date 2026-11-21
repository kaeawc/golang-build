// Command mcp serves the golang-build users directory as a Model Context
// Protocol server (spec 2026-07-28, compatible with 2025-11-25 clients).
//
//	mcp                                   # stdio, in-memory demo data
//	mcp -transport=http                   # streamable HTTP on 127.0.0.1:8081/mcp
//	MCP_AUTH_SECRET=... mcp -transport=http -addr=:8081 -resource-url=https://mcp.example.com/mcp
//	MCP_AUTH_SECRET=... mcp -mint-token   # print a dev bearer token
//	DATABASE_URL=... mcp -backend=postgres
//
// Logs always go to stderr: on stdio, stdout is the protocol channel.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaeawc/golang-build/internal/clock"
	"github.com/kaeawc/golang-build/internal/env"
	"github.com/kaeawc/golang-build/internal/healthcheck"
	"github.com/kaeawc/golang-build/internal/mcpserver"
	"github.com/kaeawc/golang-build/internal/tokens"
)

// version is set with -ldflags "-X main.version=..." (see Makefile); when
// empty the server reports the module build info instead.
var version string

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], env.OS{}, os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, envr env.Reader, stdout, stderr io.Writer) error {
	cfg, err := parseConfig(args, envr, stderr)
	if err != nil {
		return err
	}
	log := newLogger(cfg, stderr)

	if cfg.mintToken {
		tok, err := mcpserver.MintToken(tokens.NewHMAC256([]byte(cfg.authSecret)), clock.Default,
			cfg.tokenSubject, cfg.resourceURL, cfg.tokenScopes, cfg.tokenTTL)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, tok)
		return err
	}
	return serve(ctx, cfg, log, nil)
}

// serve runs the configured transport until ctx is done. ln, if non-nil, is
// a pre-bound HTTP listener (tests use it to avoid port races).
func serve(ctx context.Context, cfg config, log *slog.Logger, ln net.Listener) error {
	store, health, closeStore, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	deps := mcpserver.Deps{Store: store, Logger: log, Health: health, Version: version}
	if cfg.stateSecret != "" {
		deps.StateTokens = tokens.NewHMAC256([]byte(cfg.stateSecret))
	}
	var consent *mcpserver.MemoryConsent
	if cfg.transport == transportHTTP {
		consent = mcpserver.NewMemoryConsent(clock.Default)
		deps.Consent, deps.ConsentURL = consent, cfg.consentBaseURL()
	}
	server := mcpserver.New(deps)

	if cfg.transport == transportStdio {
		log.Info("serving MCP over stdio", "backend", cfg.backend)
		if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	}
	return serveHTTP(ctx, cfg, log, ln, server, health, consent)
}

func newLogger(cfg config, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.logLevel}
	if cfg.logJSON {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

// openStore builds the UserStore for cfg.backend plus a readiness registry
// that probes it.
func openStore(ctx context.Context, cfg config) (mcpserver.UserStore, *healthcheck.Registry, func(), error) {
	health := healthcheck.NewRegistry()
	var store mcpserver.UserStore
	closeFn := func() {}
	switch cfg.backend {
	case backendPG:
		pool, err := pgxpool.New(ctx, cfg.databaseURL)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("connect postgres: %w", err)
		}
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := pool.Ping(pingCtx); err != nil {
			pool.Close()
			return nil, nil, nil, fmt.Errorf("ping postgres: %w", err)
		}
		health.RegisterReadiness("postgres", pool.Ping)
		store, closeFn = mcpserver.NewPgxStore(pool), pool.Close
	default:
		store = mcpserver.NewMemoryStore(mcpserver.DemoUsers...)
	}
	health.RegisterReadiness("store", func(ctx context.Context) error {
		_, err := store.List(ctx, 0, 1)
		return err
	})
	return store, health, closeFn, nil
}

func serveHTTP(ctx context.Context, cfg config, log *slog.Logger, ln net.Listener, server *mcp.Server, health *healthcheck.Registry, consent mcpserver.ConsentStore) error {
	opts := mcpserver.HTTPOptions{Logger: log, Health: health, Consent: consent, TrustedOrigins: cfg.trustedOrigins}
	if cfg.authSecret != "" {
		opts.Auth = &mcpserver.AuthOptions{
			Verifier:             tokens.NewHMAC256([]byte(cfg.authSecret)),
			ResourceURL:          cfg.resourceURL,
			AuthorizationServers: cfg.authServers,
		}
	}
	handler, err := mcpserver.NewHTTPHandler(server, opts)
	if err != nil {
		return err
	}

	// Long-lived streams (legacy GET SSE, subscriptions/listen) never end on
	// their own, so request contexts derive from streamCtx, which shutdown
	// cancels once ordinary requests have had time to drain.
	streamCtx, cutStreams := context.WithCancel(context.Background())
	defer cutStreams()
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
		BaseContext:       func(net.Listener) context.Context { return streamCtx },
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		// No ReadTimeout/WriteTimeout: they would sever SSE responses and
		// long-running tool calls. Bodies are bounded by the SDK (4 MiB).
	}
	if ln == nil {
		var lc net.ListenConfig
		if ln, err = lc.Listen(ctx, "tcp", cfg.addr); err != nil {
			return fmt.Errorf("listen %s: %w", cfg.addr, err)
		}
	}
	log.Info("serving MCP over streamable HTTP", "addr", ln.Addr().String(), "endpoint", cfg.resourceURL,
		"auth", opts.Auth != nil, "backend", cfg.backend)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	return shutdown(srv, serveErr, cutStreams, cfg.shutdownTimeout, log, health)
}

// shutdown drains srv within budget: readiness flips to failing, ordinary
// requests get half the budget, then long-lived streams are cut, and
// whatever survives the full budget is force-closed.
func shutdown(srv *http.Server, serveErr <-chan error, cutStreams func(), budget time.Duration, log *slog.Logger, health *healthcheck.Registry) error {
	log.Info("shutting down", "timeout", budget)
	health.SetShuttingDown()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	cut := time.AfterFunc(budget/2, cutStreams)
	defer cut.Stop()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		// Typically a client that reconnected just before the listener
		// closed: net/http only reaps such StateNew connections after 5s.
		log.Warn("graceful shutdown timed out; closing remaining connections", "err", err)
		if err := srv.Close(); err != nil {
			return fmt.Errorf("force close: %w", err)
		}
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("shutdown complete")
	return nil
}
