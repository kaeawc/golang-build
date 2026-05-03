package limiter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcquireReleaseBasic(t *testing.T) {
	s := NewSemaphore(2)
	if err := s.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := s.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire #2: %v", err)
	}
	if s.InFlight() != 2 {
		t.Errorf("InFlight = %d, want 2", s.InFlight())
	}
	if s.TryAcquire() {
		t.Error("TryAcquire on full semaphore should return false")
	}
	s.Release()
	if !s.TryAcquire() {
		t.Error("TryAcquire after Release should succeed")
	}
}

func TestAcquireBlocksUntilRelease(t *testing.T) {
	s := NewSemaphore(1)
	_ = s.Acquire(context.Background())

	done := make(chan struct{})
	go func() {
		_ = s.Acquire(context.Background())
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("second Acquire should block while semaphore is full")
	case <-time.After(20 * time.Millisecond):
	}

	s.Release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked Acquire did not unblock after Release")
	}
}

func TestAcquireContextCancel(t *testing.T) {
	s := NewSemaphore(1)
	_ = s.Acquire(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Acquire(ctx) }()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Acquire did not return after cancel")
	}
}

func TestAcquireNExceedsCapacity(t *testing.T) {
	s := NewSemaphore(3)
	err := s.AcquireN(context.Background(), 4)
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Errorf("err = %v, want ErrCapacityExceeded", err)
	}
}

func TestAcquireNAtomic(t *testing.T) {
	s := NewSemaphore(3)
	if !s.TryAcquireN(2) {
		t.Fatal("TryAcquireN(2) on capacity 3: want true")
	}
	if s.TryAcquireN(2) {
		t.Error("TryAcquireN(2) with only 1 free: want false")
	}
	if !s.TryAcquireN(1) {
		t.Error("TryAcquireN(1) with 1 free: want true")
	}
}

func TestReleaseTooMuchPanics(t *testing.T) {
	s := NewSemaphore(1)
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic releasing more than acquired")
		}
	}()
	s.Release()
}

func TestNewSemaphoreInvalidPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for capacity < 1")
		}
	}()
	NewSemaphore(0)
}

func TestRunConvenience(t *testing.T) {
	s := NewSemaphore(2)
	called := atomic.Bool{}
	err := s.Run(context.Background(), func() error {
		called.Store(true)
		if s.InFlight() != 1 {
			t.Errorf("InFlight inside Run = %d, want 1", s.InFlight())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called.Load() {
		t.Error("fn was not called")
	}
	if s.InFlight() != 0 {
		t.Errorf("InFlight after Run = %d, want 0", s.InFlight())
	}
}

func TestRunPropagatesError(t *testing.T) {
	s := NewSemaphore(1)
	want := errors.New("boom")
	err := s.Run(context.Background(), func() error { return want })
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
	if s.InFlight() != 0 {
		t.Errorf("InFlight after failing Run = %d, want 0 (must release)", s.InFlight())
	}
}

func TestStatsTracksWaiters(t *testing.T) {
	s := NewSemaphore(1)
	_ = s.Acquire(context.Background())

	var wg sync.WaitGroup
	wg.Add(3)
	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			_ = s.Acquire(context.Background())
			s.Release()
		}()
	}
	time.Sleep(20 * time.Millisecond)
	s.Release() // release the initial Acquire; the 3 goroutines drain themselves
	wg.Wait()

	stats := s.Stats()
	if stats.TotalAcquired != 4 {
		t.Errorf("TotalAcquired = %d, want 4", stats.TotalAcquired)
	}
	if stats.TotalWaited < 1 {
		t.Errorf("TotalWaited = %d, want >= 1", stats.TotalWaited)
	}
}

func TestConcurrentNeverExceedsCapacity(t *testing.T) {
	const cap = 5
	s := NewSemaphore(cap)
	var inFlight, peak atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Acquire(context.Background())
			cur := inFlight.Add(1)
			for {
				p := peak.Load()
				if cur <= p || peak.CompareAndSwap(p, cur) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			inFlight.Add(-1)
			s.Release()
		}()
	}
	wg.Wait()
	if peak.Load() > int32(cap) {
		t.Errorf("peak in-flight = %d, exceeded capacity %d", peak.Load(), cap)
	}
}

func TestCapacity(t *testing.T) {
	s := NewSemaphore(7)
	if s.Capacity() != 7 {
		t.Errorf("Capacity = %d, want 7", s.Capacity())
	}
}
