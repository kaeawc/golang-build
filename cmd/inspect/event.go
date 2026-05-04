package main

import (
	"sync"
	"time"

	"github.com/kaeawc/golang-build/internal/eventbus"
)

// Event is a single observed item. Kind/Level/Message keep the data
// model close to a structured log line; the TUI doesn't care about the
// shape, only how to render it.
type Event struct {
	At      time.Time
	Kind    string
	Level   string // "info", "warn", "error"
	Message string
}

// Snapshot is what the LiveView samples each tick.
type Snapshot struct {
	Total       int64
	ByLevel     map[string]int64
	ByKind      map[string]int64
	Recent      []Event
	Subscribers int
	Dropped     int64
	Elapsed     time.Duration
	Done        bool
}

// Inspector wraps an eventbus.Async with a ring buffer of recent events
// and per-level/-kind counters. Tests use this directly without spinning
// up a TUI.
type Inspector struct {
	bus       *eventbus.Async[Event]
	off       eventbus.Unsubscribe
	cap       int
	startedAt time.Time
	doneAt    time.Time
	done      bool

	mu      sync.Mutex
	total   int64
	byLevel map[string]int64
	byKind  map[string]int64
	recent  []Event
}

// NewInspector returns an Inspector wired to bus, retaining the most
// recent ringCap events for display.
func NewInspector(bus *eventbus.Async[Event], ringCap int) *Inspector {
	if ringCap < 1 {
		ringCap = 10
	}
	i := &Inspector{
		bus:     bus,
		cap:     ringCap,
		byLevel: map[string]int64{},
		byKind:  map[string]int64{},
		recent:  make([]Event, 0, ringCap),
	}
	i.off = bus.Subscribe(i.handle)
	return i
}

// Start records the moment when the producer begins (for rate calc).
func (i *Inspector) Start() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.startedAt = time.Now()
}

// Stop unsubscribes from the bus and freezes Elapsed.
func (i *Inspector) Stop() {
	i.mu.Lock()
	if !i.done {
		i.done = true
		i.doneAt = time.Now()
	}
	i.mu.Unlock()
	if i.off != nil {
		i.off()
	}
}

func (i *Inspector) handle(e Event) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.total++
	i.byLevel[e.Level]++
	i.byKind[e.Kind]++
	i.recent = append(i.recent, e)
	if len(i.recent) > i.cap {
		// Drop oldest. Cheaper than a real ring for cap ~ 10-50.
		i.recent = i.recent[len(i.recent)-i.cap:]
	}
}

// Sample is the LiveSampler-compatible accessor. The second return is
// always false; the model ends the inspection by an explicit user
// action (quit) rather than the sampler reporting done.
func (i *Inspector) Sample() (any, bool) {
	return i.snapshot(), i.isDone()
}

func (i *Inspector) snapshot() Snapshot {
	i.mu.Lock()
	defer i.mu.Unlock()
	byLevel := make(map[string]int64, len(i.byLevel))
	for k, v := range i.byLevel {
		byLevel[k] = v
	}
	byKind := make(map[string]int64, len(i.byKind))
	for k, v := range i.byKind {
		byKind[k] = v
	}
	recent := make([]Event, len(i.recent))
	copy(recent, i.recent)
	end := time.Now()
	if i.done {
		end = i.doneAt
	}
	elapsed := time.Duration(0)
	if !i.startedAt.IsZero() {
		elapsed = end.Sub(i.startedAt)
	}
	return Snapshot{
		Total:       i.total,
		ByLevel:     byLevel,
		ByKind:      byKind,
		Recent:      recent,
		Subscribers: i.bus.Subscribers(),
		Dropped:     i.bus.Stats().Dropped,
		Elapsed:     elapsed,
		Done:        i.done,
	}
}

func (i *Inspector) isDone() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.done
}
