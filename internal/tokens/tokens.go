// Package tokens implements signed JSON tokens (JWT-compatible) for
// session/auth flows.
//
// The format is the standard three-part JWT: base64url(header) + "." +
// base64url(payload) + "." + base64url(signature). The header is fixed
// to {"alg":"HS256","typ":"JWT"} so any standard JWT library can verify
// the token. The payload is the caller's claims struct serialized as
// JSON.
//
// Production:
//
//	is := tokens.NewHMAC256([]byte(os.Getenv("JWT_SECRET")))
//	tok, _ := is.Sign(MyClaims{StandardClaims: tokens.StandardClaims{
//	    Subject: userID, ExpiresAt: time.Now().Add(time.Hour).Unix(),
//	}})
//	var got MyClaims
//	_ = is.Verify(tok, &got)
//
// Tests:
//
//	is := tokens.AlwaysValid(MyClaims{Subject: "u_1"})
//	_, _ = is.Sign(MyClaims{}) // returns "fake-1", "fake-2", ...
//	_ = is.Verify("anything", &got) // populates with the configured claims
//
// Scope: HMAC-SHA256 only. No RSA/ECDSA key management — those land
// downstream when the project actually needs asymmetric signing.
package tokens

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strings"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

// Sentinel errors for verification failures. Callers can errors.Is-check
// to distinguish "client sent garbage" from "token expired".
var (
	// ErrMalformed is returned when the token is not three base64-URL
	// segments separated by dots.
	ErrMalformed = errors.New("tokens: malformed")
	// ErrSignature is returned when the HMAC signature does not match.
	ErrSignature = errors.New("tokens: signature mismatch")
	// ErrAlgorithm is returned when the header's alg field is not "HS256"
	// (defends against alg=none and alg-substitution attacks).
	ErrAlgorithm = errors.New("tokens: unsupported algorithm")
	// ErrExpired is returned when exp is in the past (with leeway).
	ErrExpired = errors.New("tokens: expired")
	// ErrNotYetValid is returned when nbf is in the future (with leeway).
	ErrNotYetValid = errors.New("tokens: not yet valid")
)

// StandardClaims mirrors the registered JWT claims (RFC 7519). Embed it
// in your own claims struct.
type StandardClaims struct {
	Subject   string `json:"sub,omitempty"`
	Issuer    string `json:"iss,omitempty"`
	Audience  string `json:"aud,omitempty"`
	JWTID     string `json:"jti,omitempty"`
	IssuedAt  int64  `json:"iat,omitempty"`
	ExpiresAt int64  `json:"exp,omitempty"`
	NotBefore int64  `json:"nbf,omitempty"`
}

// Issuer signs claims and returns a JWT-encoded token.
type Issuer interface {
	Sign(claims any) (string, error)
}

// Verifier verifies a token's signature, time validity, and decodes the
// claims into dst (a pointer to the caller's claims struct).
type Verifier interface {
	Verify(token string, dst any) error
}

// IssuerVerifier is the union; HMAC256 and the fakes satisfy both.
type IssuerVerifier interface {
	Issuer
	Verifier
}

// Option configures HMAC256 / fakes.
type Option func(*config)

type config struct {
	clk    clock.Clock
	leeway time.Duration
}

// WithClock injects a clock.Clock so exp/nbf enforcement is deterministic
// in tests. Defaults to clock.Default.
func WithClock(clk clock.Clock) Option {
	return func(c *config) {
		if clk != nil {
			c.clk = clk
		}
	}
}

// WithLeeway permits a small clock-skew tolerance when checking exp/nbf
// (e.g. 30s). Default 0.
func WithLeeway(d time.Duration) Option {
	return func(c *config) { c.leeway = d }
}

func newConfig(opts []Option) config {
	c := config{clk: clock.Default}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// HMAC256 issues and verifies HS256 JWTs.
type HMAC256 struct {
	secret []byte
	cfg    config
}

// NewHMAC256 returns an HMAC256 signed with secret. The secret must not
// be empty; for production use 32+ random bytes.
func NewHMAC256(secret []byte, opts ...Option) *HMAC256 {
	if len(secret) == 0 {
		panic("tokens.NewHMAC256: secret must not be empty")
	}
	return &HMAC256{secret: secret, cfg: newConfig(opts)}
}

// jwtHeader is fixed for HS256.
const jwtHeader = `{"alg":"HS256","typ":"JWT"}`

var jwtHeaderEncoded = base64.RawURLEncoding.EncodeToString([]byte(jwtHeader))

// Sign serializes claims as JSON and HMAC-signs the token.
func (h *HMAC256) Sign(claims any) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("tokens: marshal claims: %w", err)
	}
	payloadEnc := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := jwtHeaderEncoded + "." + payloadEnc
	sig := h.sign([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify checks signature, alg, time validity, and decodes claims into dst.
func (h *HMAC256) Verify(token string, dst any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ErrMalformed
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("%w: header base64: %w", ErrMalformed, err)
	}
	if err := h.verifyAlgorithm(headerRaw); err != nil {
		return err
	}

	signingInput := parts[0] + "." + parts[1]
	expected := h.sign([]byte(signingInput))
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("%w: signature base64: %w", ErrMalformed, err)
	}
	if !hmac.Equal(expected, got) {
		return ErrSignature
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("%w: payload base64: %w", ErrMalformed, err)
	}
	if err := h.checkTimeClaims(payload); err != nil {
		return err
	}
	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(payload, dst); err != nil {
		return fmt.Errorf("%w: payload json: %w", ErrMalformed, err)
	}
	return nil
}

func (h *HMAC256) verifyAlgorithm(headerRaw []byte) error {
	var hdr struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerRaw, &hdr); err != nil {
		return fmt.Errorf("%w: header json: %w", ErrMalformed, err)
	}
	if hdr.Alg != "HS256" {
		return fmt.Errorf("%w: got %q", ErrAlgorithm, hdr.Alg)
	}
	return nil
}

// checkTimeClaims enforces exp and nbf using cfg.clk and cfg.leeway.
// Claims without these fields are accepted (zero values are skipped).
// A payload that isn't a JSON object (rare — Sign always emits one) has
// no time claims to enforce, so we accept it: the signature was valid.
func (h *HMAC256) checkTimeClaims(payload []byte) error {
	var times struct {
		ExpiresAt int64 `json:"exp,omitempty"`
		NotBefore int64 `json:"nbf,omitempty"`
	}
	if err := json.Unmarshal(payload, &times); err != nil {
		//nolint:nilerr // non-object payload has no time claims to enforce; signature was valid
		return nil
	}
	now := h.cfg.clk.Now()
	if times.ExpiresAt != 0 {
		exp := time.Unix(times.ExpiresAt, 0)
		if now.After(exp.Add(h.cfg.leeway)) {
			return ErrExpired
		}
	}
	if times.NotBefore != 0 {
		nbf := time.Unix(times.NotBefore, 0)
		if now.Before(nbf.Add(-h.cfg.leeway)) {
			return ErrNotYetValid
		}
	}
	return nil
}

func (h *HMAC256) sign(input []byte) []byte {
	mac := newMAC(h.secret)
	_, _ = mac.Write(input)
	return mac.Sum(nil)
}

func newMAC(key []byte) hash.Hash { return hmac.New(sha256.New, key) }
