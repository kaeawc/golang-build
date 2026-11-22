package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/kaeawc/golang-build/internal/config"
	"github.com/kaeawc/golang-build/internal/shutdown"
)

// startDebugServer serves net/http/pprof on cfg.DebugAddr (DEBUG_ADDR), a
// listener separate from the public router so profiles are never
// reachable on the public port. An empty address disables it and returns
// a nil address.
//
// Unless cfg.DebugAllowNonLoopback is set, the bound address must be
// loopback. config.Load already refuses non-loopback hosts; checking what
// was actually bound also covers callers that skip config.Load and a
// "localhost" that resolves elsewhere.
//
// The listener binds before returning, so a bad or busy address fails
// boot. The server stops in the "debug-server" shutdown hook; registered
// early, it runs last (LIFO), so profiles stay available while the rest of
// the process drains.
func startDebugServer(ctx context.Context, cfg *config.Config, log *slog.Logger, coord *shutdown.Coordinator) (net.Addr, error) {
	if cfg.DebugAddr == "" {
		return nil, nil
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.DebugAddr)
	if err != nil {
		return nil, fmt.Errorf("debug server: %w", err)
	}
	if tcp, ok := ln.Addr().(*net.TCPAddr); !cfg.DebugAllowNonLoopback && (!ok || !tcp.IP.IsLoopback()) {
		_ = ln.Close()
		return nil, fmt.Errorf("debug server: %s bound to non-loopback %s; set DEBUG_ALLOW_NON_LOOPBACK=true to allow it", cfg.DebugAddr, ln.Addr())
	}
	srv := &http.Server{
		Handler:           debugHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout: pprof rejects ?seconds= at or beyond it, and
		// CPU profiles and traces legitimately stream for 30s or more.
		IdleTimeout: time.Minute,
		ErrorLog:    slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	go func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			log.Error("debug server stopped", "err", err)
		}
	}()
	// Close, not Shutdown: an in-flight 30s CPU profile would otherwise
	// outlast the hook budget, and nothing on this listener needs draining.
	coord.Register("debug-server", func(context.Context) error { return srv.Close() })
	log.Info("debug server listening", "addr", ln.Addr().String())
	return ln.Addr(), nil
}

// debugHandler routes the pprof endpoints on a private mux. Importing
// net/http/pprof also registers them on http.DefaultServeMux, which this
// server never serves.
func debugHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index) // named profiles: heap, goroutine, allocs, ...
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}
