// Package errgroup is a fan-out helper with bounded concurrency and
// fail-fast semantics. It mirrors golang.org/x/sync/errgroup's surface
// (`WithContext` + `Go` + `Wait` + `SetLimit`) but plugs into
// internal/limiter so a single semaphore can throttle multiple groups
// or coexist with other limiter-bound code.
//
// The 80% use case — process N items, max K in flight, abort on first
// error:
//
//	g, ctx := errgroup.WithContext(ctx)
//	g.SetLimit(8)
//	for _, item := range items {
//	    item := item
//	    g.Go(func() error { return process(ctx, item) })
//	}
//	if err := g.Wait(); err != nil { ... }
package errgroup

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/kaeawc/golang-build/internal/limiter"
)

// limHolder wraps an interface so atomic.Pointer can manage it. nil
// pointer means no limit.
type limHolder struct{ l limiter.Limiter }

// Group runs goroutines and collects the first non-nil error. The zero
// value is unusable; construct via WithContext.
type Group struct {
	cancel context.CancelCauseFunc
	ctx    context.Context

	lim atomic.Pointer[limHolder]

	wg sync.WaitGroup

	errOnce sync.Once
	err     error
}

// WithContext returns a Group and a derived context. The context is
// canceled (with the captured error as cause) the first time a Go-
// scheduled function returns a non-nil error. Wait returns that error,
// or nil if every function returned nil.
func WithContext(ctx context.Context) (*Group, context.Context) {
	derived, cancel := context.WithCancelCause(ctx)
	return &Group{cancel: cancel, ctx: derived}, derived
}

// SetLimit caps the number of goroutines that can run concurrently.
// Calls to Go that exceed the limit block until a slot frees. n <= 0
// disables the limit (the default; matches x/sync/errgroup).
//
// SetLimit must be called BEFORE any Go. Calling it while goroutines
// are running panics if a limiter is already installed and shows
// in-flight permits; the no-limiter path can't detect in-flight work
// reliably and silently swaps state — don't rely on it.
func (g *Group) SetLimit(n int) {
	g.assertIdle("SetLimit")
	if n <= 0 {
		g.lim.Store(nil)
		return
	}
	g.lim.Store(&limHolder{l: limiter.NewSemaphore(n)})
}

// SetLimiter installs an external limiter.Limiter so multiple groups (or
// other code paths) can share a single semaphore. Same constraint as
// SetLimit: call before any Go.
func (g *Group) SetLimiter(lim limiter.Limiter) {
	g.assertIdle("SetLimiter")
	if lim == nil {
		g.lim.Store(nil)
		return
	}
	g.lim.Store(&limHolder{l: lim})
}

// assertIdle panics if the currently-installed limiter shows in-flight
// permits. With no limiter installed this is a no-op (we can't read the
// wg counter to detect in-flight work).
func (g *Group) assertIdle(method string) {
	h := g.lim.Load()
	if h != nil && h.l.InFlight() > 0 {
		panic("errgroup: " + method + " called while goroutines are running")
	}
}

// currentLimiter returns the active limiter, or nil if none is set.
func (g *Group) currentLimiter() limiter.Limiter {
	h := g.lim.Load()
	if h == nil {
		return nil
	}
	return h.l
}

// Go runs fn in a new goroutine. If a limit is set, Go blocks until a
// slot is available. The caller's goroutine waits in Acquire — this is
// the standard backpressure mechanism. If the group's context is already
// canceled, Go records the cancellation cause and returns without
// spawning.
func (g *Group) Go(fn func() error) {
	if g.acquire() != nil {
		return
	}
	g.wg.Add(1)
	go g.run(fn)
}

// TryGo schedules fn only if a slot is immediately available. Returns
// false if a limit is set and exhausted, leaving fn unscheduled. With no
// limit set, TryGo is equivalent to Go and always returns true.
func (g *Group) TryGo(fn func() error) bool {
	if lim := g.currentLimiter(); lim != nil && !lim.TryAcquire() {
		return false
	}
	g.wg.Add(1)
	go g.run(fn)
	return true
}

// acquire takes a permit if a limiter is set. Returns the context
// cancellation error if ctx was canceled mid-acquire (in which case the
// caller should not spawn).
func (g *Group) acquire() error {
	lim := g.currentLimiter()
	if lim == nil {
		return nil
	}
	if err := lim.Acquire(g.ctx); err != nil {
		g.recordErr(fmt.Errorf("errgroup: acquire: %w", err))
		return err
	}
	return nil
}

func (g *Group) release() {
	if lim := g.currentLimiter(); lim != nil {
		lim.Release()
	}
}

func (g *Group) run(fn func() error) {
	defer g.wg.Done()
	defer g.release()
	if err := fn(); err != nil {
		g.recordErr(err)
	}
}

func (g *Group) recordErr(err error) {
	g.errOnce.Do(func() {
		g.err = err
		if g.cancel != nil {
			g.cancel(err)
		}
	})
}

// Wait blocks until every Go-scheduled goroutine finishes, then returns
// the first non-nil error or nil. Wait may be called multiple times; the
// stored error and cancellation are stable after the first call.
func (g *Group) Wait() error {
	g.wg.Wait()
	if g.cancel != nil {
		// Cancel even on success so the caller's derived context is
		// guaranteed to drain its goroutines (matches x/sync/errgroup).
		g.cancel(g.err)
	}
	return g.err
}
