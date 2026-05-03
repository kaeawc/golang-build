// Package scheduler provides an in-process job scheduler for periodic and
// one-shot tasks (cache cleanup, alert polling, daily reports).
//
// Jobs are scheduled by name with either an Interval (periodic) or At
// (one-shot at a specific time). The scheduler reads "now" from
// clock.Clock so scheduling decisions are deterministic in tests via
// clock.Fake. Tests can call RunDue directly to advance state without
// real-time waits; production code calls Start which loops on a real
// timer.
//
// Per-job concurrency is bounded by a non-overlap guard: a job that takes
// longer than its interval will not start a second invocation until the
// previous one finishes. Cross-job concurrency is bounded by an optional
// limiter.Limiter.
//
//	sched := scheduler.New(clock.System{},
//	    scheduler.WithConcurrencyLimit(8),
//	    scheduler.WithOnError(func(name string, err error) {
//	        log.Error("scheduled job failed", "job", name, "err", err)
//	    }),
//	)
//	_ = sched.Schedule(scheduler.Job{
//	    Name: "kv-sweep", Interval: 30 * time.Second,
//	    Func: func(ctx context.Context) error { kv.Sweep(); return nil },
//	})
//	go sched.Start(ctx)
//	defer sched.Stop(context.Background())
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
	"github.com/kaeawc/golang-build/internal/limiter"
	"github.com/kaeawc/golang-build/internal/logger"
)

// idleTimeout caps how long Start sleeps when no jobs are due, so a
// scheduler with everything paused still wakes occasionally to notice
// ctx cancellation in the absence of a wake signal.
const idleTimeout = time.Minute

// JobFunc is the function a scheduled job runs.
type JobFunc func(ctx context.Context) error

// Job describes a scheduled task. At least one of Interval or At must be
// set:
//   - Interval only: periodic. First run at Now+Interval, then every
//     Interval after each run starts.
//   - At only: one-shot at the specified time.
//   - Both: periodic with the first run at At, subsequent runs every
//     Interval.
type Job struct {
	// Name uniquely identifies the job; used for unscheduling and error
	// reporting. Re-scheduling the same name replaces the prior entry.
	Name string
	// Func runs when the job fires.
	Func JobFunc
	// Interval, if > 0, makes the job periodic. After each run starts
	// nextDue is set to runStart+Interval. Set to zero for one-shot.
	Interval time.Duration
	// At, if non-zero, overrides the first-run time. Without it, the
	// first run fires at Now+Interval.
	At time.Time
}

// ErrNoSchedule is returned by Schedule if neither Interval nor At is set.
var ErrNoSchedule = errors.New("scheduler: Job needs Interval > 0 or non-zero At")

// ErrNameRequired is returned if Job.Name is empty.
var ErrNameRequired = errors.New("scheduler: Job.Name is required")

// ErrNoFunc is returned if Job.Func is nil.
var ErrNoFunc = errors.New("scheduler: Job.Func is required")

// ErrStopped is returned by Schedule when the scheduler has already been
// stopped — silently registering jobs that will never fire is a bug, so
// callers see the failure explicitly.
var ErrStopped = errors.New("scheduler: stopped")

// Option configures a Scheduler.
type Option func(*Scheduler)

// WithConcurrencyLimit caps the number of jobs running concurrently. Pass
// any limiter.Limiter (e.g. limiter.NewSemaphore(8)). Without this option
// every due job runs in its own goroutine without a global bound.
func WithConcurrencyLimit(l limiter.Limiter) Option {
	return func(s *Scheduler) { s.lim = l }
}

// WithOnError registers a callback for job errors and panics. When unset,
// errors are silently dropped — pass a logger.Logger.Error closure, a
// metric increment, or use WithLogger for the standard slog wiring.
func WithOnError(fn func(name string, err error)) Option {
	return func(s *Scheduler) { s.onError = fn }
}

// WithLogger sets a default OnError that emits a structured error log
// via lg. Equivalent to WithOnError that calls lg.Error("scheduled job
// failed", "job", name, "err", err). WithOnError takes precedence if
// both are passed.
func WithLogger(lg logger.Logger) Option {
	return func(s *Scheduler) {
		if s.onError != nil || lg == nil {
			return
		}
		s.onError = func(name string, err error) {
			lg.Error("scheduled job failed", "job", name, "err", err)
		}
	}
}

// Scheduler runs scheduled jobs. Construct via New.
type Scheduler struct {
	clk     clock.Clock
	lim     limiter.Limiter
	onError func(name string, err error)

	mu     sync.Mutex
	jobs   map[string]*entry
	cancel context.CancelFunc // guarded by mu

	stopped   atomic.Bool
	wake      chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

type entry struct {
	spec    Job
	nextDue time.Time
	running atomic.Bool
}

// New constructs a Scheduler.
func New(clk clock.Clock, opts ...Option) *Scheduler {
	if clk == nil {
		clk = clock.Default
	}
	s := &Scheduler{
		clk:  clk,
		jobs: map[string]*entry{},
		wake: make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// signalWake nudges Start to recompute the next sleep after a Schedule or
// Unschedule. Non-blocking: if a wake is already pending, it stays
// pending.
func (s *Scheduler) signalWake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Schedule adds or replaces a job. Returns an error if the Job is
// incomplete or the scheduler has been stopped.
func (s *Scheduler) Schedule(j Job) error {
	if j.Name == "" {
		return ErrNameRequired
	}
	if j.Func == nil {
		return ErrNoFunc
	}
	if j.Interval <= 0 && j.At.IsZero() {
		return ErrNoSchedule
	}
	if s.stopped.Load() {
		return ErrStopped
	}

	now := s.clk.Now()
	first := j.At
	if first.IsZero() {
		first = now.Add(j.Interval)
	}

	s.mu.Lock()
	s.jobs[j.Name] = &entry{spec: j, nextDue: first}
	s.mu.Unlock()
	s.signalWake()
	return nil
}

// Unschedule removes a job by name. Returns true if a job was removed.
// Currently-running invocations are not interrupted.
func (s *Scheduler) Unschedule(name string) bool {
	s.mu.Lock()
	_, ok := s.jobs[name]
	if ok {
		delete(s.jobs, name)
	}
	s.mu.Unlock()
	if ok {
		s.signalWake()
	}
	return ok
}

// Names returns the registered job names in sorted order.
func (s *Scheduler) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.jobs))
	for n := range s.jobs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// NextDue returns the earliest due time across all jobs. ok is false when
// there are no jobs registered.
func (s *Scheduler) NextDue() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var earliest time.Time
	found := false
	for _, e := range s.jobs {
		if e.nextDue.IsZero() {
			continue
		}
		if !found || e.nextDue.Before(earliest) {
			earliest = e.nextDue
			found = true
		}
	}
	return earliest, found
}

// RunDue runs every job whose nextDue is <= clk.Now() and that isn't
// already in flight. Each job runs in its own goroutine (under the
// optional limiter); RunDue returns the number of invocations launched
// without waiting for them to finish. Use Stop or the WaitGroup-style
// methods to wait for completion.
//
// Tests call RunDue directly to advance scheduler state on a clock.Fake
// without real-time waits.
func (s *Scheduler) RunDue(ctx context.Context) int {
	now := s.clk.Now()
	s.mu.Lock()
	due := make([]*entry, 0)
	for _, e := range s.jobs {
		if e.nextDue.IsZero() || e.nextDue.After(now) {
			continue
		}
		if e.running.Load() {
			continue
		}
		due = append(due, e)
	}
	s.mu.Unlock()

	launched := 0
	for _, e := range due {
		if !e.running.CompareAndSwap(false, true) {
			continue
		}
		s.advanceNext(e, now)
		launched++
		s.wg.Add(1)
		go s.run(ctx, e)
	}
	return launched
}

// advanceNext sets nextDue for the next firing or zero for one-shot jobs.
// Caller is responsible for serialization (running flag handles overlap).
func (s *Scheduler) advanceNext(e *entry, runStart time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.spec.Interval > 0 {
		e.nextDue = runStart.Add(e.spec.Interval)
	} else {
		e.nextDue = time.Time{}
	}
}

func (s *Scheduler) run(ctx context.Context, e *entry) {
	defer s.wg.Done()
	defer e.running.Store(false)

	if s.lim != nil {
		if err := s.lim.Acquire(ctx); err != nil {
			s.report(e.spec.Name, fmt.Errorf("acquire concurrency permit: %w", err))
			return
		}
		defer s.lim.Release()
	}

	if err := safeRun(ctx, e.spec.Func); err != nil {
		s.report(e.spec.Name, err)
	}
}

// safeRun catches panics so a buggy job can't crash the scheduler.
func safeRun(ctx context.Context, fn JobFunc) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v", rec)
		}
	}()
	return fn(ctx)
}

func (s *Scheduler) report(name string, err error) {
	if s.onError != nil {
		s.onError(name, err)
	}
}

// Start runs the scheduling loop in the calling goroutine until ctx is
// canceled or Stop is called. The loop sleeps until the next job is due
// (capped at idleTimeout to bound idle wakeups) or until Schedule /
// Unschedule signals a wake.
//
// Start may only be called once per Scheduler. Subsequent calls return
// without doing anything.
func (s *Scheduler) Start(ctx context.Context) {
	started := false
	s.startOnce.Do(func() {
		runCtx, cancel := context.WithCancel(ctx)
		s.mu.Lock()
		s.cancel = cancel
		s.mu.Unlock()
		ctx = runCtx
		started = true
	})
	if !started {
		return
	}

	for {
		s.RunDue(ctx)
		wait := s.nextSleep()
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-s.wake:
			t.Stop()
		case <-t.C:
		}
	}
}

// nextSleep returns how long Start should sleep before its next RunDue.
// Caps at idleTimeout when there are no due jobs so a paused scheduler
// still wakes occasionally to observe context cancellation.
func (s *Scheduler) nextSleep() time.Duration {
	next, ok := s.NextDue()
	if !ok {
		return idleTimeout
	}
	d := next.Sub(s.clk.Now())
	if d <= 0 {
		// Already due — yield briefly so callers don't busy-loop on a
		// pathological clock.
		return time.Microsecond
	}
	if d > idleTimeout {
		return idleTimeout
	}
	return d
}

// WaitInflight blocks until every currently-running job invocation has
// completed, or until ctx cancels. Useful in tests after RunDue to wait
// for goroutines to finish before advancing the fake clock again.
func (s *Scheduler) WaitInflight(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop signals Start to exit and waits for in-flight jobs to finish or
// for ctx to expire, whichever comes first. Subsequent Schedule calls
// return ErrStopped. Safe to call before or after Start, and may be
// called multiple times.
func (s *Scheduler) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		s.stopped.Store(true)
		s.mu.Lock()
		cancel := s.cancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	})
	if err := s.WaitInflight(ctx); err != nil {
		return fmt.Errorf("scheduler: stop timed out: %w", err)
	}
	return nil
}
