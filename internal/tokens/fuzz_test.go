package tokens

import (
	"errors"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kaeawc/golang-build/internal/clock"
)

var fuzzSecret = []byte("fuzz-secret-0123456789abcdef0123")

type fuzzClaims struct {
	StandardClaims
	Role string `json:"role,omitempty"`
}

// FuzzVerify feeds arbitrary strings to Verify. It must never panic, and
// it must never accept a token the issuer did not sign: without the secret
// the only way in is a byte-identical copy of a signed token.
func FuzzVerify(f *testing.F) {
	h := NewHMAC256(fuzzSecret, WithClock(clock.NewFake(time.Unix(1700000000, 0))))
	signed, err := h.Sign(fuzzClaims{Subject: "u_1", Role: "admin"})
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range []string{
		signed,
		"",
		"a.b.c",
		"..",
		"eyJhbGciOiJub25lIn0.e30.", // alg=none
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30.AAAA", // right alg, bad sig
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, token string) {
		var got fuzzClaims
		if err := h.Verify(token, &got); err != nil {
			return
		}
		if token != signed {
			t.Fatalf("accepted a token that was never issued: %q", token)
		}
	})
}

// FuzzSignVerifyRoundTrip checks the issuer's two halves agree: anything
// Sign produces, Verify accepts and decodes back to the same claims, and
// changing any single character of the token makes Verify reject it.
func FuzzSignVerifyRoundTrip(f *testing.F) {
	f.Add("u_1", "admin", int64(0), 0, byte('A'))
	f.Add("", "", int64(1700003600), 10, byte('.'))
	f.Add("user@example.com", "viewer", int64(1600000000), 80, byte('_'))
	f.Fuzz(func(t *testing.T, sub, role string, exp int64, pos int, b byte) {
		// encoding/json replaces invalid UTF-8 with U+FFFD, so such
		// strings cannot round-trip through any JSON token.
		if !utf8.ValidString(sub) || !utf8.ValidString(role) {
			return
		}
		now := time.Unix(1700000000, 0)
		h := NewHMAC256(fuzzSecret, WithClock(clock.NewFake(now)))
		in := fuzzClaims{Subject: sub, ExpiresAt: exp, Role: role}
		tok, err := h.Sign(in)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}

		var out fuzzClaims
		err = h.Verify(tok, &out)
		expired := exp != 0 && now.After(time.Unix(exp, 0))
		switch {
		case expired && !errors.Is(err, ErrExpired):
			t.Fatalf("exp=%d now=%d: Verify = %v, want ErrExpired", exp, now.Unix(), err)
		case expired:
			return
		case err != nil:
			t.Fatalf("Verify(Sign(%+v)) = %v", in, err)
		case out != in:
			t.Fatalf("round trip: got %+v, want %+v", out, in)
		}

		if pos < 0 {
			pos = -(pos + 1)
		}
		pos %= len(tok)
		if tok[pos] == b {
			return
		}
		tampered := tok[:pos] + string(b) + tok[pos+1:]
		if err := h.Verify(tampered, &out); err == nil {
			t.Fatalf("accepted tampered token (byte %d %q -> %q): %q", pos, tok[pos], b, tampered)
		}
	})
}
