package ratelimit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

func TestBucketBurstAllowedImmediately(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	b := New(clk, 10, 5)
	for i := 0; i < 5; i++ {
		if !b.Allow() {
			t.Fatalf("Allow #%d: want true (within burst)", i)
		}
	}
	if b.Allow() {
		t.Error("Allow #6: want false (burst exhausted)")
	}
}

func TestBucketRefillByTime(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	b := New(clk, 10, 5)
	for i := 0; i < 5; i++ {
		b.Allow()
	}
	if b.Allow() {
		t.Fatal("expected exhausted")
	}
	// 200ms = 2 tokens at 10/s.
	clk.Advance(200 * time.Millisecond)
	if !b.Allow() {
		t.Error("Allow after 200ms: want true (refilled 2 tokens)")
	}
	if !b.Allow() {
		t.Error("Allow #2 after 200ms: want true")
	}
	if b.Allow() {
		t.Error("Allow #3: want false (only 2 tokens refilled)")
	}
}

func TestBucketRefillCapsAtBurst(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	b := New(clk, 10, 3)
	clk.Advance(time.Hour) // far more than the burst worth of tokens
	if got := b.Tokens(); got != 3 {
		t.Errorf("Tokens after huge wait = %v, want 3 (capped at burst)", got)
	}
}

func TestBucketAllowN(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	b := New(clk, 10, 5)
	if !b.AllowN(5) {
		t.Fatal("AllowN(5) at full burst: want true")
	}
	if b.AllowN(1) {
		t.Error("AllowN(1) after consuming burst: want false")
	}
}

func TestBucketWaitNFailsOnExceedsBurst(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	b := New(clk, 10, 5)
	err := b.WaitN(context.Background(), 6)
	if !errors.Is(err, ErrBurstExceeded) {
		t.Errorf("err = %v, want ErrBurstExceeded", err)
	}
}

func TestBucketWaitContextCancel(t *testing.T) {
	// Use real clock so the timer fires in real time (test is fast).
	b := New(clock.System{}, 1, 1) // 1 token/sec, burst 1
	if !b.Allow() {
		t.Fatal("first Allow should succeed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := b.Wait(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Wait err = %v, want DeadlineExceeded", err)
	}
}

func TestBucketPanicsOnBadConfig(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rate  float64
		burst int
	}{
		{"zero rate", 0, 1},
		{"negative rate", -1, 1},
		{"zero burst", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("expected panic for %s", tc.name)
				}
			}()
			New(clock.System{}, tc.rate, tc.burst)
		})
	}
}

func TestManualAllowAndWait(t *testing.T) {
	m := NewManual(2)
	if !m.Allow() {
		t.Fatal("first Allow should succeed")
	}
	if !m.Allow() {
		t.Fatal("second Allow should succeed")
	}
	if m.Allow() {
		t.Error("third Allow: want false")
	}
	m.Add(1)
	if !m.Allow() {
		t.Error("Allow after Add(1): want true")
	}
}

func TestManualWaitBlocksUntilAdd(t *testing.T) {
	m := NewManual(0)
	done := make(chan error, 1)
	go func() {
		done <- m.Wait(context.Background())
	}()

	select {
	case <-done:
		t.Fatal("Wait returned before tokens were added")
	case <-time.After(20 * time.Millisecond):
	}

	m.Add(1)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Wait err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after Add")
	}
	if m.Tokens() != 0 {
		t.Errorf("Tokens after Wait consumed = %d, want 0", m.Tokens())
	}
}

func TestManualWaitContextCancel(t *testing.T) {
	m := NewManual(0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := m.Wait(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestManualWaitWakesOnCancelMidBlock(t *testing.T) {
	m := NewManual(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- m.Wait(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after cancel")
	}
}

func TestManualSet(t *testing.T) {
	m := NewManual(5)
	m.Set(2)
	if got := m.Tokens(); got != 2 {
		t.Errorf("Tokens after Set(2) = %d", got)
	}
}

func TestManualConcurrent(t *testing.T) {
	m := NewManual(100)
	var wg sync.WaitGroup
	allowed := 0
	var amu sync.Mutex
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if m.Allow() {
				amu.Lock()
				allowed++
				amu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 100 {
		t.Errorf("allowed=%d, want exactly 100", allowed)
	}
	if m.Tokens() != 0 {
		t.Errorf("Tokens=%d, want 0", m.Tokens())
	}
}
