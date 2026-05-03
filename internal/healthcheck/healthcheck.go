// Package healthcheck provides liveness and readiness probes for HTTP
// services.
//
// Liveness probes answer "is the process alive enough to be worth keeping?"
// — fail only on unrecoverable conditions (deadlock, OOM-near). Readiness
// probes answer "should the load balancer route traffic to me right now?" —
// fail when downstream dependencies are unreachable or shutdown has begun.
//
// Typical wiring:
//
//	hc := healthcheck.NewRegistry()
//	hc.RegisterReadiness("db", func(ctx context.Context) error {
//	    return db.PingContext(ctx)
//	})
//	mux.Handle("/healthz", hc.LivenessHandler())
//	mux.Handle("/readyz", hc.ReadinessHandler())
//
//	// On shutdown, flip readiness so the LB drains us before httpserver
//	// stops accepting new requests:
//	shutdownCoord.Register("readiness", func(context.Context) error {
//	    hc.SetShuttingDown()
//	    return nil
//	})
package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

// ProbeFunc reports nil if the probe passes, or an error describing why it
// failed. It must honor ctx.
type ProbeFunc func(ctx context.Context) error

// Status is the aggregate health verdict.
type Status string

const (
	// StatusOK means every probe passed.
	StatusOK Status = "ok"
	// StatusFail means at least one probe failed or shutdown is in progress.
	StatusFail Status = "fail"
)

// Registry holds liveness and readiness probes.
type Registry struct {
	probeTimeout time.Duration
	clk          clock.Clock

	mu        sync.RWMutex
	liveness  map[string]ProbeFunc
	readiness map[string]ProbeFunc

	shuttingDown atomic.Bool
}

// NewRegistry returns an empty Registry. Probes default to a 2-second
// per-probe timeout; pass options to override.
func NewRegistry(opts ...Option) *Registry {
	r := &Registry{
		probeTimeout: 2 * time.Second,
		clk:          clock.Default,
		liveness:     map[string]ProbeFunc{},
		readiness:    map[string]ProbeFunc{},
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Option configures a Registry.
type Option func(*Registry)

// WithProbeTimeout sets the per-probe timeout. Probes that exceed this
// timeout report as failed with a deadline-exceeded error.
func WithProbeTimeout(d time.Duration) Option {
	return func(r *Registry) {
		if d > 0 {
			r.probeTimeout = d
		}
	}
}

// WithClock injects a clock.Clock so probe Duration is deterministic in tests.
func WithClock(clk clock.Clock) Option {
	return func(r *Registry) {
		if clk != nil {
			r.clk = clk
		}
	}
}

// RegisterLiveness adds a liveness probe. Replacing an existing probe by
// name is allowed.
func (r *Registry) RegisterLiveness(name string, fn ProbeFunc) {
	r.mu.Lock()
	r.liveness[name] = fn
	r.mu.Unlock()
}

// RegisterReadiness adds a readiness probe.
func (r *Registry) RegisterReadiness(name string, fn ProbeFunc) {
	r.mu.Lock()
	r.readiness[name] = fn
	r.mu.Unlock()
}

// Unregister removes a probe by name from both liveness and readiness sets.
// Returns true if anything was removed.
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, lOK := r.liveness[name]
	_, rOK := r.readiness[name]
	delete(r.liveness, name)
	delete(r.readiness, name)
	return lOK || rOK
}

// SetShuttingDown flips readiness to fail-all so load balancers stop routing
// traffic during graceful shutdown. Liveness is not affected — the process
// is still alive and should be allowed to drain in-flight requests.
func (r *Registry) SetShuttingDown() { r.shuttingDown.Store(true) }

// IsShuttingDown reports whether SetShuttingDown has been called.
func (r *Registry) IsShuttingDown() bool { return r.shuttingDown.Load() }

// CheckResult is the per-probe outcome.
type CheckResult struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Duration string `json:"duration"`
}

// Report is the aggregate liveness/readiness response.
type Report struct {
	Status       Status        `json:"status"`
	ShuttingDown bool          `json:"shuttingDown,omitempty"`
	Checks       []CheckResult `json:"checks,omitempty"`
}

// CheckLiveness runs every liveness probe in parallel and returns the
// aggregate report. Each probe gets its own per-probe timeout rooted at ctx.
func (r *Registry) CheckLiveness(ctx context.Context) Report {
	return r.runChecks(ctx, r.snapshot(true))
}

// CheckReadiness runs every readiness probe in parallel. If SetShuttingDown
// was called, status is forced to fail without invoking probes.
func (r *Registry) CheckReadiness(ctx context.Context) Report {
	if r.shuttingDown.Load() {
		return Report{Status: StatusFail, ShuttingDown: true}
	}
	return r.runChecks(ctx, r.snapshot(false))
}

// LivenessHandler returns an http.Handler that runs liveness probes and
// writes the JSON report. 200 on pass, 503 on fail.
func (r *Registry) LivenessHandler() http.Handler {
	return r.handler(r.CheckLiveness)
}

// ReadinessHandler returns an http.Handler that runs readiness probes.
func (r *Registry) ReadinessHandler() http.Handler {
	return r.handler(r.CheckReadiness)
}

func (r *Registry) handler(check func(context.Context) Report) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		report := check(req.Context())
		status := http.StatusOK
		if report.Status != StatusOK {
			status = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(report)
	})
}

type namedProbe struct {
	name string
	fn   ProbeFunc
}

func (r *Registry) snapshot(liveness bool) []namedProbe {
	r.mu.RLock()
	src := r.readiness
	if liveness {
		src = r.liveness
	}
	probes := make([]namedProbe, 0, len(src))
	for name, fn := range src {
		probes = append(probes, namedProbe{name: name, fn: fn})
	}
	r.mu.RUnlock()
	sort.Slice(probes, func(i, j int) bool { return probes[i].name < probes[j].name })
	return probes
}

func (r *Registry) runChecks(parent context.Context, probes []namedProbe) Report {
	report := Report{Status: StatusOK}
	if len(probes) == 0 {
		return report
	}

	results := make([]CheckResult, len(probes))
	var wg sync.WaitGroup
	wg.Add(len(probes))
	for i, p := range probes {
		go func(i int, p namedProbe) {
			defer wg.Done()
			results[i] = r.runOne(parent, p)
		}(i, p)
	}
	wg.Wait()

	for _, res := range results {
		if !res.OK {
			report.Status = StatusFail
			break
		}
	}
	report.Checks = results
	return report
}

func (r *Registry) runOne(parent context.Context, p namedProbe) CheckResult {
	ctx, cancel := context.WithTimeout(parent, r.probeTimeout)
	defer cancel()

	start := r.clk.Now()
	err := safeProbe(ctx, p.fn)
	dur := r.clk.Now().Sub(start)

	result := CheckResult{Name: p.name, OK: err == nil, Duration: dur.String()}
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

// safeProbe runs fn and recovers from panics so a buggy probe can't crash
// the health endpoint.
func safeProbe(ctx context.Context, fn ProbeFunc) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = &probePanic{value: rec}
		}
	}()
	return fn(ctx)
}

type probePanic struct{ value any }

func (p *probePanic) Error() string {
	return fmt.Sprintf("probe panic: %v", p.value)
}
