// Package auth provides a generic authentication interface and a few
// drop-in implementations suitable for templating internal tools.
//
// The Authenticator interface returns an Identity for an incoming
// http.Request (or an error). Implementations live alongside it:
//
//   - Static       — a fixed identity for local development and tests
//   - CloudflareAccess — verifies a Cf-Access-Jwt-Assertion header against
//     a team's JWKS
//   - Tailscale    — resolves identity by looking up the request's
//     remote address via an injected WhoIs-style resolver
//
// The middleware Require attaches the resolved Identity to the request
// context. Handlers retrieve it with IdentityFrom.
package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/kaeawc/golang-build/internal/httpmw"
	"github.com/kaeawc/golang-build/internal/logger"
)

// Identity describes the authenticated principal.
//
// Subject is the stable, opaque identifier for the user (provider-specific:
// Cloudflare uses the access token's `sub`, Tailscale uses the login name).
// Email is best-effort — empty when the provider does not expose it.
// Provider names which Authenticator produced this identity, for logging
// and policy decisions ("cloudflare", "tailscale", "static").
// Claims is a free-form bag of provider-specific extras (groups, IdP, etc).
type Identity struct {
	Subject  string
	Email    string
	Provider string
	Claims   map[string]any
}

// Authenticator extracts an Identity from an incoming request.
//
// Implementations return ErrNoIdentity when the request carries no
// credentials at all, ErrInvalidToken when credentials are present but
// fail verification, and any other error for transport / configuration
// problems (e.g. JWKS fetch failure).
type Authenticator interface {
	Identify(r *http.Request) (Identity, error)
}

// Sentinel errors. Middleware distinguishes these from infrastructure
// errors so the response code is correct (401 vs 500).
var (
	ErrNoIdentity   = errors.New("auth: no identity")
	ErrInvalidToken = errors.New("auth: invalid token")
)

type ctxKey int

const ctxKeyIdentity ctxKey = 0

// WithIdentity returns a copy of ctx carrying id.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKeyIdentity, id)
}

// IdentityFrom returns the identity stashed by Require, and ok=false if
// the request was not authenticated.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKeyIdentity).(Identity)
	return id, ok
}

// Require returns middleware that calls a.Identify on every request,
// stashes the identity on the context, and rejects unauthenticated
// requests with 401. Internal errors (e.g. JWKS unreachable) are logged
// via log and surfaced as 500 so monitoring catches them.
func Require(a Authenticator, log logger.Logger) httpmw.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := a.Identify(r)
			if err != nil {
				switch {
				case errors.Is(err, ErrNoIdentity), errors.Is(err, ErrInvalidToken):
					http.Error(w, "unauthorized", http.StatusUnauthorized)
				default:
					if log != nil {
						log.Error("auth: identify failed",
							"err", err,
							"path", r.URL.Path,
							"requestId", httpmw.RequestIDFrom(r),
						)
					}
					http.Error(w, "internal error", http.StatusInternalServerError)
				}
				return
			}
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
		})
	}
}
