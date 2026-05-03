package tokens

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

type myClaims struct {
	StandardClaims
	UserID string `json:"uid,omitempty"`
	Role   string `json:"role,omitempty"`
}

func TestHMACSignAndVerify(t *testing.T) {
	clk := clock.NewFake(time.Unix(1700000000, 0))
	is := NewHMAC256([]byte("secret"), WithClock(clk))

	c := myClaims{
		StandardClaims: StandardClaims{
			Subject:   "u_42",
			IssuedAt:  clk.Now().Unix(),
			ExpiresAt: clk.Now().Add(time.Hour).Unix(),
		},
		UserID: "u_42",
		Role:   "admin",
	}
	tok, err := is.Sign(c)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if strings.Count(tok, ".") != 2 {
		t.Errorf("token does not have 3 dot-separated parts: %s", tok)
	}

	var got myClaims
	if err := is.Verify(tok, &got); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.UserID != "u_42" || got.Role != "admin" || got.Subject != "u_42" {
		t.Errorf("decoded claims = %+v", got)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	clk := clock.NewFake(time.Unix(1700000000, 0))
	a := NewHMAC256([]byte("secret-a"), WithClock(clk))
	b := NewHMAC256([]byte("secret-b"), WithClock(clk))

	tok, _ := a.Sign(myClaims{UserID: "u_1"})
	var got myClaims
	err := b.Verify(tok, &got)
	if !errors.Is(err, ErrSignature) {
		t.Errorf("err = %v, want ErrSignature", err)
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	is := NewHMAC256([]byte("k"))
	cases := []string{
		"",
		"only-one-part",
		"two.parts",
		"four.parts.are.too.many",
	}
	for _, tok := range cases {
		var got myClaims
		err := is.Verify(tok, &got)
		if !errors.Is(err, ErrMalformed) {
			t.Errorf("Verify(%q): err = %v, want ErrMalformed", tok, err)
		}
	}
}

func TestVerifyRejectsAlgNone(t *testing.T) {
	is := NewHMAC256([]byte("k"))
	// Hand-craft a token with alg=none.
	noneToken := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJzdWIiOiJ1XzEifQ."
	var got myClaims
	err := is.Verify(noneToken, &got)
	if !errors.Is(err, ErrAlgorithm) {
		t.Errorf("err = %v, want ErrAlgorithm", err)
	}
}

func TestVerifyExpired(t *testing.T) {
	clk := clock.NewFake(time.Unix(1700000000, 0))
	is := NewHMAC256([]byte("k"), WithClock(clk))
	tok, _ := is.Sign(myClaims{StandardClaims: StandardClaims{
		ExpiresAt: clk.Now().Add(time.Minute).Unix(),
	}})

	// Just-before expiry: passes.
	clk.Advance(59 * time.Second)
	if err := is.Verify(tok, &myClaims{}); err != nil {
		t.Errorf("pre-expiry Verify: %v", err)
	}

	// Past expiry: ErrExpired.
	clk.Advance(2 * time.Second)
	err := is.Verify(tok, &myClaims{})
	if !errors.Is(err, ErrExpired) {
		t.Errorf("err = %v, want ErrExpired", err)
	}
}

func TestVerifyNotYetValid(t *testing.T) {
	clk := clock.NewFake(time.Unix(1700000000, 0))
	is := NewHMAC256([]byte("k"), WithClock(clk))
	tok, _ := is.Sign(myClaims{StandardClaims: StandardClaims{
		NotBefore: clk.Now().Add(time.Minute).Unix(),
	}})

	err := is.Verify(tok, &myClaims{})
	if !errors.Is(err, ErrNotYetValid) {
		t.Errorf("err = %v, want ErrNotYetValid", err)
	}

	clk.Advance(2 * time.Minute)
	if err := is.Verify(tok, &myClaims{}); err != nil {
		t.Errorf("after nbf elapsed: %v", err)
	}
}

func TestVerifyLeewayAcceptsSlightSkew(t *testing.T) {
	clk := clock.NewFake(time.Unix(1700000000, 0))
	is := NewHMAC256([]byte("k"), WithClock(clk), WithLeeway(30*time.Second))
	tok, _ := is.Sign(myClaims{StandardClaims: StandardClaims{
		ExpiresAt: clk.Now().Add(time.Minute).Unix(),
	}})

	// 75s after issue, 15s past expiry — within leeway.
	clk.Advance(75 * time.Second)
	if err := is.Verify(tok, &myClaims{}); err != nil {
		t.Errorf("within leeway: %v", err)
	}

	// 95s, 35s past — outside leeway.
	clk.Advance(20 * time.Second)
	err := is.Verify(tok, &myClaims{})
	if !errors.Is(err, ErrExpired) {
		t.Errorf("outside leeway: err = %v, want ErrExpired", err)
	}
}

func TestVerifyNilDstSkipsDecode(t *testing.T) {
	is := NewHMAC256([]byte("k"))
	tok, _ := is.Sign(myClaims{UserID: "u_1"})
	if err := is.Verify(tok, nil); err != nil {
		t.Errorf("Verify with nil dst should still verify signature: %v", err)
	}
}

func TestNewHMAC256PanicsOnEmptySecret(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for empty secret")
		}
	}()
	NewHMAC256(nil)
}

func TestSignMarshalError(t *testing.T) {
	is := NewHMAC256([]byte("k"))
	// channels are unmarshalable
	_, err := is.Sign(map[string]chan int{"x": make(chan int)})
	if err == nil {
		t.Error("expected error for unmarshalable claims")
	}
}

func TestFakeAlwaysValid(t *testing.T) {
	c := myClaims{UserID: "u_1", Role: "admin"}
	f := AlwaysValid(c)

	tok1, err := f.Sign(myClaims{})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	tok2, _ := f.Sign(myClaims{})
	if tok1 == tok2 {
		t.Error("Fake should emit unique tokens per Sign call")
	}

	var got myClaims
	if err := f.Verify("anything", &got); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.UserID != "u_1" || got.Role != "admin" {
		t.Errorf("got = %+v, want %+v", got, c)
	}
}

func TestFakeAlwaysInvalid(t *testing.T) {
	want := errors.New("nope")
	f := AlwaysInvalid(want)

	if _, err := f.Sign(myClaims{}); !errors.Is(err, want) {
		t.Errorf("Sign err = %v, want %v", err, want)
	}
	var got myClaims
	if err := f.Verify("x", &got); !errors.Is(err, want) {
		t.Errorf("Verify err = %v, want %v", err, want)
	}
}

func TestFakeSetError(t *testing.T) {
	f := AlwaysValid(myClaims{UserID: "u_1"})

	// Initially valid.
	if err := f.Verify("x", &myClaims{}); err != nil {
		t.Fatalf("initial Verify: %v", err)
	}

	want := errors.New("revoked")
	f.SetError(want)
	if err := f.Verify("x", &myClaims{}); !errors.Is(err, want) {
		t.Errorf("after SetError: err = %v, want %v", err, want)
	}
}

func TestFakeCounters(t *testing.T) {
	f := AlwaysValid(myClaims{})
	for i := 0; i < 3; i++ {
		_, _ = f.Sign(myClaims{})
	}
	for i := 0; i < 5; i++ {
		_ = f.Verify("x", &myClaims{})
	}
	if f.SignCount() != 3 {
		t.Errorf("SignCount = %d, want 3", f.SignCount())
	}
	if f.VerifyCount() != 5 {
		t.Errorf("VerifyCount = %d, want 5", f.VerifyCount())
	}
}

func TestFakeZeroValueRejects(t *testing.T) {
	var f Fake
	var got myClaims
	if err := f.Verify("x", &got); !errors.Is(err, ErrSignature) {
		t.Errorf("zero-value Fake.Verify err = %v, want ErrSignature", err)
	}
}

func TestFakeSetClaims(t *testing.T) {
	f := AlwaysValid(myClaims{UserID: "u_1"})
	f.SetClaims(myClaims{UserID: "u_2"})

	var got myClaims
	_ = f.Verify("x", &got)
	if got.UserID != "u_2" {
		t.Errorf("UserID = %q, want u_2", got.UserID)
	}
}

func TestRoundTripWithSpecialChars(t *testing.T) {
	is := NewHMAC256([]byte("secret"))
	c := myClaims{
		UserID: "user with spaces & émojis 🎉",
		StandardClaims: StandardClaims{
			Subject: "sub/with/slashes+plus=eq",
		},
	}
	tok, err := is.Sign(c)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// URL-safe base64 should not contain +, /, =
	for _, ch := range tok {
		switch ch {
		case '+', '/', '=':
			t.Errorf("token contains URL-unsafe char %q", ch)
		}
	}
	var got myClaims
	if err := is.Verify(tok, &got); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != c {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, c)
	}
}
