package circuitbreaker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

var errBoom = errors.New("boom")

func TestClosedAllowsCalls(t *testing.T) {
	cb := New(Config{Threshold: 3, Cooldown: time.Second})
	for i := 0; i < 10; i++ {
		if err := cb.Do(context.Background(), func(context.Context) error { return nil }); err != nil {
			t.Errorf("Do #%d: %v", i, err)
		}
	}
	if cb.State() != StateClosed {
		t.Errorf("State = %v, want closed", cb.State())
	}
}

func TestTripsAfterThreshold(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	cb := New(Config{Threshold: 3, Cooldown: time.Second, Clock: clk})

	for i := 0; i < 3; i++ {
		_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	}
	if cb.State() != StateOpen {
		t.Fatalf("State = %v, want open after %d failures", cb.State(), 3)
	}

	err := cb.Do(context.Background(), func(context.Context) error { return nil })
	if !errors.Is(err, ErrOpen) {
		t.Errorf("Do while open: err = %v, want ErrOpen", err)
	}
}

func TestSuccessResetsFailureCount(t *testing.T) {
	cb := New(Config{Threshold: 3})
	_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	_ = cb.Do(context.Background(), func(context.Context) error { return nil })

	// 2 failures + 1 success → fully reset; 2 more failures should not trip.
	_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	if cb.State() != StateClosed {
		t.Errorf("State = %v, want closed (2 failures < threshold)", cb.State())
	}
}

func TestHalfOpenAfterCooldown(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	cb := New(Config{Threshold: 2, Cooldown: 100 * time.Millisecond, Clock: clk})

	_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	if cb.State() != StateOpen {
		t.Fatal("expected open")
	}

	// Before cooldown elapses, calls short-circuit.
	if err := cb.Do(context.Background(), func(context.Context) error { return nil }); !errors.Is(err, ErrOpen) {
		t.Errorf("before cooldown: err = %v, want ErrOpen", err)
	}

	// Advance past cooldown — next call enters half-open and runs.
	clk.Advance(150 * time.Millisecond)
	called := false
	err := cb.Do(context.Background(), func(context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Errorf("half-open probe: err = %v", err)
	}
	if !called {
		t.Error("half-open probe did not run op")
	}
	if cb.State() != StateClosed {
		t.Errorf("State = %v after successful probe, want closed", cb.State())
	}
}

func TestHalfOpenFailureReopens(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	cb := New(Config{Threshold: 2, Cooldown: 50 * time.Millisecond, Clock: clk})

	_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	clk.Advance(100 * time.Millisecond)

	// Failed probe should re-open.
	_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	if cb.State() != StateOpen {
		t.Errorf("State after failed probe = %v, want open", cb.State())
	}

	// Cooldown applies again.
	if err := cb.Do(context.Background(), func(context.Context) error { return nil }); !errors.Is(err, ErrOpen) {
		t.Errorf("post-reopen call: err = %v, want ErrOpen", err)
	}
}

func TestHalfOpenSerializesProbe(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	cb := New(Config{Threshold: 1, Cooldown: 10 * time.Millisecond, Clock: clk})

	// Trip the breaker.
	_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	clk.Advance(50 * time.Millisecond)

	probeStarted := make(chan struct{})
	probeRelease := make(chan struct{})
	probeErr := make(chan error, 1)
	go func() {
		probeErr <- cb.Do(context.Background(), func(context.Context) error {
			close(probeStarted)
			<-probeRelease
			return nil
		})
	}()

	<-probeStarted
	// While probe is in flight, concurrent calls must short-circuit.
	for i := 0; i < 5; i++ {
		err := cb.Do(context.Background(), func(context.Context) error { return nil })
		if !errors.Is(err, ErrOpen) {
			t.Errorf("concurrent call during probe: err = %v, want ErrOpen", err)
		}
	}
	close(probeRelease)
	if err := <-probeErr; err != nil {
		t.Errorf("probe: %v", err)
	}
}

func TestIsFailureFilter(t *testing.T) {
	notFound := errors.New("404")
	cb := New(Config{
		Threshold: 2,
		IsFailure: func(err error) bool {
			return err != nil && !errors.Is(err, notFound)
		},
	})
	for i := 0; i < 10; i++ {
		_ = cb.Do(context.Background(), func(context.Context) error { return notFound })
	}
	if cb.State() != StateClosed {
		t.Errorf("State = %v, want closed (filter excluded these errors)", cb.State())
	}
}

func TestOnStateChangeFires(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	var transitions []string
	var mu sync.Mutex
	cb := New(Config{
		Threshold: 1,
		Cooldown:  10 * time.Millisecond,
		Clock:     clk,
		OnStateChange: func(from, to State) {
			mu.Lock()
			transitions = append(transitions, from.String()+"->"+to.String())
			mu.Unlock()
		},
	})

	_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	clk.Advance(50 * time.Millisecond)
	_ = cb.Do(context.Background(), func(context.Context) error { return nil })

	mu.Lock()
	defer mu.Unlock()
	want := []string{"closed->open", "open->half-open", "half-open->closed"}
	if len(transitions) != len(want) {
		t.Fatalf("transitions = %v, want %v", transitions, want)
	}
	for i, w := range want {
		if transitions[i] != w {
			t.Errorf("transitions[%d] = %q, want %q", i, transitions[i], w)
		}
	}
}

func TestStatsCounters(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	cb := New(Config{Threshold: 2, Cooldown: time.Hour, Clock: clk})

	_ = cb.Do(context.Background(), func(context.Context) error { return nil })
	_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	for i := 0; i < 3; i++ {
		_ = cb.Do(context.Background(), func(context.Context) error { return nil })
	}

	s := cb.Stats()
	if s.TotalCalls != 6 {
		t.Errorf("TotalCalls = %d, want 6", s.TotalCalls)
	}
	if s.TotalFailures != 2 {
		t.Errorf("TotalFailures = %d, want 2", s.TotalFailures)
	}
	if s.TotalShortCircuits != 3 {
		t.Errorf("TotalShortCircuits = %d, want 3", s.TotalShortCircuits)
	}
	if s.State != StateOpen {
		t.Errorf("State = %v, want open", s.State)
	}
}

func TestReset(t *testing.T) {
	cb := New(Config{Threshold: 1, Cooldown: time.Hour})
	_ = cb.Do(context.Background(), func(context.Context) error { return errBoom })
	if cb.State() != StateOpen {
		t.Fatal("expected open")
	}
	cb.Reset()
	if cb.State() != StateClosed {
		t.Errorf("State after Reset = %v, want closed", cb.State())
	}
}

func TestDoHonorsCtx(t *testing.T) {
	cb := New(Config{Threshold: 5})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := cb.Do(ctx, func(context.Context) error {
		t.Error("op should not run with canceled ctx")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestStateString(t *testing.T) {
	for _, tc := range []struct {
		s    State
		want string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half-open"},
		{State(99), "unknown"},
	} {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("State(%d).String() = %q, want %q", tc.s, got, tc.want)
		}
	}
}

func TestConcurrentDo(t *testing.T) {
	cb := New(Config{Threshold: 100})
	var success, opens atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := cb.Do(context.Background(), func(context.Context) error { return nil })
			if errors.Is(err, ErrOpen) {
				opens.Add(1)
			} else if err == nil {
				success.Add(1)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 200 {
		t.Errorf("success = %d, want 200", success.Load())
	}
	if opens.Load() != 0 {
		t.Errorf("opens = %d, want 0 (all calls should succeed)", opens.Load())
	}
}
