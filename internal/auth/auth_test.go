package auth_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/auth"
	"github.com/kaeawc/golang-build/internal/clock"
)

func TestStaticIdentify(t *testing.T) {
	a := auth.Static(auth.Identity{Subject: "u_1", Email: "a@b"})
	id, err := a.Identify(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if id.Subject != "u_1" || id.Email != "a@b" || id.Provider != "static" {
		t.Fatalf("unexpected identity: %+v", id)
	}
}

func TestRequireAttachesIdentity(t *testing.T) {
	a := auth.Static(auth.Identity{Subject: "u_1"})
	mw := auth.Require(a, nil)
	var seen auth.Identity
	var ok bool
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, ok = auth.IdentityFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204", rec.Code)
	}
	if !ok || seen.Subject != "u_1" {
		t.Fatalf("identity not attached: ok=%v id=%+v", ok, seen)
	}
}

func TestRequireUnauthorizedAndInternal(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"no-identity", auth.ErrNoIdentity, http.StatusUnauthorized},
		{"invalid-token", auth.ErrInvalidToken, http.StatusUnauthorized},
		{"infra", errors.New("jwks unreachable"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mw := auth.Require(failAuth{err: tc.err}, nil)
			rec := httptest.NewRecorder()
			mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("handler should not run")
			})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if rec.Code != tc.want {
				t.Fatalf("status: got %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

type failAuth struct{ err error }

func (f failAuth) Identify(*http.Request) (auth.Identity, error) {
	return auth.Identity{}, f.err
}

func TestTailscaleResolverWiring(t *testing.T) {
	called := false
	a := auth.Tailscale(func(_ context.Context, addr string) (auth.Identity, error) {
		called = true
		if addr == "" {
			t.Fatalf("empty remote addr reached resolver")
		}
		return auth.Identity{Subject: "alice@example.com"}, nil
	})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "100.64.0.5:42"
	id, err := a.Identify(r)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if !called {
		t.Fatal("resolver was not invoked")
	}
	if id.Subject != "alice@example.com" || id.Provider != "tailscale" {
		t.Fatalf("unexpected identity: %+v", id)
	}
}

func TestTailscaleEmptyRemoteAddr(t *testing.T) {
	a := auth.Tailscale(func(context.Context, string) (auth.Identity, error) {
		t.Fatal("resolver should not be called for empty RemoteAddr")
		return auth.Identity{}, nil
	})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = ""
	if _, err := a.Identify(r); !errors.Is(err, auth.ErrNoIdentity) {
		t.Fatalf("got %v, want ErrNoIdentity", err)
	}
}

func TestCloudflareAccess_HappyPath(t *testing.T) {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	const kid = "k1"
	jwksSrv := newJWKSServer(t, kid, &k.PublicKey)
	defer jwksSrv.Close()

	// CloudflareAccess hard-codes the JWKS URL based on TeamDomain, so
	// rewrite the cert host to our test server via a custom transport.
	hc := &http.Client{Transport: rewriteHost{target: jwksSrv.URL}}

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ca := auth.NewCloudflareAccess(auth.CloudflareConfig{
		TeamDomain: "test",
		AppAUD:     "aud-123",
		HTTPClient: hc,
		Clock:      fakeClock{t: now},
	})

	tok := signJWT(t, k, kid, map[string]any{
		"iss":   "https://test.cloudflareaccess.com",
		"aud":   "aud-123",
		"sub":   "user-1",
		"email": "u@example.com",
		"exp":   now.Add(5 * time.Minute).Unix(),
		"nbf":   now.Add(-time.Minute).Unix(),
	})

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(auth.HeaderCloudflareAccess, tok)
	id, err := ca.Identify(r)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if id.Subject != "user-1" || id.Email != "u@example.com" || id.Provider != "cloudflare" {
		t.Fatalf("unexpected identity: %+v", id)
	}
	if _, ok := id.Claims["iss"]; !ok {
		t.Fatalf("claims bag did not include iss: %+v", id.Claims)
	}
}

func TestCloudflareAccess_Rejects(t *testing.T) {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	const kid = "k1"
	jwksSrv := newJWKSServer(t, kid, &k.PublicKey)
	defer jwksSrv.Close()
	hc := &http.Client{Transport: rewriteHost{target: jwksSrv.URL}}

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ca := auth.NewCloudflareAccess(auth.CloudflareConfig{
		TeamDomain: "test",
		AppAUD:     "aud-123",
		HTTPClient: hc,
		Clock:      fakeClock{t: now},
	})

	mk := func(claims map[string]any) string { return signJWT(t, k, kid, claims) }

	cases := []struct {
		name  string
		token string
		want  error
	}{
		{
			name:  "no header",
			token: "",
			want:  auth.ErrNoIdentity,
		},
		{
			name: "wrong aud",
			token: mk(map[string]any{
				"iss": "https://test.cloudflareaccess.com",
				"aud": "other", "sub": "u", "exp": now.Add(time.Hour).Unix(),
			}),
			want: auth.ErrInvalidToken,
		},
		{
			name: "wrong iss",
			token: mk(map[string]any{
				"iss": "https://evil.cloudflareaccess.com",
				"aud": "aud-123", "sub": "u", "exp": now.Add(time.Hour).Unix(),
			}),
			want: auth.ErrInvalidToken,
		},
		{
			name: "expired",
			token: mk(map[string]any{
				"iss": "https://test.cloudflareaccess.com",
				"aud": "aud-123", "sub": "u",
				"exp": now.Add(-time.Hour).Unix(),
			}),
			want: auth.ErrInvalidToken,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.token != "" {
				r.Header.Set(auth.HeaderCloudflareAccess, tc.token)
			}
			_, err := ca.Identify(r)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want errors.Is %v", err, tc.want)
			}
		})
	}
}

func TestCloudflareAccess_BadSignatureRejected(t *testing.T) {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	const kid = "k1"
	jwksSrv := newJWKSServer(t, kid, &k.PublicKey)
	defer jwksSrv.Close()
	hc := &http.Client{Transport: rewriteHost{target: jwksSrv.URL}}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ca := auth.NewCloudflareAccess(auth.CloudflareConfig{
		TeamDomain: "test", AppAUD: "aud-123",
		HTTPClient: hc, Clock: fakeClock{t: now},
	})

	// Sign with the wrong key but advertise the registered kid.
	tok := signJWT(t, other, kid, map[string]any{
		"iss": "https://test.cloudflareaccess.com",
		"aud": "aud-123", "sub": "u",
		"exp": now.Add(time.Hour).Unix(),
	})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(auth.HeaderCloudflareAccess, tok)
	_, err := ca.Identify(r)
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("got %v, want ErrInvalidToken", err)
	}
}

// --- helpers ---

type fakeClock struct{ t time.Time }

func (f fakeClock) Now() time.Time { return f.t }

var _ clock.Clock = fakeClock{}

// rewriteHost rewrites every outbound request to point at target while
// preserving the original path. Lets the test redirect Cloudflare's
// hard-coded JWKS URL to an httptest server.
type rewriteHost struct{ target string }

func (rw rewriteHost) RoundTrip(r *http.Request) (*http.Response, error) {
	u := *r.URL
	parsed, err := r.URL.Parse(rw.target + u.Path)
	if err != nil {
		return nil, err
	}
	r2 := r.Clone(r.Context())
	r2.URL = parsed
	r2.Host = parsed.Host
	return http.DefaultTransport.RoundTrip(r2)
}

func newJWKSServer(t *testing.T, kid string, pub *rsa.PublicKey) *httptest.Server {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	eBytes := bigEndianExp(pub.E)
	e := base64.RawURLEncoding.EncodeToString(eBytes)
	body := map[string]any{
		"keys": []map[string]any{{
			"kid": kid, "kty": "RSA", "alg": "RS256", "use": "sig",
			"n": n, "e": e,
		}},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/cdn-cgi/access/certs") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func bigEndianExp(e int) []byte {
	// Trim leading zero bytes — RFC 7518 wants minimal big-endian.
	buf := []byte{byte(e >> 24), byte(e >> 16), byte(e >> 8), byte(e)}
	for len(buf) > 1 && buf[0] == 0 {
		buf = buf[1:]
	}
	return buf
}

func signJWT(t *testing.T, k *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}
