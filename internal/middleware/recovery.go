package middleware

import (
	"errors"
	"net/http"
	"runtime/debug"

	"github.com/kaeawc/golang-build/internal/logger"
)

// Recover turns a panic in a downstream handler into a 500 and logs it,
// with its stack, through the request-scoped logger (so the record
// carries request_id, method and path when Logging runs first).
// http.ErrAbortHandler is re-panicked, as net/http expects.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}
			logger.FromContext(r.Context()).Error("panic recovered",
				"panic", rec,
				"stack", string(debug.Stack()),
			)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}
