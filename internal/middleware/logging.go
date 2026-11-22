package middleware

import (
	"bufio"
	"errors"
	"log/slog"
	"net"
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
			completed := false
			// Deferred so a panic that escapes Recover (http.ErrAbortHandler)
			// still leaves an access line for the request.
			defer func() {
				attrs := []slog.Attr{
					slog.Int("status", rw.status),
					slog.Int64("bytes", rw.bytes),
					slog.Int64("duration_ms", time.Since(start).Milliseconds()),
					slog.String("remote_addr", r.RemoteAddr),
				}
				if r.URL.RawQuery != "" {
					attrs = append(attrs, slog.String("query", r.URL.RawQuery))
				}
				if !completed {
					attrs = append(attrs, slog.Bool("aborted", true))
				}
				l.LogAttrs(r.Context(), slog.LevelInfo, "http request", attrs...)
			}()
			next.ServeHTTP(rw, r.WithContext(logger.WithContext(r.Context(), l)))
			completed = true
		})
	}
}

// accessWriter records the final status code and body size for the
// access log. Informational (1xx) headers pass through without being
// recorded as the status; only the first final status is kept.
type accessWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (w *accessWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	if code >= 200 || code == http.StatusSwitchingProtocols {
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

// Flush forwards to the wrapped writer when it supports http.Flusher.
func (w *accessWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the wrapped writer so websocket upgrades work
// behind this middleware.
func (w *accessWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("middleware: ResponseWriter does not support Hijack")
	}
	conn, rw, err := h.Hijack()
	if err == nil {
		w.wroteHeader = true
		w.status = http.StatusSwitchingProtocols
	}
	return conn, rw, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *accessWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
