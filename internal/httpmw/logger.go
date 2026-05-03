package httpmw

import (
	"bufio"
	"errors"
	"net"
	"net/http"

	"github.com/kaeawc/golang-build/internal/clock"
	"github.com/kaeawc/golang-build/internal/logger"
)

// LoggerOption configures Logger.
type LoggerOption func(*loggerCfg)

type loggerCfg struct {
	clk clock.Clock
}

// WithLoggerClock injects a clock.Clock so the access log's durationMs is
// deterministic in tests via clock.Fake.
func WithLoggerClock(clk clock.Clock) LoggerOption {
	return func(c *loggerCfg) {
		if clk != nil {
			c.clk = clk
		}
	}
}

// Logger middleware emits one access log per request via log. Status code,
// bytes written, and duration are captured via a recording response writer.
// Place this near the top of the chain so it sees the final status code
// (after Recover translates panics to 500s).
func Logger(log logger.Logger, opts ...LoggerOption) Middleware {
	cfg := loggerCfg{clk: clock.Default}
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := cfg.clk.Now()
			rw := newRecordingWriter(w)
			next.ServeHTTP(rw, r)

			if log == nil {
				return
			}
			log.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.status,
				"bytes", rw.bytes,
				"durationMs", cfg.clk.Now().Sub(start).Milliseconds(),
				"remoteIp", RealIPFrom(r),
				"requestId", RequestIDFrom(r),
				"userAgent", r.UserAgent(),
			)
		})
	}
}

// recordingWriter captures status code and bytes written for the access log
// while transparently forwarding optional ResponseWriter interfaces
// (http.Flusher, http.Hijacker) to the wrapped writer so streaming and
// websocket-upgrade handlers continue to work behind the middleware.
type recordingWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func newRecordingWriter(w http.ResponseWriter) *recordingWriter {
	return &recordingWriter{ResponseWriter: w, status: http.StatusOK}
}

func (rw *recordingWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.wroteHeader = true
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *recordingWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytes += int64(n)
	return n, err
}

// Flush forwards to the wrapped writer if it implements http.Flusher.
func (rw *recordingWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the wrapped writer if it implements http.Hijacker so
// websocket upgrades and other connection takeovers continue to work behind
// this middleware. After a successful Hijack the connection is owned by the
// caller and the access log's status/bytes will reflect only what was
// written before the hijack.
func (rw *recordingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("httpmw: underlying ResponseWriter does not support Hijack")
	}
	return h.Hijack()
}
