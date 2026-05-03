package tokens

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

// AlwaysValid returns a Fake whose Sign emits a sequence of unique
// "fake-token-N" strings and whose Verify populates dst with the
// supplied claims (round-tripped through JSON so dst types convert
// correctly). Useful for handler tests that just need a working
// IssuerVerifier without configuring an HMAC secret.
func AlwaysValid(claims any) *Fake {
	return &Fake{claims: claims}
}

// AlwaysInvalid returns a Fake whose Sign and Verify return err. Useful
// for testing handler error paths.
func AlwaysInvalid(err error) *Fake {
	return &Fake{err: err}
}

// Fake is a scripted IssuerVerifier for tests. The zero value returns
// ErrSignature on every Verify and a sequenced "fake-token-N" on Sign.
//
// Concurrent use is safe: SetError / SetClaims serialize with Sign /
// Verify via mu, and the call counters are atomic.
type Fake struct {
	mu     sync.Mutex
	claims any
	err    error

	signed   atomic.Int64
	verified atomic.Int64
}

// Sign returns a sequenced fake token, or the configured err.
func (f *Fake) Sign(_ any) (string, error) {
	n := f.signed.Add(1)
	f.mu.Lock()
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("fake-token-%d", n), nil
}

// Verify populates dst with the configured claims (when AlwaysValid was
// used), or returns the configured err. With the zero-value Fake it
// returns ErrSignature so misconfigured tests fail loudly.
func (f *Fake) Verify(_ string, dst any) error {
	f.verified.Add(1)
	f.mu.Lock()
	err := f.err
	claims := f.claims
	f.mu.Unlock()

	if err != nil {
		return err
	}
	if claims == nil {
		return ErrSignature
	}
	if dst == nil {
		return nil
	}
	// Round-trip through JSON so caller's dst type doesn't have to
	// match f.claims type exactly — they only need overlapping fields.
	raw, err := json.Marshal(claims)
	if err != nil {
		return fmt.Errorf("tokens.Fake: marshal stored claims: %w", err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("tokens.Fake: unmarshal into dst: %w", err)
	}
	return nil
}

// SignCount reports how many times Sign was called.
func (f *Fake) SignCount() int64 { return f.signed.Load() }

// VerifyCount reports how many times Verify was called.
func (f *Fake) VerifyCount() int64 { return f.verified.Load() }

// SetError replaces the configured error after construction. Useful for
// tests that flip a fake from valid to invalid mid-flow.
func (f *Fake) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// SetClaims replaces the stored claims after construction.
func (f *Fake) SetClaims(claims any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = claims
}

// compile-time assertions: both impls satisfy IssuerVerifier.
var (
	_ IssuerVerifier = (*HMAC256)(nil)
	_ IssuerVerifier = (*Fake)(nil)
)
