package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

// jwksCache fetches and caches a JWKS document. It refreshes on a TTL,
// and on a kid miss it does one in-flight refresh before declaring the
// key unknown — that handles Cloudflare's key rotation transparently.
type jwksCache struct {
	url    string
	client *http.Client
	clk    clock.Clock
	ttl    time.Duration

	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastRefresh time.Time
}

func newJWKSCache(url string, client *http.Client, clk clock.Clock, ttl time.Duration) *jwksCache {
	if client == nil {
		client = http.DefaultClient
	}
	if clk == nil {
		clk = clock.Default
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &jwksCache{url: url, client: client, clk: clk, ttl: ttl}
}

// key returns the RSA public key for kid, refreshing the JWKS at most
// once per minRefreshInterval if the kid is missing or the cache is stale.
func (c *jwksCache) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	now := c.clk.Now()
	stale := c.keys == nil || now.Sub(c.fetchedAt) > c.ttl
	if k, ok := c.keys[kid]; ok && !stale {
		c.mu.Unlock()
		return k, nil
	}
	// Rate-limit refresh attempts so a stream of requests with bogus kids
	// doesn't hammer Cloudflare.
	canRefresh := now.Sub(c.lastRefresh) > 30*time.Second
	c.mu.Unlock()

	if canRefresh {
		if err := c.refresh(ctx); err != nil {
			return nil, err
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("%w: unknown key id %q", ErrInvalidToken, kid)
}

func (c *jwksCache) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("auth: build jwks request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("auth: fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("auth: jwks status %d", resp.StatusCode)
	}
	var doc struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("auth: decode jwks: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := decodeRSAPublicKey(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	c.mu.Lock()
	c.keys = keys
	c.fetchedAt = c.clk.Now()
	c.lastRefresh = c.fetchedAt
	c.mu.Unlock()
	return nil
}

func decodeRSAPublicKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, err
	}
	// RFC 7518: e is unsigned big-endian. Real RSA public exponents are
	// tiny (3, 17, 65537); cap at 4 bytes so the value always fits in
	// int on 32-bit platforms.
	if len(eBytes) > 4 {
		return nil, errors.New("auth: rsa exponent too large")
	}
	var eBuf [4]byte
	copy(eBuf[4-len(eBytes):], eBytes)
	e := binary.BigEndian.Uint32(eBuf[:])
	if e > math.MaxInt32 {
		return nil, errors.New("auth: rsa exponent too large")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e)}, nil
}

// verifyRS256 verifies a JWT signed with RS256, returning the decoded
// payload (claims JSON) on success. It does NOT enforce time claims —
// callers do that against their own clock.
func verifyRS256(token string, getKey func(kid string) (*rsa.PublicKey, error)) ([]byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: not three segments", ErrInvalidToken)
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: header b64: %w", ErrInvalidToken, err)
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerRaw, &hdr); err != nil {
		return nil, fmt.Errorf("%w: header json: %w", ErrInvalidToken, err)
	}
	if hdr.Alg != "RS256" {
		return nil, fmt.Errorf("%w: alg %q", ErrInvalidToken, hdr.Alg)
	}
	if hdr.Kid == "" {
		return nil, fmt.Errorf("%w: missing kid", ErrInvalidToken)
	}
	key, err := getKey(hdr.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: sig b64: %w", ErrInvalidToken, err)
	}
	signingInput := parts[0] + "." + parts[1]
	hashed := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, hashed[:], sig); err != nil {
		return nil, fmt.Errorf("%w: signature: %w", ErrInvalidToken, err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: payload b64: %w", ErrInvalidToken, err)
	}
	return payload, nil
}
