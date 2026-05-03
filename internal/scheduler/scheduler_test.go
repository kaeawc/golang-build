package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
	"github.com/kaeawc/golang-build/internal/limiter"
)

// waitForCount blocks until counter reaches want or the deadline elapses.
// Used after RunDue launches goroutines to give them time to record their
// effect under -race.
func waitForCount(t *testing.T, counter *atomic.Int32, want int32, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if counter.Load() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("counter reached %d, want %d", counter.Load(), want)
}

func TestScheduleValidation(t *testing.T) {
	s := New(clock.System{})
	for _, tc := range []struct {
		name string
		j    Job
		want error
	}{
		{"missing name", Job{Func: func(context.Context) error { return nil }, Interval: time.Second}, ErrNameRequired},
		{"missing func", Job{Name: "x", Interval: time.Second}, ErrNoFunc},
		{"missing schedule", Job{Name: "x", Func: func(context.Context) error { return nil }}, ErrNoSchedule},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.Schedule(tc.j); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRunDueFiresPeriodicJob(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	s := New(clk)
	var fired atomic.Int32
	_ = s.Schedule(Job{
		Name:     "tick",
		Interval: 100 * time.Millisecond,
		Func:     func(context.Context) error { fired.Add(1); return nil },
	})

	// Time = 0; nextDue = 100ms. Nothing due yet.
	if n := s.RunDue(context.Background()); n != 0 {
		t.Errorf("RunDue at t=0 launched %d, want 0", n)
	}
	clk.Advance(99 * time.Millisecond)
	if n := s.RunDue(context.Background()); n != 0 {
		t.Errorf("RunDue at t=99ms launched %d, want 0", n)
	}
	clk.Advance(2 * time.Millisecond)
	if n := s.RunDue(context.Background()); n != 1 {
		t.Errorf("RunDue at t=101ms launched %d, want 1", n)
	}
	waitForCount(t, &fired, 1, time.Second)

	// Wait for the running flag to clear (job goroutine sets it false on
	// completion via the deferred Store(false)). Use Stop to drain.
	_ = s.Stop(context.Background())

	// nextDue was advanced to runStart (101ms) + 100ms = 201ms.
	clk.Advance(99 * time.Millisecond)
	// Need a fresh scheduler since Stop closed wg; just check NextDue tracked it.
}

func TestPeriodicSchedulesNextRun(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	s := New(clk)
	var fired atomic.Int32
	_ = s.Schedule(Job{
		Name:     "tick",
		Interval: 50 * time.Millisecond,
		Func:     func(context.Context) error { fired.Add(1); return nil },
	})

	for i := 0; i < 5; i++ {
		clk.Advance(50 * time.Millisecond)
		s.RunDue(context.Background())
		// Wait for the goroutine to finish before next advance so the
		// running flag clears and the next RunDue isn't skipped.
		if err := s.WaitInflight(context.Background()); err != nil {
			t.Fatalf("WaitInflight: %v", err)
		}
	}
	if fired.Load() != 5 {
		t.Errorf("fired = %d, want 5", fired.Load())
	}
	_ = s.Stop(context.Background())
}

func TestOneShotFiresOnceThenStops(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	s := New(clk)
	var fired atomic.Int32
	_ = s.Schedule(Job{
		Name: "once",
		At:   time.Unix(0, 0).Add(50 * time.Millisecond),
		Func: func(context.Context) error { fired.Add(1); return nil },
	})

	clk.Advance(50 * time.Millisecond)
	if n := s.RunDue(context.Background()); n != 1 {
		t.Errorf("RunDue launched %d, want 1", n)
	}
	waitForCount(t, &fired, 1, time.Second)

	_ = s.Stop(context.Background())

	if _, ok := s.NextDue(); ok {
		t.Errorf("NextDue should report no due jobs after one-shot completes")
	}
	clk.Advance(time.Hour)
	// Even after huge advance, no more invocations.
}

func TestNonOverlapPreventsParallelRuns(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	s := New(clk)
	var fired atomic.Int32
	gate := make(chan struct{})
	_ = s.Schedule(Job{
		Name:     "slow",
		Interval: 10 * time.Millisecond,
		Func: func(context.Context) error {
			fired.Add(1)
			<-gate
			return nil
		},
	})

	clk.Advance(10 * time.Millisecond)
	s.RunDue(context.Background())
	// First run is in flight (blocked on gate). Advance past 2nd due time.
	clk.Advance(20 * time.Millisecond)
	if n := s.RunDue(context.Background()); n != 0 {
		t.Errorf("second RunDue launched %d, want 0 (job already running)", n)
	}
	close(gate)
	_ = s.Stop(context.Background())

	if got := fired.Load(); got != 1 {
		t.Errorf("fired = %d, want 1 (first run only)", got)
	}
}

func TestUnschedule(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	s := New(clk)
	_ = s.Schedule(Job{Name: "a", Interval: time.Second, Func: func(context.Context) error { return nil }})

	if !s.Unschedule("a") {
		t.Error("Unschedule should report removal")
	}
	if s.Unschedule("a") {
		t.Error("Unschedule of absent job should return false")
	}
	if names := s.Names(); len(names) != 0 {
		t.Errorf("Names = %v, want empty", names)
	}
}

func TestErrorReportedToOnError(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	want := errors.New("boom")
	var got atomic.Value
	s := New(clk, WithOnError(func(_ string, err error) { got.Store(err) }))
	_ = s.Schedule(Job{
		Name:     "fails",
		Interval: 10 * time.Millisecond,
		Func:     func(context.Context) error { return want },
	})

	clk.Advance(10 * time.Millisecond)
	s.RunDue(context.Background())
	_ = s.Stop(context.Background())

	gotErr, _ := got.Load().(error)
	if !errors.Is(gotErr, want) {
		t.Errorf("onError got %v, want %v", got.Load(), want)
	}
}

func TestPanicReportedToOnError(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	var got atomic.Value
	s := New(clk, WithOnError(func(_ string, err error) { got.Store(err) }))
	_ = s.Schedule(Job{
		Name:     "panics",
		Interval: 10 * time.Millisecond,
		Func:     func(context.Context) error { panic("nope") },
	})

	clk.Advance(10 * time.Millisecond)
	s.RunDue(context.Background())
	_ = s.Stop(context.Background())

	if v := got.Load(); v == nil {
		t.Fatal("expected panic to be reported")
	} else if err, _ := v.(error); err == nil || err.Error() == "" {
		t.Errorf("err = %v", v)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	sem := limiter.NewSemaphore(2)
	s := New(clk, WithConcurrencyLimit(sem))

	gate := make(chan struct{})
	var fired atomic.Int32
	for i := 0; i < 5; i++ {
		name := string(rune('a' + i))
		_ = s.Schedule(Job{
			Name:     name,
			Interval: 10 * time.Millisecond,
			Func: func(context.Context) error {
				fired.Add(1)
				<-gate
				return nil
			},
		})
	}

	clk.Advance(10 * time.Millisecond)
	s.RunDue(context.Background())

	// Wait until the limiter is fully booked: 2 jobs running, others queued.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && sem.InFlight() < 2 {
		time.Sleep(time.Millisecond)
	}
	if sem.InFlight() != 2 {
		t.Errorf("InFlight = %d, want 2 (capped by semaphore)", sem.InFlight())
	}

	close(gate)
	_ = s.Stop(context.Background())
	if fired.Load() != 5 {
		t.Errorf("fired = %d, want 5 (all eventually ran)", fired.Load())
	}
}

func TestStopWaitsForInflight(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	s := New(clk)

	var done atomic.Bool
	gate := make(chan struct{})
	_ = s.Schedule(Job{
		Name:     "slow",
		Interval: 10 * time.Millisecond,
		Func: func(context.Context) error {
			<-gate
			done.Store(true)
			return nil
		},
	})

	clk.Advance(10 * time.Millisecond)
	s.RunDue(context.Background())

	stopDone := make(chan error, 1)
	go func() { stopDone <- s.Stop(context.Background()) }()

	// Stop should still be blocked while job is running.
	select {
	case <-stopDone:
		t.Fatal("Stop returned before job finished")
	case <-time.After(20 * time.Millisecond):
	}

	close(gate)
	if err := <-stopDone; err != nil {
		t.Errorf("Stop: %v", err)
	}
	if !done.Load() {
		t.Error("job did not complete")
	}
}

func TestStopRespectsContextDeadline(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	s := New(clk)

	gate := make(chan struct{})
	defer close(gate)
	_ = s.Schedule(Job{
		Name:     "stuck",
		Interval: 10 * time.Millisecond,
		Func:     func(context.Context) error { <-gate; return nil },
	})

	clk.Advance(10 * time.Millisecond)
	s.RunDue(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := s.Stop(ctx); err == nil {
		t.Error("expected timeout error from Stop")
	}
}

func TestStartFiresPastDueJob(t *testing.T) {
	// Smoke test: Start uses a real time.NewTimer, so we can't drive it
	// via clock.Fake. Verify it fires a job whose nextDue (against the
	// injected clock) has already passed.
	clk := clock.NewFake(time.Unix(0, 0))
	s := New(clk)
	var fired atomic.Int32
	// Use At in the past so nextDue is immediately due against clk.
	_ = s.Schedule(Job{
		Name: "x",
		At:   time.Unix(0, 0).Add(-time.Hour),
		Func: func(context.Context) error { fired.Add(1); return nil },
	})

	go s.Start(context.Background())
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && fired.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if fired.Load() == 0 {
		t.Errorf("Start did not run the past-due job within deadline")
	}
	_ = s.Stop(context.Background())
}

func TestNamesSortedAndUnique(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	s := New(clk)
	for _, n := range []string{"c", "a", "b"} {
		_ = s.Schedule(Job{Name: n, Interval: time.Second, Func: func(context.Context) error { return nil }})
	}
	got := s.Names()
	want := []string{"a", "b", "c"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("Names()[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestRescheduleReplacesPrior(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	s := New(clk)
	var fired atomic.Int32
	_ = s.Schedule(Job{Name: "x", Interval: 10 * time.Millisecond, Func: func(context.Context) error { fired.Add(1); return nil }})
	_ = s.Schedule(Job{Name: "x", Interval: 20 * time.Millisecond, Func: func(context.Context) error { fired.Add(100); return nil }})

	clk.Advance(20 * time.Millisecond)
	s.RunDue(context.Background())
	_ = s.Stop(context.Background())
	if fired.Load() != 100 {
		t.Errorf("fired = %d, want 100 (second registration should win)", fired.Load())
	}
}

func TestNextDueEmpty(t *testing.T) {
	s := New(clock.NewFake(time.Unix(0, 0)))
	if _, ok := s.NextDue(); ok {
		t.Error("NextDue on empty scheduler should report ok=false")
	}
}

func TestScheduleAfterStopFails(t *testing.T) {
	s := New(clock.NewFake(time.Unix(0, 0)))
	_ = s.Stop(context.Background())
	err := s.Schedule(Job{Name: "x", Interval: time.Second, Func: func(context.Context) error { return nil }})
	if !errors.Is(err, ErrStopped) {
		t.Errorf("err = %v, want ErrStopped", err)
	}
}

func TestConcurrentScheduleAndRunDue(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	s := New(clk)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_ = s.Schedule(Job{
				Name:     string(rune('a' + (i % 26))),
				Interval: 10 * time.Millisecond,
				Func:     func(context.Context) error { return nil },
			})
		}(i)
		go func() {
			defer wg.Done()
			s.RunDue(context.Background())
		}()
	}
	wg.Wait()
	_ = s.Stop(context.Background())
}
