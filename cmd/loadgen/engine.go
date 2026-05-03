package main

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"
)

// HTTPClient is the slice of net/http used by the engine. Splitting it
// out lets tests inject a fake without spinning a real server.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config controls a load run.
type Config struct {
	URL         string
	Method      string
	Concurrency int
	Duration    time.Duration
	Client      HTTPClient
	// Now is injected for deterministic tests; defaults to time.Now.
	Now func() time.Time
}

// Snapshot is a point-in-time view of engine stats. Returned by
// Engine.Snapshot and rendered by the live dashboard.
type Snapshot struct {
	Elapsed       time.Duration
	Total         int
	Errors        int
	StatusCounts  map[int]int
	RPS           float64
	P50           time.Duration
	P95           time.Duration
	P99           time.Duration
	Done          bool
	DoneAt        time.Time
	StartedAt     time.Time
	TargetURL     string
	Concurrency   int
	TargetSeconds float64
}

// Engine drives Concurrency goroutines hammering URL until Duration
// elapses. Stats are accumulated under a single mutex; Snapshot is
// safe to call concurrently.
type Engine struct {
	cfg Config

	mu           sync.Mutex
	startedAt    time.Time
	doneAt       time.Time
	done         bool
	total        int
	errors       int
	statusCounts map[int]int
	latencies    []time.Duration
}

// NewEngine validates cfg and returns a ready-to-Start engine.
func NewEngine(cfg Config) *Engine {
	if cfg.Method == "" {
		cfg.Method = http.MethodGet
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Engine{
		cfg:          cfg,
		statusCounts: make(map[int]int),
		latencies:    make([]time.Duration, 0, 1024),
	}
}

// Start runs the load test, blocking until Duration elapses or ctx is
// cancelled. Spawn it in a goroutine; poll Snapshot for live stats.
func (e *Engine) Start(ctx context.Context) {
	e.mu.Lock()
	e.startedAt = e.cfg.Now()
	e.mu.Unlock()

	deadline := e.startedAt.Add(e.cfg.Duration)
	runCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < e.cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.workerLoop(runCtx)
		}()
	}
	wg.Wait()

	e.mu.Lock()
	e.doneAt = e.cfg.Now()
	e.done = true
	e.mu.Unlock()
}

func (e *Engine) workerLoop(ctx context.Context) {
	for ctx.Err() == nil {
		e.issueOne(ctx)
	}
}

func (e *Engine) issueOne(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, e.cfg.Method, e.cfg.URL, nil)
	if err != nil {
		e.recordError()
		return
	}
	start := e.cfg.Now()
	resp, err := e.cfg.Client.Do(req)
	dur := e.cfg.Now().Sub(start)
	if err != nil {
		// Don't count cancellation/deadline as an error: it's just the
		// run finishing while a worker happened to be in-flight.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		e.recordError()
		return
	}
	defer resp.Body.Close()
	e.record(resp.StatusCode, dur)
}

func (e *Engine) record(status int, latency time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.total++
	e.statusCounts[status]++
	e.latencies = append(e.latencies, latency)
	if status >= 400 {
		e.errors++
	}
}

func (e *Engine) recordError() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.total++
	e.errors++
}

// Snapshot returns a copy of current stats. Safe to call concurrently
// with Start.
func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.cfg.Now()
	elapsed := time.Duration(0)
	if !e.startedAt.IsZero() {
		end := now
		if e.done {
			end = e.doneAt
		}
		elapsed = end.Sub(e.startedAt)
	}

	rps := 0.0
	if elapsed > 0 {
		rps = float64(e.total) / elapsed.Seconds()
	}

	p50, p95, p99 := percentiles(e.latencies)

	statuses := make(map[int]int, len(e.statusCounts))
	for k, v := range e.statusCounts {
		statuses[k] = v
	}

	return Snapshot{
		Elapsed:       elapsed,
		Total:         e.total,
		Errors:        e.errors,
		StatusCounts:  statuses,
		RPS:           rps,
		P50:           p50,
		P95:           p95,
		P99:           p99,
		Done:          e.done,
		DoneAt:        e.doneAt,
		StartedAt:     e.startedAt,
		TargetURL:     e.cfg.URL,
		Concurrency:   e.cfg.Concurrency,
		TargetSeconds: e.cfg.Duration.Seconds(),
	}
}

func percentiles(latencies []time.Duration) (p50, p95, p99 time.Duration) {
	if len(latencies) == 0 {
		return 0, 0, 0
	}
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	pick := func(p float64) time.Duration {
		idx := int(math.Round(float64(len(sorted)-1) * p))
		return sorted[idx]
	}
	return pick(0.50), pick(0.95), pick(0.99)
}
