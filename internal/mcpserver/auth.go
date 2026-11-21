package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaeawc/golang-build/internal/clock"
	"github.com/kaeawc/golang-build/internal/tokens"
)

// OAuth scopes understood by the server.
const (
	ScopeRead  = "users:read"
	ScopeWrite = "users:write"
)

// AccessClaims is the bearer-token payload: an RFC 9068-style JWT access
// token with a space-delimited scope claim.
type AccessClaims struct {
	tokens.StandardClaims
	Scope string `json:"scope,omitempty"`
}

// TokenVerifier adapts an internal/tokens Verifier (HS256 JWTs) to the SDK's
// auth.TokenVerifier. audience must be the canonical resource URI of this
// server: tokens minted for a different resource are rejected, which is the
// spec's mitigation for token passthrough / confused-deputy attacks.
func TokenVerifier(v tokens.Verifier, audience string) auth.TokenVerifier {
	return func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		var c AccessClaims
		if err := v.Verify(token, &c); err != nil {
			return nil, fmt.Errorf("%w: %w", auth.ErrInvalidToken, err)
		}
		if c.Subject == "" {
			return nil, fmt.Errorf("%w: missing sub", auth.ErrInvalidToken)
		}
		if c.Audience != audience {
			return nil, fmt.Errorf("%w: audience %q is not %q", auth.ErrInvalidToken, c.Audience, audience)
		}
		info := &auth.TokenInfo{
			Scopes: strings.Fields(c.Scope),
			// UserID binds stateful sessions to the token's subject, so a
			// leaked Mcp-Session-Id is useless to another user.
			UserID: c.Subject,
		}
		if c.ExpiresAt != 0 {
			info.Expiration = time.Unix(c.ExpiresAt, 0)
		}
		return info, nil
	}
}

// MintToken issues a development access token. Real deployments get tokens
// from their authorization server; this exists so the demo can run end to
// end without one.
func MintToken(is tokens.Issuer, clk clock.Clock, subject, audience string, scopes []string, ttl time.Duration) (string, error) {
	now := clk.Now()
	return is.Sign(AccessClaims{
		Subject:   subject,
		Audience:  audience,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(ttl).Unix(),
		Scope:     strings.Join(scopes, " "),
	})
}

var errInsufficientScope = errors.New("insufficient scope")

// requireScope enforces per-tool scopes. Requests without TokenInfo come
// from unauthenticated transports (stdio, or HTTP with auth disabled),
// where the transport itself is the trust boundary.
func requireScope(req *mcp.CallToolRequest, scope string) error {
	if req.Extra == nil || req.Extra.TokenInfo == nil {
		return nil
	}
	if !slices.Contains(req.Extra.TokenInfo.Scopes, scope) {
		return fmt.Errorf("%w: this tool requires the %q scope", errInsufficientScope, scope)
	}
	return nil
}
