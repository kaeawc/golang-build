package errgroup

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/limiter"
)

func TestSuccessReturnsNil(t *testing.T) {
	g, _ := WithContext(context.Background())
	for i := 0; i < 5; i++ {
		g.Go(func() error { return nil })
	}
	if err := g.Wait(); err != nil {
		t.Errorf("Wait: %v", err)
	}
}

func TestFirstErrorCaptured(t *testing.T) {
	g, _ := WithContext(context.Background())
	want := errors.New("boom")
	g.Go(func() error { return nil })
	g.Go(func() error { return want })
	g.Go(func() error { return errors.New("second") })
	err := g.Wait()
	// Order isn't guaranteed; just verify we got *one* of the errors.
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestContextCancelOnFirstError(t *testing.T) {
	g, ctx := WithContext(context.Background())
	want := errors.New("boom")

	started := make(chan struct{})
	g.Go(func() error {
		close(started)
		return want
	})

	<-started
	// Wait briefly for cancellation to propagate.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	if ctx.Err() == nil {
		t.Fatal("derived context should be canceled after first error")
	}
	if cause := context.Cause(ctx); !errors.Is(cause, want) {
		t.Errorf("Cause = %v, want %v", cause, want)
	}

	if err := g.Wait(); !errors.Is(err, want) {
		t.Errorf("Wait err = %v, want %v", err, want)
	}
}

func TestSetLimitBoundsConcurrency(t *testing.T) {
	g, _ := WithContext(context.Background())
	g.SetLimit(3)

	var inFlight, peak atomic.Int32
	hold := make(chan struct{})
	// Issue Go calls from a separate goroutine: Go blocks when the
	// limit is full so the main goroutine must remain free to release
	// `hold`.
	dispatched := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			g.Go(func() error {
				cur := inFlight.Add(1)
				for {
					p := peak.Load()
					if cur <= p || peak.CompareAndSwap(p, cur) {
						break
					}
				}
				<-hold
				inFlight.Add(-1)
				return nil
			})
		}
		close(dispatched)
	}()

	// Let some goroutines reach the hold so peak settles.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && peak.Load() < 3 {
		time.Sleep(time.Millisecond)
	}
	close(hold)
	<-dispatched
	if err := g.Wait(); err != nil {
		t.Errorf("Wait: %v", err)
	}
	if peak.Load() > 3 {
		t.Errorf("peak = %d, exceeded limit 3", peak.Load())
	}
}

func TestSetLimiterSharesAcrossGroups(t *testing.T) {
	sem := limiter.NewSemaphore(2)
	g1, _ := WithContext(context.Background())
	g1.SetLimiter(sem)
	g2, _ := WithContext(context.Background())
	g2.SetLimiter(sem)

	var peak atomic.Int32
	hold := make(chan struct{})
	count := func() {
		c := int32(sem.InFlight())
		for {
			p := peak.Load()
			if c <= p || peak.CompareAndSwap(p, c) {
				break
			}
		}
	}

	dispatched := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			g1.Go(func() error { count(); <-hold; return nil })
			g2.Go(func() error { count(); <-hold; return nil })
		}
		close(dispatched)
	}()

	// Wait until the semaphore is at capacity, then release.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && sem.InFlight() < 2 {
		time.Sleep(time.Millisecond)
	}
	close(hold)
	<-dispatched
	_ = g1.Wait()
	_ = g2.Wait()

	if peak.Load() > 2 {
		t.Errorf("peak across both groups = %d, exceeded shared limit 2", peak.Load())
	}
}

func TestTryGoFailsWhenLimitExhausted(t *testing.T) {
	g, _ := WithContext(context.Background())
	g.SetLimit(2)

	hold := make(chan struct{})
	g.Go(func() error { <-hold; return nil })
	g.Go(func() error { <-hold; return nil })

	// Wait for both Go calls to acquire; otherwise TryGo may race in.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		// Best signal we have: TryGo should return false now.
		ok := g.TryGo(func() error { return nil })
		if !ok {
			break
		}
		// Spawned a third — wait for it then try again.
	}

	if g.TryGo(func() error { return nil }) {
		t.Error("TryGo should fail when limit is exhausted")
	}
	close(hold)
	_ = g.Wait()
}

func TestTryGoSucceedsWithoutLimit(t *testing.T) {
	g, _ := WithContext(context.Background())
	if !g.TryGo(func() error { return nil }) {
		t.Error("TryGo without limit should always succeed")
	}
	_ = g.Wait()
}

func TestSetLimitPanicsWhileRunning(t *testing.T) {
	g, _ := WithContext(context.Background())
	g.SetLimit(1)
	hold := make(chan struct{})
	g.Go(func() error { <-hold; return nil })
	// Give the goroutine a moment to acquire the semaphore.
	time.Sleep(20 * time.Millisecond)

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected SetLimit to panic while goroutines are running")
		}
		close(hold)
		_ = g.Wait()
	}()
	g.SetLimit(5)
}

func TestWaitIdempotent(t *testing.T) {
	g, _ := WithContext(context.Background())
	want := errors.New("boom")
	g.Go(func() error { return want })

	err1 := g.Wait()
	err2 := g.Wait()
	if !errors.Is(err1, want) || !errors.Is(err2, want) {
		t.Errorf("Wait1=%v Wait2=%v, want both %v", err1, err2, want)
	}
}

func TestGoAfterCanceledCtxRecordsError(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	g, _ := WithContext(parent)
	g.SetLimit(1)

	hold := make(chan struct{})
	g.Go(func() error { <-hold; return nil })

	// Cancel parent — group ctx should be canceled too.
	cancel()
	close(hold)

	// Now Go acquires, but since ctx is canceled, acquire returns
	// ctx.Err and the function should not run.
	var ran atomic.Bool
	g.Go(func() error {
		ran.Store(true)
		return nil
	})
	err := g.Wait()
	if err == nil {
		t.Error("Wait should report cancellation error from Acquire")
	}
	if ran.Load() {
		t.Error("fn should not run when acquire fails")
	}
}

func TestWaitReturnsNilWithoutAnyGo(t *testing.T) {
	g, _ := WithContext(context.Background())
	if err := g.Wait(); err != nil {
		t.Errorf("Wait on empty group = %v, want nil", err)
	}
}

func TestZeroValueGroupIsBroken(t *testing.T) {
	// Documenting that zero value is not usable — Wait returns nil but
	// any state checks are undefined. The package doc is the contract;
	// this test just verifies WithContext is required for proper use.
	var g Group
	// Should not panic, but cancel func is nil.
	if err := g.Wait(); err != nil {
		t.Errorf("Wait on zero-value Group = %v", err)
	}
}

func TestConcurrentGo(t *testing.T) {
	g, _ := WithContext(context.Background())
	g.SetLimit(8)
	var counter atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Go(func() error { counter.Add(1); return nil })
		}()
	}
	wg.Wait()
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
	if counter.Load() != 100 {
		t.Errorf("counter = %d, want 100", counter.Load())
	}
}
