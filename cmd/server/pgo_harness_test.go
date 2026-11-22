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
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/config"
	"github.com/kaeawc/golang-build/internal/db"
	"github.com/kaeawc/golang-build/internal/httpserver"
	"github.com/kaeawc/golang-build/internal/logger"
	"github.com/kaeawc/golang-build/internal/shutdown"
)

// TestMain doubles as the PGO profiling harness for scripts/pgo.sh
// (make pgo). With PGO_HARNESS_ADDR set, the test binary serves the
// production router (buildRouter and its full middleware chain) backed by
// in-memory fakes, plus the real debug server on PGO_HARNESS_DEBUG_ADDR,
// until SIGINT/SIGTERM. That profiles the request path without Postgres
// or Valkey; docs/pgo.md covers what the fakes leave out. Without the
// variable it runs the tests as usual.
func TestMain(m *testing.M) {
	if addr := os.Getenv("PGO_HARNESS_ADDR"); addr != "" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err := serveHarness(ctx, addr, os.Getenv("PGO_HARNESS_DEBUG_ADDR"))
		stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, "pgo harness:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// serveHarness mirrors run with the I/O-bound dependencies faked. Request
// logs are formatted as in production (JSON at info) but discarded, so
// their cost stays in the profile.
func serveHarness(ctx context.Context, addr, debugAddr string) (err error) {
	log := logger.NewSlog(logger.Config{Writer: io.Discard, Format: logger.FormatJSON, Level: slog.LevelInfo})
	coord := shutdown.New(5 * time.Second)
	defer func() { err = errors.Join(err, coord.Shutdown(context.WithoutCancel(ctx))) }()

	if _, err := startDebugServer(ctx, &config.Config{DebugAddr: debugAddr}, log, coord); err != nil {
		return err
	}
	srv := httpserver.New(httpserver.Config{
		Addr:         addr,
		Handler:      buildRouter(log, testPinger{}, harnessUsers(50), newMemCache()),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
		Signals:      []os.Signal{},
	})
	return srv.Run(ctx)
}

// harnessUsers is a fixed user table of n rows.
type harnessUsers int

func (n harnessUsers) GetUsers(context.Context) ([]db.User, error) {
	users := make([]db.User, n)
	for i := range users {
		users[i] = db.User{ID: int64(i + 1), Name: fmt.Sprintf("User %d", i+1)}
	}
	return users, nil
}

// memCache is an in-process stand-in for Valkey that honors TTLs, so the
// /api/users hit/miss mix matches production.
type memCache struct {
	mu      sync.Mutex
	entries map[string]memEntry
}

type memEntry struct {
	value   string
	expires time.Time
}

func newMemCache() *memCache { return &memCache{entries: map[string]memEntry{}} }

func (c *memCache) Get(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expires) {
		return "", errors.New("cache miss")
	}
	return e.value, nil
}

func (c *memCache) Set(_ context.Context, key, value string, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = memEntry{value: value, expires: time.Now().Add(ttl)}
	return nil
}

func (c *memCache) Close() {}

// TestServeHarness keeps the make pgo harness compiling and working: it
// must serve the public routes and pprof on separate listeners, and stop
// cleanly when its context is canceled.
func TestServeHarness(t *testing.T) {
	addr, debugAddr := freeAddr(t), freeAddr(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- serveHarness(ctx, addr, debugAddr) }()

	waitReady(t, "http://"+addr+"/healthz")
	if code, body := get(t, "http://"+addr+"/api/users"); code != http.StatusOK || !strings.Contains(body, `"User 50"`) {
		t.Errorf("GET /api/users = %d %.80q", code, body)
	}
	if code, _ := get(t, "http://"+debugAddr+"/debug/pprof/"); code != http.StatusOK {
		t.Errorf("GET debug /debug/pprof/ = %d, want 200", code)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveHarness: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveHarness did not stop after cancel")
	}
}

func TestMemCacheHonorsTTL(t *testing.T) {
	c := newMemCache()
	if _, err := c.Get(t.Context(), "k"); err == nil {
		t.Error("Get on empty cache succeeded")
	}
	_ = c.Set(t.Context(), "k", "v", time.Hour)
	if v, err := c.Get(t.Context(), "k"); err != nil || v != "v" {
		t.Errorf("Get = %q, %v; want v, nil", v, err)
	}
	_ = c.Set(t.Context(), "k", "v", -time.Second)
	if _, err := c.Get(t.Context(), "k"); err == nil {
		t.Error("Get returned an expired entry")
	}
}

// freeAddr reserves a loopback port and releases it for the caller to bind.
func freeAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func waitReady(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s not ready after 10s", url)
}
