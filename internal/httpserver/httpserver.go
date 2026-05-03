// Package httpserver wraps net/http.Server with the boilerplate every Go
// service eventually writes: bind a listener, run ListenAndServe in a
// goroutine, install signal handlers, drain in-flight requests with a
// bounded shutdown timeout. Pair with internal/shutdown for additional
// teardown hooks.
//
// Typical use:
//
//	srv := httpserver.New(httpserver.Config{
//	    Addr:            ":8080",
//	    Handler:         router,
//	    ShutdownTimeout: 10 * time.Second,
//	})
//	if err := srv.Run(ctx); err != nil { log.Fatal(err) }
//
// Run blocks until ctx is canceled OR a SIGINT/SIGTERM arrives, then
// drains the server with the configured timeout and returns.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Config controls New. Addr and Handler are required; everything else has a
// sensible default.
type Config struct {
	// Addr is the TCP listen address (":8080", "127.0.0.1:0", etc).
	Addr string
	// Handler is the root http.Handler (router, mux, etc).
	Handler http.Handler
	// ShutdownTimeout bounds graceful shutdown. Default 10s.
	ShutdownTimeout time.Duration
	// ReadHeaderTimeout protects against slowloris. Default 10s. Set to a
	// negative value to disable (not recommended for public services).
	ReadHeaderTimeout time.Duration
	// ReadTimeout, WriteTimeout, IdleTimeout pass through to *http.Server.
	// Zero means no limit.
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
	// OnShutdown, if non-nil, runs after the server stops accepting new
	// requests but before Shutdown completes. Use it to flush metrics or
	// run additional teardown.
	OnShutdown func(ctx context.Context) error
	// Signals are the OS signals that trigger graceful shutdown. Default
	// is SIGINT and SIGTERM. Pass an empty slice to disable signal
	// handling (useful in tests).
	Signals []os.Signal
}

// Server is a graceful HTTP server.
type Server struct {
	cfg Config
	hs  *http.Server

	mu      sync.Mutex
	addr    net.Addr
	started bool
}

// New constructs a Server. Returns nil if Handler is nil.
func New(cfg Config) *Server {
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 10 * time.Second
	}
	if cfg.ReadHeaderTimeout == 0 {
		cfg.ReadHeaderTimeout = 10 * time.Second
	} else if cfg.ReadHeaderTimeout < 0 {
		cfg.ReadHeaderTimeout = 0
	}

	hs := &http.Server{
		Addr:              cfg.Addr,
		Handler:           cfg.Handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
	return &Server{cfg: cfg, hs: hs}
}

// Run binds the listener, serves until ctx is canceled or a configured signal
// arrives, then drains the server with ShutdownTimeout.
//
// http.ErrServerClosed is normal and not returned. Any other Serve error or
// shutdown error is returned to the caller.
func (s *Server) Run(ctx context.Context) error {
	if s.cfg.Handler == nil {
		return errors.New("httpserver: Handler is nil")
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("httpserver: listen %s: %w", s.cfg.Addr, err)
	}
	s.mu.Lock()
	s.addr = ln.Addr()
	s.started = true
	s.mu.Unlock()

	serveErr := make(chan error, 1)
	go func() {
		err := s.hs.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			serveErr <- nil
			return
		}
		serveErr <- err
	}()

	signals := s.cfg.Signals
	if signals == nil {
		signals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}
	}
	var sigCh chan os.Signal
	if len(signals) > 0 {
		sigCh = make(chan os.Signal, 1)
		signal.Notify(sigCh, signals...)
		defer signal.Stop(sigCh)
	}

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	case <-sigCh:
	}

	return s.shutdown(serveErr)
}

func (s *Server) shutdown(serveErr <-chan error) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()

	var errs []error
	if err := s.hs.Shutdown(shutdownCtx); err != nil {
		errs = append(errs, fmt.Errorf("httpserver: shutdown: %w", err))
	}
	if s.cfg.OnShutdown != nil {
		if err := s.cfg.OnShutdown(shutdownCtx); err != nil {
			errs = append(errs, fmt.Errorf("httpserver: OnShutdown: %w", err))
		}
	}
	// Drain Serve goroutine; ignore http.ErrServerClosed which is the
	// expected return after Shutdown.
	if err := <-serveErr; err != nil {
		errs = append(errs, fmt.Errorf("httpserver: serve: %w", err))
	}
	return errors.Join(errs...)
}

// Addr returns the bound listener address. Only valid after Run has assigned
// it; returns nil before Run.
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}
