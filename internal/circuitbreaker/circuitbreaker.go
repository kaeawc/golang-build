// Package circuitbreaker implements the circuit-breaker pattern for outbound
// calls. After enough consecutive failures the breaker opens — subsequent
// calls fast-fail with ErrOpen instead of contacting the downstream — and
// after a cooldown the breaker enters half-open, allowing a probe call. A
// successful probe closes the breaker; a failed probe re-opens it.
//
// The breaker reads "now" from clock.Clock so cooldown behavior is
// deterministic in tests via clock.Fake.
//
// Pair with internal/retry and internal/httpx so a transient downstream
// outage gets isolated cleanly:
//
//	cb := circuitbreaker.New(circuitbreaker.Config{
//	    Threshold: 5,            // open after 5 consecutive failures
//	    Cooldown:  10*time.Second,
//	})
//	err := cb.Do(ctx, func(ctx context.Context) error {
//	    return outboundCall(ctx)
//	})
package circuitbreaker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

// ErrOpen is returned by Do when the breaker is open.
var ErrOpen = errors.New("circuitbreaker: open")

// State is the breaker state.
type State int

const (
	// StateClosed lets calls through.
	StateClosed State = iota
	// StateOpen short-circuits calls with ErrOpen.
	StateOpen
	// StateHalfOpen lets one probe call through; result decides next state.
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Config controls Breaker.
type Config struct {
	// Threshold is the consecutive-failure count that trips the breaker.
	// Defaults to 5.
	Threshold int
	// Cooldown is how long the breaker stays open before entering
	// half-open. Defaults to 30s.
	Cooldown time.Duration
	// IsFailure decides whether an error from the operation counts as a
	// failure for circuit purposes. Default counts every non-nil error.
	// Use this to skip "expected" errors (e.g. 404 from a lookup) so they
	// don't trip the breaker on unrelated outages.
	IsFailure func(error) bool
	// OnStateChange, if non-nil, is invoked synchronously when state
	// transitions. Useful for emitting metrics or logs.
	OnStateChange func(from, to State)
	// Clock supplies "now". Defaults to clock.Default.
	Clock clock.Clock
}

// Breaker is a circuit breaker. Construct via New.
type Breaker struct {
	cfg Config
	clk clock.Clock

	mu             sync.Mutex
	state          State
	failures       int
	openedAt       time.Time
	totalCalls     int64
	totalFailures  int64
	totalShortCirc int64
	transitions    int64
}

// New constructs a Breaker.
func New(cfg Config) *Breaker {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 5
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 30 * time.Second
	}
	if cfg.IsFailure == nil {
		cfg.IsFailure = func(err error) bool { return err != nil }
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.Default
	}
	return &Breaker{cfg: cfg, clk: clk, state: StateClosed}
}

// Do executes op under the breaker. Returns ErrOpen immediately if the
// breaker is open and the cooldown hasn't elapsed; otherwise calls op and
// updates state based on the result. Half-open lets exactly one probe
// through; concurrent callers see ErrOpen until the probe finishes.
func (b *Breaker) Do(ctx context.Context, op func(ctx context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if !b.allow() {
		b.mu.Lock()
		b.totalShortCirc++
		b.mu.Unlock()
		return ErrOpen
	}

	err := op(ctx)
	b.recordResult(err)
	return err
}

// allow reports whether the call should proceed. Transitions Open → HalfOpen
// when the cooldown has elapsed; in HalfOpen, only one caller is admitted at
// a time (others see ErrOpen until the probe completes).
func (b *Breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.totalCalls++

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		if b.clk.Now().Sub(b.openedAt) < b.cfg.Cooldown {
			return false
		}
		b.transition(StateHalfOpen)
		return true
	case StateHalfOpen:
		// Probe is already in flight; reject this call.
		return false
	default:
		return false
	}
}

func (b *Breaker) recordResult(err error) {
	failed := b.cfg.IsFailure(err)

	b.mu.Lock()
	defer b.mu.Unlock()

	if failed {
		b.totalFailures++
	}

	switch b.state {
	case StateClosed:
		if !failed {
			b.failures = 0
			return
		}
		b.failures++
		if b.failures >= b.cfg.Threshold {
			b.openedAt = b.clk.Now()
			b.transition(StateOpen)
		}
	case StateHalfOpen:
		if failed {
			b.openedAt = b.clk.Now()
			b.transition(StateOpen)
			b.failures = b.cfg.Threshold
			return
		}
		// Probe succeeded; close.
		b.failures = 0
		b.transition(StateClosed)
	case StateOpen:
		// Shouldn't reach here — allow() returned false.
	}
}

// transition is called with mu held.
func (b *Breaker) transition(to State) {
	from := b.state
	if from == to {
		return
	}
	b.state = to
	b.transitions++
	if b.cfg.OnStateChange != nil {
		// Drop the lock around the callback so listeners can call back
		// into the breaker without deadlocking.
		fn := b.cfg.OnStateChange
		b.mu.Unlock()
		fn(from, to)
		b.mu.Lock()
	}
}

// State returns the current breaker state.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Stats reports cumulative breaker activity.
type Stats struct {
	State              State `json:"state"`
	Failures           int   `json:"failures"`
	TotalCalls         int64 `json:"totalCalls"`
	TotalFailures      int64 `json:"totalFailures"`
	TotalShortCircuits int64 `json:"totalShortCircuits"`
	Transitions        int64 `json:"transitions"`
}

// Stats returns a snapshot of cumulative counters.
func (b *Breaker) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Stats{
		State:              b.state,
		Failures:           b.failures,
		TotalCalls:         b.totalCalls,
		TotalFailures:      b.totalFailures,
		TotalShortCircuits: b.totalShortCirc,
		Transitions:        b.transitions,
	}
}

// Reset forces the breaker back to closed and clears the failure counter.
// Useful in tests; production callers usually let cooldown handle recovery.
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.openedAt = time.Time{}
	b.transition(StateClosed)
}
