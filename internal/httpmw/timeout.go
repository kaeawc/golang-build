package httpmw

import (
	"context"
	"net/http"
	"time"
)

// Timeout middleware enforces a per-request deadline by setting a context
// timeout on the inbound request. Handlers that respect ctx.Done() will
// abort cleanly; those that don't will finish but the response may be
// truncated by net/http if a write happens after the parent times out.
//
// This is a context-only timeout — it does NOT race the handler against
// the deadline. For that, wrap with `http.TimeoutHandler`. The two compose:
// Timeout signals cancellation early so handlers can clean up; TimeoutHandler
// guarantees the response is bounded.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
