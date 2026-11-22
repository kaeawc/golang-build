package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/config"
	"github.com/kaeawc/golang-build/internal/shutdown"
)

func TestDebugServerDisabledWhenAddrEmpty(t *testing.T) {
	coord := shutdown.New(time.Second)
	addr, err := startDebugServer(t.Context(), &config.Config{}, slog.New(slog.DiscardHandler), coord)
	if err != nil || addr != nil {
		t.Fatalf("startDebugServer(\"\") = %v, %v; want nil, nil", addr, err)
	}
	if coord.Size() != 0 {
		t.Errorf("registered %d shutdown hooks, want 0", coord.Size())
	}
}

func TestDebugServerServesPprofUntilShutdown(t *testing.T) {
	coord := shutdown.New(time.Second)
	addr, err := startDebugServer(t.Context(), &config.Config{DebugAddr: "127.0.0.1:0"}, slog.New(slog.DiscardHandler), coord)
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + addr.String()

	for _, path := range []string{"/debug/pprof/", "/debug/pprof/heap?debug=1", "/debug/pprof/cmdline"} {
		code, body := get(t, base+path)
		if code != http.StatusOK || body == "" {
			t.Errorf("GET %s = %d (%d bytes), want 200 with a body", path, code, len(body))
		}
	}
	// A CPU profile longer than any WriteTimeout would be refused by pprof.
	if code, body := get(t, base+"/debug/pprof/profile?seconds=1"); code != http.StatusOK {
		t.Errorf("GET /debug/pprof/profile?seconds=1 = %d %q", code, body)
	}

	if err := coord.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", addr.String(), time.Second)
	if err == nil {
		_ = conn.Close()
		t.Error("debug listener still accepting after shutdown")
	}
}

func TestDebugServerBindErrorFailsBoot(t *testing.T) {
	var lc net.ListenConfig
	busy, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	coord := shutdown.New(time.Second)
	if _, err := startDebugServer(t.Context(), &config.Config{DebugAddr: busy.Addr().String()}, slog.New(slog.DiscardHandler), coord); err == nil {
		t.Fatal("startDebugServer on a busy port succeeded, want error")
	}
	if coord.Size() != 0 {
		t.Errorf("registered %d shutdown hooks after a failed bind, want 0", coord.Size())
	}
}

// TestDebugServerChecksBoundAddress covers callers that skip
// config.Load: the bound address itself must be loopback unless allowed.
func TestDebugServerChecksBoundAddress(t *testing.T) {
	coord := shutdown.New(time.Second)
	log := slog.New(slog.DiscardHandler)
	if addr, err := startDebugServer(t.Context(), &config.Config{DebugAddr: "0.0.0.0:0"}, log, coord); err == nil {
		t.Fatalf("startDebugServer(0.0.0.0:0) bound %v, want error", addr)
	}
	if coord.Size() != 0 {
		t.Fatalf("registered %d shutdown hooks after refusing, want 0", coord.Size())
	}

	addr, err := startDebugServer(t.Context(), &config.Config{DebugAddr: "0.0.0.0:0", DebugAllowNonLoopback: true}, log, coord)
	if err != nil {
		t.Fatalf("startDebugServer(0.0.0.0:0) with opt-in: %v", err)
	}
	if err := coord.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if addr == nil {
		t.Error("nil address with opt-in")
	}
}

// TestPublicRouterDoesNotServePprof guards against pprof leaking onto the
// public mux (e.g. someone serving http.DefaultServeMux, where importing
// net/http/pprof registers its handlers).
func TestPublicRouterDoesNotServePprof(t *testing.T) {
	router := newTestRouter()
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/cmdline", "/debug/pprof/heap?debug=1", "/debug/pprof/goroutine?debug=1"} {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		body := rr.Body.String()
		if strings.Contains(body, "Types of profiles available") || strings.Contains(body, "goroutine profile:") ||
			strings.Contains(body, "heap profile:") || strings.Contains(body, "server.test") {
			t.Errorf("public router served pprof for %s: %d %.80q", path, rr.Code, body)
		}
	}
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}
