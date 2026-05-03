package auth

import (
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

// HeaderCloudflareAccess is the JWT header Cloudflare Access injects on
// every request that passes its edge.
const HeaderCloudflareAccess = "Cf-Access-Jwt-Assertion"

// CloudflareConfig configures CloudflareAccess.
type CloudflareConfig struct {
	// TeamDomain is the Cloudflare Zero Trust team subdomain — e.g.
	// "myteam" for "myteam.cloudflareaccess.com". Required.
	TeamDomain string
	// AppAUD is the application audience (AUD) tag from the Access app
	// configuration. Verified against the token's `aud` claim. Required.
	AppAUD string
	// HTTPClient overrides the JWKS fetch client (defaults to http.DefaultClient).
	HTTPClient *http.Client
	// Clock overrides the clock used for exp/nbf checks (defaults to clock.Default).
	Clock clock.Clock
	// Leeway permits small clock skew on exp/nbf (default 30s).
	Leeway time.Duration
	// JWKSCacheTTL controls how long a fetched JWKS is reused (default 10m).
	JWKSCacheTTL time.Duration
}

// CloudflareAccess verifies the Cf-Access-Jwt-Assertion header against
// the team's JWKS and the configured application AUD.
type CloudflareAccess struct {
	cfg   CloudflareConfig
	jwks  *jwksCache
	clk   clock.Clock
	issue string // expected `iss`
}

// NewCloudflareAccess constructs a CloudflareAccess Authenticator.
// Panics if TeamDomain or AppAUD is empty — these are required for any
// meaningful verification.
func NewCloudflareAccess(cfg CloudflareConfig) *CloudflareAccess {
	if cfg.TeamDomain == "" {
		panic("auth: CloudflareConfig.TeamDomain is required")
	}
	if cfg.AppAUD == "" {
		panic("auth: CloudflareConfig.AppAUD is required")
	}
	if cfg.Leeway == 0 {
		cfg.Leeway = 30 * time.Second
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.Default
	}
	base := "https://" + cfg.TeamDomain + ".cloudflareaccess.com"
	return &CloudflareAccess{
		cfg:   cfg,
		jwks:  newJWKSCache(base+"/cdn-cgi/access/certs", cfg.HTTPClient, clk, cfg.JWKSCacheTTL),
		clk:   clk,
		issue: base,
	}
}

// Identify reads HeaderCloudflareAccess, verifies the JWT, and returns
// the resulting Identity.
func (c *CloudflareAccess) Identify(r *http.Request) (Identity, error) {
	tok := r.Header.Get(HeaderCloudflareAccess)
	if tok == "" {
		return Identity{}, ErrNoIdentity
	}
	payload, err := verifyRS256(tok, func(kid string) (*rsa.PublicKey, error) {
		return c.jwks.key(r.Context(), kid)
	})
	if err != nil {
		return Identity{}, err
	}

	var claims struct {
		Sub      string   `json:"sub"`
		Email    string   `json:"email"`
		Iss      string   `json:"iss"`
		Aud      audience `json:"aud"`
		Exp      int64    `json:"exp"`
		Nbf      int64    `json:"nbf"`
		Identity string   `json:"identity_nonce"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Identity{}, fmt.Errorf("%w: claims json: %w", ErrInvalidToken, err)
	}
	if !strings.EqualFold(strings.TrimSuffix(claims.Iss, "/"), c.issue) {
		return Identity{}, fmt.Errorf("%w: iss %q", ErrInvalidToken, claims.Iss)
	}
	if !claims.Aud.contains(c.cfg.AppAUD) {
		return Identity{}, fmt.Errorf("%w: aud mismatch", ErrInvalidToken)
	}
	now := c.clk.Now()
	if claims.Exp != 0 && now.After(time.Unix(claims.Exp, 0).Add(c.cfg.Leeway)) {
		return Identity{}, fmt.Errorf("%w: expired", ErrInvalidToken)
	}
	if claims.Nbf != 0 && now.Before(time.Unix(claims.Nbf, 0).Add(-c.cfg.Leeway)) {
		return Identity{}, fmt.Errorf("%w: not yet valid", ErrInvalidToken)
	}

	// Surface the full claim bag so callers can read groups/IdP/etc.
	var bag map[string]any
	_ = json.Unmarshal(payload, &bag)
	return Identity{
		Subject:  claims.Sub,
		Email:    claims.Email,
		Provider: "cloudflare",
		Claims:   bag,
	}, nil
}

// audience handles the JWT `aud` field which may be a string or a string array.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	if b[0] == '[' {
		var arr []string
		if err := json.Unmarshal(b, &arr); err != nil {
			return err
		}
		*a = arr
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*a = []string{s}
	return nil
}

func (a audience) contains(want string) bool {
	for _, v := range a {
		if v == want {
			return true
		}
	}
	return false
}
