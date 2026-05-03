// Package ratelimit provides a token-bucket Limiter interface so production
// code can rate-limit calls (outbound HTTP, expensive handlers, queue
// consumers) and tests can assert on rate-limited behavior deterministically.
//
// Bucket is the standard production impl: a token bucket with refill rate
// and burst, using clock.Clock for "now" so its behavior is deterministic
// when wired with a clock.Fake. Manual is a fully-test-controlled limiter
// where the test sets the available token count via Set/Add.
//
// All Limiter methods are safe for concurrent use.
package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

// Limiter is the rate-limit interface.
type Limiter interface {
	// Allow reports whether one token is available right now and consumes
	// it on success. Non-blocking.
	Allow() bool
	// AllowN is Allow but for n tokens.
	AllowN(n int) bool
	// Wait blocks until one token is available or ctx is canceled. Returns
	// nil on success, ctx.Err() on cancellation.
	Wait(ctx context.Context) error
	// WaitN is Wait but for n tokens. Returns an error immediately if n
	// exceeds the bucket's burst capacity.
	WaitN(ctx context.Context, n int) error
}

// ErrBurstExceeded is returned by WaitN when n is larger than the bucket's
// configured burst capacity.
var ErrBurstExceeded = errors.New("ratelimit: requested tokens exceed burst")

// Bucket is a token-bucket Limiter. The bucket fills at Rate tokens per
// second, capped at Burst. The zero value is unusable; construct via New.
type Bucket struct {
	rate  float64 // tokens per second
	burst int

	clk clock.Clock

	mu       sync.Mutex
	tokens   float64
	lastFill time.Time
}

// New constructs a Bucket. Pass clock.System{} in production and
// clock.NewFake(t) in tests. ratePerSec must be > 0 and burst >= 1, otherwise
// New panics — these are programmer errors, not runtime failures.
func New(clk clock.Clock, ratePerSec float64, burst int) *Bucket {
	if clk == nil {
		clk = clock.Default
	}
	if ratePerSec <= 0 {
		panic("ratelimit.New: ratePerSec must be > 0")
	}
	if burst < 1 {
		panic("ratelimit.New: burst must be >= 1")
	}
	return &Bucket{
		rate:     ratePerSec,
		burst:    burst,
		clk:      clk,
		tokens:   float64(burst),
		lastFill: clk.Now(),
	}
}

// Allow reports whether one token is available now.
func (b *Bucket) Allow() bool { return b.AllowN(1) }

// AllowN reports whether n tokens are available now.
func (b *Bucket) AllowN(n int) bool {
	if n <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked()
	if b.tokens >= float64(n) {
		b.tokens -= float64(n)
		return true
	}
	return false
}

// Wait blocks until a token is available.
func (b *Bucket) Wait(ctx context.Context) error { return b.WaitN(ctx, 1) }

// WaitN blocks until n tokens are available or ctx is canceled.
func (b *Bucket) WaitN(ctx context.Context, n int) error {
	if n <= 0 {
		return nil
	}
	if n > b.burst {
		return ErrBurstExceeded
	}
	for {
		b.mu.Lock()
		b.refillLocked()
		if b.tokens >= float64(n) {
			b.tokens -= float64(n)
			b.mu.Unlock()
			return nil
		}
		need := float64(n) - b.tokens
		wait := time.Duration(need / b.rate * float64(time.Second))
		b.mu.Unlock()

		if wait <= 0 {
			wait = time.Millisecond
		}
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
}

// Tokens returns the currently-available token count (refilled to "now").
// Useful in tests; not used on the hot path.
func (b *Bucket) Tokens() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked()
	return b.tokens
}

func (b *Bucket) refillLocked() {
	now := b.clk.Now()
	elapsed := now.Sub(b.lastFill).Seconds()
	if elapsed <= 0 {
		return
	}
	b.tokens += elapsed * b.rate
	if b.tokens > float64(b.burst) {
		b.tokens = float64(b.burst)
	}
	b.lastFill = now
}

// Manual is a Limiter whose available token count is set explicitly by the
// test. Useful when you want to assert "this code path triggers rate
// limiting" without doing time-based arithmetic.
//
// Wait blocks until a token becomes available via Set/Add, or ctx cancels.
type Manual struct {
	mu     sync.Mutex
	tokens int
	cond   *sync.Cond
}

// NewManual returns a Manual with initial tokens available.
func NewManual(initial int) *Manual {
	m := &Manual{tokens: initial}
	m.cond = sync.NewCond(&m.mu)
	return m
}

// Set replaces the token count.
func (m *Manual) Set(n int) {
	m.mu.Lock()
	m.tokens = n
	m.mu.Unlock()
	m.cond.Broadcast()
}

// Add adjusts the token count by delta (may be negative).
func (m *Manual) Add(delta int) {
	m.mu.Lock()
	m.tokens += delta
	m.mu.Unlock()
	m.cond.Broadcast()
}

// Tokens returns the current count.
func (m *Manual) Tokens() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokens
}

// Allow consumes one token if available.
func (m *Manual) Allow() bool { return m.AllowN(1) }

// AllowN consumes n tokens if available.
func (m *Manual) AllowN(n int) bool {
	if n <= 0 {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tokens >= n {
		m.tokens -= n
		return true
	}
	return false
}

// Wait blocks until one token is available.
func (m *Manual) Wait(ctx context.Context) error { return m.WaitN(ctx, 1) }

// WaitN blocks until n tokens are available. Cancellation arrives via a
// goroutine that broadcasts on the condvar so blocked callers wake.
func (m *Manual) WaitN(ctx context.Context, n int) error {
	if n <= 0 {
		return nil
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			m.cond.Broadcast()
		case <-done:
		}
	}()

	m.mu.Lock()
	defer m.mu.Unlock()
	for m.tokens < n {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.cond.Wait()
	}
	m.tokens -= n
	return nil
}
