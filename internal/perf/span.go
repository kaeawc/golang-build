package perf

import (
	"sync"
	"time"
)

// Span is a single in-progress timing. End it with Stop — typically via defer:
//
//	defer perf.NewSpan(t, "request").Stop()
//
// or, if you need to attach metrics/attributes:
//
//	s := perf.NewSpan(t, "download")
//	defer s.Stop()
//	s.SetAttr("url", u)
//	s.AddMetric("bytes", n)
//
// Spans against a no-op Tracker are zero-allocation no-ops.
type Span interface {
	// SetAttr attaches a string attribute to the span. Last write wins.
	SetAttr(key, value string)
	// AddMetric attaches a numeric metric. Last write wins.
	AddMetric(key string, value int64)
	// Stop records the elapsed duration on the underlying Tracker. Safe to
	// call multiple times; subsequent calls are no-ops.
	Stop()
}

// NewSpan starts a span on t under name. Always pair with Stop, ideally via
// defer. If t is disabled (a no-op Tracker), the returned span is also a
// no-op and allocates nothing.
func NewSpan(t Tracker, name string) Span {
	if t == nil || !t.IsEnabled() {
		return noopSpan{}
	}
	rt, ok := t.(*realTracker)
	if !ok {
		// Custom Tracker impl — fall back to TrackVoid timing.
		// We approximate by recording a trivially-bracketed timing.
		s := &fallbackSpan{t: t, name: name, start: time.Now()}
		return s
	}
	return &realSpan{t: rt, name: name, start: rt.now()}
}

type realSpan struct {
	t       *realTracker
	name    string
	start   time.Time
	mu      sync.Mutex
	metrics map[string]int64
	attrs   map[string]string
	stopped bool
}

func (s *realSpan) SetAttr(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if s.attrs == nil {
		s.attrs = map[string]string{}
	}
	s.attrs[key] = value
}

func (s *realSpan) AddMetric(key string, value int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if s.metrics == nil {
		s.metrics = map[string]int64{}
	}
	s.metrics[key] = value
}

func (s *realSpan) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	dur := s.t.now().Sub(s.start)
	metrics, attrs := s.metrics, s.attrs
	s.mu.Unlock()
	AddEntryDetails(s.t, s.name, dur, metrics, attrs)
}

type fallbackSpan struct {
	t       Tracker
	name    string
	start   time.Time
	mu      sync.Mutex
	metrics map[string]int64
	attrs   map[string]string
	stopped bool
}

func (s *fallbackSpan) SetAttr(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if s.attrs == nil {
		s.attrs = map[string]string{}
	}
	s.attrs[key] = value
}

func (s *fallbackSpan) AddMetric(key string, value int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if s.metrics == nil {
		s.metrics = map[string]int64{}
	}
	s.metrics[key] = value
}

func (s *fallbackSpan) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	dur := time.Since(s.start)
	metrics, attrs := s.metrics, s.attrs
	s.mu.Unlock()
	AddEntryDetails(s.t, s.name, dur, metrics, attrs)
}

type noopSpan struct{}

func (noopSpan) SetAttr(string, string)  {}
func (noopSpan) AddMetric(string, int64) {}
func (noopSpan) Stop()                   {}
