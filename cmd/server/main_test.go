package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/db"
	"github.com/kaeawc/golang-build/internal/handlers"
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

// newTestRouter builds the real routing table. The pool is nil, so tests
// must not invoke handlers that touch it (/healthz, /api/admin/healthchecks);
// the table test resolves those with ServeMux.Handler instead.
func newTestRouter() *http.ServeMux {
	return buildRouter(nil, &testQuerier{}, &testCache{})
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

		// SPA fallback: every other GET, including unknown /api paths
		// and trailing-slash variants, as with the gorilla catch-all.
		{method: "GET", path: "/", pattern: "GET /"},
		{method: "GET", path: "/assets/app.js", pattern: "GET /"},
		{method: "GET", path: "/some/client/route", pattern: "GET /"},
		{method: "GET", path: "/healthz/", pattern: "GET /"},
		{method: "GET", path: "/api/nope", pattern: "GET /"},
		{method: "GET", path: "/api/users/", pattern: "GET /"},

		// Wrong method: 405 with Allow instead of serving the SPA.
		{method: "POST", path: "/healthz", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
		{method: "POST", path: "/api/users", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
		{method: "DELETE", path: "/api/admin/traffic", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
		{method: "PUT", path: "/api/admin/healthchecks", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
		{method: "POST", path: "/ws", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
		{method: "POST", path: "/", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
		{method: "POST", path: "/some/client/route", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},

		// Unclean paths redirect (307; gorilla used 301). ServeMux.Handler
		// reports the pattern the redirect target matches.
		{method: "GET", path: "//api/users", pattern: "GET /api/users", status: http.StatusTemporaryRedirect, location: "/api/users"},
		{method: "GET", path: "/api/../healthz", pattern: "GET /healthz", status: http.StatusTemporaryRedirect, location: "/healthz"},
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
	serve("GET", "/api/does-not-exist")
	serve("POST", "/api/users")

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
	if !slices.Contains(paths, "GET /api/users") {
		t.Errorf("traffic %v missing GET /api/users", paths)
	}
	for _, p := range []string{"GET /api/does-not-exist", "POST /api/users"} {
		if slices.Contains(paths, p) {
			t.Errorf("traffic recorded unmatched request %q: %v", p, paths)
		}
	}
}
