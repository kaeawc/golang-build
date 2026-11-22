package main

import (
	"bytes"
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

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/kaeawc/golang-build/internal/db"
	"github.com/kaeawc/golang-build/internal/env"
	"github.com/kaeawc/golang-build/internal/handlers"
	"github.com/kaeawc/golang-build/internal/httpmw"
	"github.com/kaeawc/golang-build/internal/middleware"
	"github.com/kaeawc/golang-build/internal/telemetry"
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

func TestHTTPRoute(t *testing.T) {
	for pattern, want := range map[string]string{
		"GET /api/users":           "/api/users",
		"/ws":                      "/ws",
		"GET example.com/api/{id}": "/api/{id}",
		"":                         "",
	} {
		if got := httpRoute(pattern); got != want {
			t.Errorf("httpRoute(%q) = %q, want %q", pattern, got, want)
		}
	}
}

// TestInstrumentTracesRoutesAndJoinsLogs checks the otelhttp wrapper names
// server spans after the ServeMux pattern, skips /healthz, and that the
// request-scoped access log line carries the span's trace_id.
func TestInstrumentTracesRoutesAndJoinsLogs(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	var logs bytes.Buffer
	log := slog.New(telemetry.LogHandler(slog.NewJSONHandler(&logs, nil)))
	h := instrument(buildRouter(log, testPinger{}, &testQuerier{}, &testCache{}), otelhttp.WithTracerProvider(tp))

	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/api/users"},
		{http.MethodPost, "/api/users"},
		{http.MethodGet, "/healthz"},
	} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(req.method, req.path, nil))
	}

	var names []string
	var usersSpan sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		names = append(names, s.Name())
		if s.Name() == "GET /api/users" {
			usersSpan = s
		}
	}
	if !slices.Equal(names, []string{"GET /api/users", "POST"}) {
		t.Fatalf("span names = %q, want [GET /api/users POST] (/healthz untraced)", names)
	}
	var route string
	for _, a := range usersSpan.Attributes() {
		if a.Key == "http.route" {
			route = a.Value.AsString()
		}
	}
	if route != "/api/users" {
		t.Errorf("http.route = %q, want /api/users", route)
	}

	var access map[string]any
	if err := json.NewDecoder(&logs).Decode(&access); err != nil {
		t.Fatalf("decode access log: %v (%q)", err, logs.String())
	}
	if access["msg"] != "http request" || access["trace_id"] != usersSpan.SpanContext().TraceID().String() {
		t.Errorf("access log = %v, want trace_id %s", access, usersSpan.SpanContext().TraceID())
	}
}

// Span names follow otelhttp's semconv naming (HEAD keeps its own method,
// unknown methods become "HTTP"), and a request that panics past the
// handler still gets its route.
func TestInstrumentSpanNames(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	h := instrument(mux, otelhttp.WithTracerProvider(tp))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodHead, "/items/7", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("FOOBAR1234", "/items/7", nil))
	func() {
		defer func() { _ = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/boom", nil))
	}()

	var got []string
	for _, s := range rec.Ended() {
		route := ""
		for _, a := range s.Attributes() {
			if a.Key == "http.route" {
				route = a.Value.AsString()
			}
		}
		got = append(got, s.Name()+" route="+route)
	}
	want := []string{"HEAD /items/{id} route=/items/{id}", "HTTP route=", "GET /boom route=/boom"}
	if !slices.Equal(got, want) {
		t.Errorf("spans = %q, want %q", got, want)
	}
}
