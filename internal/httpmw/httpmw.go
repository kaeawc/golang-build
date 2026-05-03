// Package httpmw provides composable HTTP middleware for Go services.
//
// All middleware are `func(http.Handler) http.Handler` so they chain via
// `router.Use` (gorilla/mux, chi) or stack directly via `Compose`.
//
//	chain := httpmw.Compose(
//	    httpmw.RequestID(idgen.NewUUID()),
//	    httpmw.RealIP(),
//	    httpmw.Recover(log),
//	    httpmw.Logger(log),
//	    httpmw.SecureHeaders(httpmw.DefaultSecureHeaders),
//	)
//	mux.Handle("/", chain(handler))
//
// The package depends only on internal/logger and internal/idgen.
package httpmw

import (
	"context"
	"net/http"
	"strings"

	"github.com/kaeawc/golang-build/internal/idgen"
)

// MaxRequestIDLength bounds inbound X-Request-ID values so a hostile client
// can't pad logs or response headers with arbitrarily long strings.
const MaxRequestIDLength = 128

// Middleware is the standard func-of-Handler shape.
type Middleware func(http.Handler) http.Handler

// Compose returns a Middleware that applies mws in order: the first listed
// is the outermost, the last is closest to the wrapped handler. So
// `Compose(A, B, C)(h)` produces a handler whose request flows
// A → B → C → h, and whose response flows back h → C → B → A.
func Compose(mws ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	}
}

// ctxKey scopes context values to this package.
type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyRealIP
)

// RequestID middleware reads X-Request-ID or generates a fresh one via gen,
// stashes it on the request context, and echoes it back in the response.
//
// Inbound IDs are bounded to MaxRequestIDLength and stripped of CR/LF and
// other control characters so a hostile client cannot inject newlines into
// log lines or smuggle headers via the echoed response value.
func RequestID(gen idgen.Generator) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := sanitizeRequestID(r.Header.Get(HeaderRequestID))
			if id == "" {
				id = gen.Next()
			}
			w.Header().Set(HeaderRequestID, id)
			ctx := context.WithValue(r.Context(), ctxKeyRequestID, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func sanitizeRequestID(s string) string {
	if s == "" {
		return ""
	}
	if len(s) > MaxRequestIDLength {
		s = s[:MaxRequestIDLength]
	}
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return clean
}

// RequestIDFrom returns the request ID stored on r's context by RequestID,
// or "".
func RequestIDFrom(r *http.Request) string {
	if r == nil {
		return ""
	}
	if v, ok := r.Context().Value(ctxKeyRequestID).(string); ok {
		return v
	}
	return ""
}
