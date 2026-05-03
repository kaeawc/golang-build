package perf

import (
	"sync"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

// TimingEntry records the duration of a named phase.
type TimingEntry struct {
	Name       string            `json:"name"`
	DurationMs int64             `json:"durationMs"`
	Children   []TimingEntry     `json:"children,omitempty"`
	Metrics    map[string]int64  `json:"metrics,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Tracker records hierarchical performance timings.
type Tracker interface {
	// Track runs fn and records its duration under name.
	Track(name string, fn func() error) error
	// TrackVoid runs fn and records its duration under name. Use when fn
	// cannot fail; reserve Track for sites that genuinely propagate err.
	TrackVoid(name string, fn func())
	// Serial returns a child tracker that appends entries under name.
	Serial(name string) Tracker
	// End finalizes this child tracker and returns the parent.
	End() Tracker
	// GetTimings returns all recorded timing entries.
	GetTimings() []TimingEntry
	// IsEnabled reports whether timing is active.
	IsEnabled() bool
}

// New returns a Tracker. When enabled is false, the tracker is a no-op
// with zero overhead.
func New(enabled bool) Tracker {
	if !enabled {
		return &noopTracker{}
	}
	return &realTracker{clk: clock.Default}
}

// NewWithClock returns an enabled Tracker that reads time from clk.
// Pass a clock.Fake to drive deterministic durations in tests.
func NewWithClock(clk clock.Clock) Tracker {
	if clk == nil {
		clk = clock.Default
	}
	return &realTracker{clk: clk}
}

// --- real tracker ---

type realTracker struct {
	mu       sync.Mutex
	entries  []TimingEntry
	parent   *realTracker
	name     string
	start    time.Time
	children []TimingEntry
	clk      clock.Clock
}

func (t *realTracker) now() time.Time {
	if t.clk != nil {
		return t.clk.Now()
	}
	return time.Now()
}

func (t *realTracker) IsEnabled() bool { return true }

func (t *realTracker) Track(name string, fn func() error) error {
	start := t.now()
	err := fn()
	dur := t.now().Sub(start).Milliseconds()

	t.mu.Lock()
	t.entries = append(t.entries, TimingEntry{Name: name, DurationMs: dur})
	t.mu.Unlock()

	return err
}

func (t *realTracker) TrackVoid(name string, fn func()) {
	start := t.now()
	fn()
	dur := t.now().Sub(start).Milliseconds()

	t.mu.Lock()
	t.entries = append(t.entries, TimingEntry{Name: name, DurationMs: dur})
	t.mu.Unlock()
}

func (t *realTracker) Serial(name string) Tracker {
	child := &realTracker{
		parent: t,
		name:   name,
		clk:    t.clk,
	}
	child.start = child.now()
	return child
}

func (t *realTracker) End() Tracker {
	if t.parent == nil {
		return t
	}
	dur := t.now().Sub(t.start).Milliseconds()
	entry := TimingEntry{
		Name:       t.name,
		DurationMs: dur,
		Children:   t.entries,
	}
	t.parent.mu.Lock()
	t.parent.entries = append(t.parent.entries, entry)
	t.parent.mu.Unlock()
	return t.parent
}

func (t *realTracker) GetTimings() []TimingEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]TimingEntry, len(t.entries))
	for i, entry := range t.entries {
		out[i] = cloneEntry(entry)
	}
	return out
}

// AddEntry adds a pre-computed timing entry to the tracker.
// This is useful for recording wall-clock time that spans multiple Track calls.
func AddEntry(t Tracker, name string, dur time.Duration) {
	AddEntryDetails(t, name, dur, nil, nil)
}

// AddEntryDetails adds a pre-computed timing entry with optional numeric
// metrics and string attributes.
func AddEntryDetails(t Tracker, name string, dur time.Duration, metrics map[string]int64, attrs map[string]string) {
	if rt, ok := t.(*realTracker); ok {
		rt.mu.Lock()
		rt.entries = append(rt.entries, TimingEntry{
			Name:       name,
			DurationMs: dur.Milliseconds(),
			Metrics:    cloneMetrics(metrics),
			Attributes: cloneAttributes(attrs),
		})
		rt.mu.Unlock()
	}
}

// AddEntries appends pre-built timing entries to the tracker.
func AddEntries(t Tracker, entries []TimingEntry) {
	if rt, ok := t.(*realTracker); ok {
		rt.mu.Lock()
		for _, entry := range entries {
			rt.entries = append(rt.entries, cloneEntry(entry))
		}
		rt.mu.Unlock()
	}
}

func cloneEntry(entry TimingEntry) TimingEntry {
	out := TimingEntry{
		Name:       entry.Name,
		DurationMs: entry.DurationMs,
		Metrics:    cloneMetrics(entry.Metrics),
		Attributes: cloneAttributes(entry.Attributes),
	}
	if len(entry.Children) > 0 {
		out.Children = make([]TimingEntry, len(entry.Children))
		for i, child := range entry.Children {
			out.Children[i] = cloneEntry(child)
		}
	}
	return out
}

func cloneMetrics(metrics map[string]int64) map[string]int64 {
	if len(metrics) == 0 {
		return nil
	}
	out := make(map[string]int64, len(metrics))
	for k, v := range metrics {
		out[k] = v
	}
	return out
}

func cloneAttributes(attrs map[string]string) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]string, len(attrs))
	for k, v := range attrs {
		out[k] = v
	}
	return out
}

// --- no-op tracker ---

type noopTracker struct{}

func (n *noopTracker) IsEnabled() bool                       { return false }
func (n *noopTracker) Track(_ string, fn func() error) error { return fn() }
func (n *noopTracker) TrackVoid(_ string, fn func())          { fn() }
func (n *noopTracker) Serial(_ string) Tracker               { return n }
func (n *noopTracker) End() Tracker                          { return n }
func (n *noopTracker) GetTimings() []TimingEntry             { return nil }
