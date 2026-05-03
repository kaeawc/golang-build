// Package limiter provides a bounded-concurrency primitive (Semaphore).
//
// Use it to cap the number of in-flight workers, outbound requests, or
// expensive computations. Acquire blocks until a permit is available or ctx
// cancels; the returned Release function returns the permit. The interface
// makes it easy to substitute fakes that record contention in tests.
//
// Typical use:
//
//	sem := limiter.NewSemaphore(8)
//	for _, item := range items {
//	    item := item
//	    if err := sem.Acquire(ctx); err != nil { return err }
//	    go func() {
//	        defer sem.Release()
//	        process(item)
//	    }()
//	}
package limiter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// Limiter is the bounded-concurrency interface.
type Limiter interface {
	// Acquire takes one permit, blocking until available or ctx cancels.
	// Returns ctx.Err() on cancellation, never wraps it.
	Acquire(ctx context.Context) error
	// AcquireN takes n permits atomically (all-or-nothing).
	AcquireN(ctx context.Context, n int) error
	// TryAcquire attempts a non-blocking acquire of one permit.
	TryAcquire() bool
	// TryAcquireN attempts a non-blocking acquire of n permits.
	TryAcquireN(n int) bool
	// Release returns one permit. Releasing more than acquired panics.
	Release()
	// ReleaseN returns n permits.
	ReleaseN(n int)
	// InFlight reports the current number of acquired permits.
	InFlight() int
	// Capacity reports the maximum permit count.
	Capacity() int
}

// ErrCapacityExceeded is returned by AcquireN when n exceeds capacity.
var ErrCapacityExceeded = errors.New("limiter: requested permits exceed capacity")

// Semaphore is a counting semaphore Limiter. Construct via NewSemaphore.
type Semaphore struct {
	capacity int

	mu       sync.Mutex
	cond     *sync.Cond
	inFlight int

	// stats
	totalAcquired atomic.Int64
	totalWaited   atomic.Int64 // count of Acquires that had to wait
}

// NewSemaphore returns a Semaphore with the given capacity. Capacity < 1
// panics — that's a programmer error.
func NewSemaphore(capacity int) *Semaphore {
	if capacity < 1 {
		panic("limiter.NewSemaphore: capacity must be >= 1")
	}
	s := &Semaphore{capacity: capacity}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Acquire takes one permit.
func (s *Semaphore) Acquire(ctx context.Context) error { return s.AcquireN(ctx, 1) }

// AcquireN takes n permits atomically.
func (s *Semaphore) AcquireN(ctx context.Context, n int) error {
	if n <= 0 {
		return nil
	}
	if n > s.capacity {
		return ErrCapacityExceeded
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Wake blocked waiters on cancellation.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			s.cond.Broadcast()
		case <-done:
		}
	}()

	s.mu.Lock()
	defer s.mu.Unlock()
	waited := false
	for s.inFlight+n > s.capacity {
		if err := ctx.Err(); err != nil {
			return err
		}
		waited = true
		s.cond.Wait()
	}
	s.inFlight += n
	s.totalAcquired.Add(int64(n))
	if waited {
		s.totalWaited.Add(1)
	}
	return nil
}

// TryAcquire attempts a non-blocking acquire of one permit.
func (s *Semaphore) TryAcquire() bool { return s.TryAcquireN(1) }

// TryAcquireN attempts a non-blocking acquire of n permits.
func (s *Semaphore) TryAcquireN(n int) bool {
	if n <= 0 {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight+n > s.capacity {
		return false
	}
	s.inFlight += n
	s.totalAcquired.Add(int64(n))
	return true
}

// Release returns one permit.
func (s *Semaphore) Release() { s.ReleaseN(1) }

// ReleaseN returns n permits. Releasing more than acquired panics.
func (s *Semaphore) ReleaseN(n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	if s.inFlight < n {
		s.mu.Unlock()
		panic("limiter: ReleaseN: released more permits than acquired")
	}
	s.inFlight -= n
	s.mu.Unlock()
	s.cond.Broadcast()
}

// InFlight returns the current acquired-permit count.
func (s *Semaphore) InFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlight
}

// Capacity returns the configured capacity.
func (s *Semaphore) Capacity() int { return s.capacity }

// Stats is a snapshot of cumulative semaphore activity.
type Stats struct {
	TotalAcquired int64 `json:"totalAcquired"`
	TotalWaited   int64 `json:"totalWaited"`
}

// Stats returns cumulative counters since construction.
func (s *Semaphore) Stats() Stats {
	return Stats{
		TotalAcquired: s.totalAcquired.Load(),
		TotalWaited:   s.totalWaited.Load(),
	}
}

// Run is a convenience wrapper: it acquires one permit, runs fn, and
// releases. Returns ctx.Err() if Acquire fails, otherwise the result of fn.
func (s *Semaphore) Run(ctx context.Context, fn func() error) error {
	if err := s.Acquire(ctx); err != nil {
		return err
	}
	defer s.Release()
	return fn()
}
