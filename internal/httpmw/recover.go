package httpmw

import (
	"errors"
	"net/http"
	"runtime/debug"

	"github.com/kaeawc/golang-build/internal/logger"
)

// Recover middleware catches panics from downstream handlers, logs them
// via log, and writes a 500 response. The original panic stack is logged
// at error level. If log is nil, panics are silently caught and only the
// 500 is written.
func Recover(log logger.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// http.ErrAbortHandler is the documented way to bail out of
				// a handler without logging; honor it.
				if recErr, ok := rec.(error); ok && errors.Is(recErr, http.ErrAbortHandler) {
					panic(rec)
				}
				if log != nil {
					log.Error("panic in handler",
						"err", rec,
						"method", r.Method,
						"path", r.URL.Path,
						"requestId", RequestIDFrom(r),
						"stack", string(debug.Stack()),
					)
				}
				// Don't try to write the body if headers are already out;
				// http will swallow the second WriteHeader call but log the
				// attempt.
				w.WriteHeader(http.StatusInternalServerError)
			}()
			next.ServeHTTP(w, r)
		})
	}
}
