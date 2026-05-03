package perf

import (
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

func TestSpanRecordsDuration(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	tr := NewWithClock(clk)

	s := NewSpan(tr, "phase")
	clk.Advance(150 * time.Millisecond)
	s.Stop()

	entries := tr.GetTimings()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Name != "phase" || entries[0].DurationMs != 150 {
		t.Errorf("entry = %+v", entries[0])
	}
}

func TestSpanSetAttrAndMetric(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	tr := NewWithClock(clk)

	s := NewSpan(tr, "download")
	s.SetAttr("url", "https://x")
	s.AddMetric("bytes", 1024)
	clk.Advance(50 * time.Millisecond)
	s.Stop()

	entries := tr.GetTimings()
	if entries[0].Attributes["url"] != "https://x" {
		t.Errorf("attrs = %v", entries[0].Attributes)
	}
	if entries[0].Metrics["bytes"] != 1024 {
		t.Errorf("metrics = %v", entries[0].Metrics)
	}
}

func TestSpanStopIsIdempotent(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	tr := NewWithClock(clk)

	s := NewSpan(tr, "phase")
	clk.Advance(10 * time.Millisecond)
	s.Stop()
	clk.Advance(100 * time.Millisecond)
	s.Stop() // should be no-op

	entries := tr.GetTimings()
	if len(entries) != 1 || entries[0].DurationMs != 10 {
		t.Errorf("entries = %+v, want exactly one 10ms entry", entries)
	}
}

func TestSpanOnDisabledTrackerIsNoop(t *testing.T) {
	tr := New(false)
	s := NewSpan(tr, "phase")
	s.SetAttr("k", "v")
	s.AddMetric("m", 1)
	s.Stop()

	if entries := tr.GetTimings(); entries != nil {
		t.Errorf("disabled tracker should record nothing, got %v", entries)
	}
	if _, ok := s.(noopSpan); !ok {
		t.Errorf("expected noopSpan for disabled tracker, got %T", s)
	}
}

func TestSpanDeferUsage(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	tr := NewWithClock(clk)

	func() {
		defer NewSpan(tr, "request").Stop()
		clk.Advance(33 * time.Millisecond)
	}()

	entries := tr.GetTimings()
	if len(entries) != 1 || entries[0].DurationMs != 33 {
		t.Errorf("entries = %+v", entries)
	}
}
