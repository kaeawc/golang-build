package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kaeawc/golang-build/internal/logger"
)

// BenchmarkRouter measures the public request path (routing, the /api
// middleware chain, handler, gzip) with the same fakes as the make pgo
// harness. Compare builds with and without the profile, see docs/pgo.md.
func BenchmarkRouter(b *testing.B) {
	log := logger.NewSlog(logger.Config{Writer: io.Discard, Format: logger.FormatJSON, Level: slog.LevelInfo})
	router := buildRouter(log, testPinger{}, harnessUsers(50), newMemCache())
	for _, path := range []string{"/api/users", "/healthz", "/api/admin/traffic", "/api/no-such-endpoint"} {
		b.Run(path, func(b *testing.B) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Accept-Encoding", "gzip")
			b.ReportAllocs()
			for b.Loop() {
				router.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}
