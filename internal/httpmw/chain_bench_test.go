package httpmw_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/httpmw"
	"github.com/kaeawc/golang-build/internal/idgen"
	"github.com/kaeawc/golang-build/internal/logger"
)

// discardWriter is a reusable ResponseWriter so the benchmark measures the
// middleware, not httptest.ResponseRecorder's buffering.
type discardWriter struct {
	h    http.Header
	code int
}

func (w *discardWriter) Header() http.Header         { return w.h }
func (w *discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *discardWriter) WriteHeader(code int)        { w.code = code }

// BenchmarkChain measures per-request middleware overhead through the
// package's exported API, from a bare handler up to a typical production
// stack. Subtract "bare" to get the cost the middleware adds.
func BenchmarkChain(b *testing.B) {
	log := logger.Discard()
	body := []byte(`{"ok":true}`)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})

	cases := []struct {
		name  string
		chain httpmw.Middleware
	}{
		{"bare", httpmw.Compose()},
		{"requestid", httpmw.Compose(httpmw.RequestID(idgen.UUID{}))},
		{"production", httpmw.Compose(
			httpmw.RequestID(idgen.UUID{}),
			httpmw.RealIP(httpmw.NewTrustedProxies("10.0.0.0/8")),
			httpmw.Recover(log),
			httpmw.Logger(log),
			httpmw.Timeout(5*time.Second),
			httpmw.SecureHeaders(httpmw.DefaultSecureHeaders),
			httpmw.CORS(httpmw.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}),
		)},
	}
	for _, bc := range cases {
		b.Run(bc.name, func(b *testing.B) {
			h := bc.chain(handler)
			req := httptest.NewRequest(http.MethodGet, "/api/users/7", nil)
			req.RemoteAddr = "10.1.2.3:4567"
			req.Header.Set("X-Forwarded-For", "203.0.113.9")
			req.Header.Set("Origin", "https://app.example.com")
			w := &discardWriter{h: http.Header{}}
			b.ReportAllocs()
			for b.Loop() {
				clear(w.h)
				h.ServeHTTP(w, req)
			}
			if w.code != 0 && w.code != http.StatusOK {
				b.Fatalf("status %d", w.code)
			}
		})
	}
}
