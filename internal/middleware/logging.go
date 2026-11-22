package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/kaeawc/golang-build/internal/httpmw"
	"github.com/kaeawc/golang-build/internal/logger"
)

// Logging derives a request-scoped logger from base (request_id, method,
// path), stores it on the request context for handlers and later
// middleware (logger.FromContext), and writes one access log line when
// the request finishes. Put httpmw.RequestID before it so request_id is
// set.
func Logging(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			// slog escapes values, so request-controlled strings cannot
			// forge log lines.
			l := base.With(
				"request_id", httpmw.RequestIDFrom(r),
				"method", r.Method,
				"path", r.URL.Path,
			)
			rw := &accessWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r.WithContext(logger.WithContext(r.Context(), l)))
			l.LogAttrs(r.Context(), slog.LevelInfo, "http request",
				slog.Int("status", rw.status),
				slog.Int64("bytes", rw.bytes),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}

// accessWriter records the status code and body size for the access log.
type accessWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (w *accessWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *accessWriter) Write(b []byte) (int, error) {
	w.wroteHeader = true
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *accessWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
