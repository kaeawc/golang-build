package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/db"
	"github.com/kaeawc/golang-build/internal/env"
	"github.com/kaeawc/golang-build/internal/handlers"
	"github.com/kaeawc/golang-build/internal/httpmw"
	"github.com/kaeawc/golang-build/internal/middleware"
)

type testQuerier struct{}

func (q *testQuerier) GetUsers(_ context.Context) ([]db.User, error) {
	return []db.User{
		{ID: 1, Name: "Alice"},
		{ID: 2, Name: "Bob"},
		{ID: 3, Name: "Charlie"},
	}, nil
}

type testCache struct{}

func (c *testCache) Get(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("miss")
}

func (c *testCache) Set(_ context.Context, _ string, _ string, _ time.Duration) error {
	return nil
}

func (c *testCache) Close() {}

type testPinger struct{}

func (testPinger) Ping(context.Context) error { return nil }

// newTestRouter builds the real routing table with fake dependencies.
func newTestRouter() *http.ServeMux {
	return buildRouter(slog.New(slog.DiscardHandler), testPinger{}, &testQuerier{}, &testCache{})
}

// TestRunReturnsConfigErrors checks run reports bad configuration as an
// error (so main's deferred cleanups and single exit point apply)
// instead of exiting the process.
func TestRunReturnsConfigErrors(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	tests := map[string]struct {
		vars env.Map
		want string
	}{
		"missing DATABASE_URL": {env.Map{}, "DATABASE_URL is required"},
		"bad LOG_LEVEL":        {env.Map{"DATABASE_URL": "postgres://x", "LOG_LEVEL": "loud"}, "LOG_LEVEL"},
		"missing VALKEY_ADDR":  {env.Map{"DATABASE_URL": "postgres://x"}, "VALKEY_ADDR is required"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := run(t.Context(), tt.vars, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("run() = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestStoppedBySignal(t *testing.T) {
	live := t.Context()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	wrapped := fmt.Errorf("boot: %w", context.Canceled)
	for _, tt := range []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"signal during boot", canceled, wrapped, true},
		{"no error", canceled, nil, false},
		{"real failure after signal", canceled, errors.New("listen: address in use"), false},
		{"canceled error without a signal", live, wrapped, false},
	} {
		if got := stoppedBySignal(tt.ctx, tt.err); got != tt.want {
			t.Errorf("%s: stoppedBySignal = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestAPIResponsesCarryRequestID(t *testing.T) {
	rr := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	if rr.Header().Get(httpmw.HeaderRequestID) == "" {
		t.Errorf("missing %s on /api response", httpmw.HeaderRequestID)
	}
}

func TestHealthz(t *testing.T) {
	rr := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != `{"status":"ok"}` {
		t.Errorf("GET /healthz = %d %q", rr.Code, rr.Body.String())
	}
}

func TestMainAppStartup(t *testing.T) {
	router := newTestRouter()

	req, err := http.NewRequest(http.MethodGet, "/api/users", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Expected status code 200, but got %v", status)
	}

	if contentType := rr.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, but got %v", contentType)
	}

	var users []handlers.User
	err = json.Unmarshal(rr.Body.Bytes(), &users)
	if err != nil {
		t.Errorf("Response body did not contain valid JSON: %v", err)
	}
}

// TestRoutingTable pins the method and path behavior of every route.
// pattern is the ServeMux pattern the request resolves to; rows with a
// status are also served, for responses ServeMux writes itself (405 and
// canonical-path redirects).
func TestRoutingTable(t *testing.T) {
	router := newTestRouter()

	tests := []struct {
		method, path string
		pattern      string
		status       int
		allow        string
		location     string
	}{
		{method: "GET", path: "/healthz", pattern: "GET /healthz"},
		{method: "HEAD", path: "/healthz", pattern: "GET /healthz"},
		{method: "GET", path: "/api/users", pattern: "GET /api/users"},
		{method: "HEAD", path: "/api/users", pattern: "GET /api/users"},
		{method: "GET", path: "/api/admin/traffic", pattern: "GET /api/admin/traffic"},
		{method: "GET", path: "/api/admin/healthchecks", pattern: "GET /api/admin/healthchecks"},
		{method: "GET", path: "/ws", pattern: "GET /ws"},

		// SPA fallback: every other GET outside /api, as with the
		// gorilla catch-all.
		{method: "GET", path: "/", pattern: "GET /"},
		{method: "GET", path: "/assets/app.js", pattern: "GET /"},
		{method: "GET", path: "/some/client/route", pattern: "GET /"},
		{method: "GET", path: "/healthz/", pattern: "GET /"},

		// Unknown /api paths: JSON 404 rather than the SPA's index.html.
		{method: "GET", path: "/api/nope", pattern: "GET /api/", status: http.StatusNotFound},
		{method: "GET", path: "/api/users/", pattern: "GET /api/", status: http.StatusNotFound},
		{method: "GET", path: "/api/admin/nope", pattern: "GET /api/", status: http.StatusNotFound},

		// Wrong method: 405 with Allow instead of serving the SPA.
		{method: "POST", path: "/healthz", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
		{method: "POST", path: "/api/users", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
		{method: "DELETE", path: "/api/admin/traffic", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
		{method: "PUT", path: "/api/admin/healthchecks", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
		{method: "POST", path: "/ws", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
		{method: "POST", path: "/", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
		{method: "POST", path: "/some/client/route", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
		{method: "POST", path: "/api/nope", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},

		// Unclean paths redirect (307; gorilla used 301). ServeMux.Handler
		// reports the pattern the redirect target matches.
		{method: "GET", path: "//api/users", pattern: "GET /api/users", status: http.StatusTemporaryRedirect, location: "/api/users"},
		{method: "GET", path: "/api/../healthz", pattern: "GET /healthz", status: http.StatusTemporaryRedirect, location: "/healthz"},
		{method: "GET", path: "/api", pattern: "GET /api/", status: http.StatusTemporaryRedirect, location: "/api/"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			h, pattern := router.Handler(req)
			if pattern != tt.pattern {
				t.Fatalf("pattern = %q, want %q", pattern, tt.pattern)
			}
			if tt.status == 0 {
				return
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tt.status {
				t.Errorf("status = %d, want %d", rr.Code, tt.status)
			}
			if got := rr.Header().Get("Allow"); got != tt.allow {
				t.Errorf("Allow = %q, want %q", got, tt.allow)
			}
			if got := rr.Header().Get("Location"); got != tt.location {
				t.Errorf("Location = %q, want %q", got, tt.location)
			}
		})
	}
}

// TestAPIMiddlewareWrapsOnlyAPIRoutes checks the /api chain is applied
// to API routes (gzip, traffic recording) and that requests falling
// through to the SPA never reach the traffic recorder, so arbitrary
// paths cannot grow it.
func TestAPIMiddlewareWrapsOnlyAPIRoutes(t *testing.T) {
	router := newTestRouter()
	serve := func(method, path string, hdr ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}

	rr := serve("GET", "/api/users", "Accept-Encoding", "gzip")
	if got := rr.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("/api/users Vary = %q; gzip middleware not applied", got)
	}
	if rr := serve("GET", "/api/does-not-exist"); !strings.HasPrefix(rr.Header().Get("Content-Type"), "application/json") {
		t.Errorf("unknown /api path Content-Type = %q, want application/json", rr.Header().Get("Content-Type"))
	}
	serve("POST", "/api/users")
	// Every API route must be registered through handleAPI.
	serve("GET", "/api/admin/healthchecks")

	rr = serve("GET", "/api/admin/traffic")
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("/api/admin/traffic Content-Type = %q, want application/json", got)
	}
	var stats []middleware.TrafficStat
	if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode traffic: %v (body %q)", err, rr.Body.String())
	}
	var paths []string
	for _, s := range stats {
		paths = append(paths, s.Method+" "+s.Path)
	}
	for _, p := range []string{"GET /api/users", "GET /api/admin/healthchecks"} {
		if !slices.Contains(paths, p) {
			t.Errorf("traffic %v missing %s: route skips the /api middleware", paths, p)
		}
	}
	for _, p := range []string{"GET /api/does-not-exist", "POST /api/users"} {
		if slices.Contains(paths, p) {
			t.Errorf("traffic recorded unmatched request %q: %v", p, paths)
		}
	}
}
