package perf

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

func TestTracker_Enabled(t *testing.T) {
	tracker := New(true)

	err := tracker.Track("testPhase", func() error {
		time.Sleep(5 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("Track returned error: %v", err)
	}

	timings := tracker.GetTimings()
	if len(timings) != 1 {
		t.Fatalf("expected 1 timing entry, got %d", len(timings))
	}
	if timings[0].Name != "testPhase" {
		t.Errorf("expected name=testPhase, got %s", timings[0].Name)
	}
	if timings[0].DurationMs < 0 {
		t.Errorf("expected non-negative duration, got %d", timings[0].DurationMs)
	}
}

func TestTracker_Disabled(t *testing.T) {
	tracker := New(false)

	called := false
	err := tracker.Track("testPhase", func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Track returned error: %v", err)
	}
	if !called {
		t.Error("expected fn to be called even when disabled")
	}

	timings := tracker.GetTimings()
	if timings != nil {
		t.Errorf("expected nil timings when disabled, got %v", timings)
	}
}

func TestTracker_Serial(t *testing.T) {
	tracker := New(true)

	child := tracker.Serial("parent")
	err := child.Track("child1", func() error {
		return nil
	})
	if err != nil {
		t.Fatalf("Track returned error: %v", err)
	}
	err = child.Track("child2", func() error {
		return nil
	})
	if err != nil {
		t.Fatalf("Track returned error: %v", err)
	}
	child.End()

	timings := tracker.GetTimings()
	if len(timings) != 1 {
		t.Fatalf("expected 1 top-level timing entry, got %d", len(timings))
	}
	if timings[0].Name != "parent" {
		t.Errorf("expected name=parent, got %s", timings[0].Name)
	}
	if len(timings[0].Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(timings[0].Children))
	}
	if timings[0].Children[0].Name != "child1" {
		t.Errorf("expected first child=child1, got %s", timings[0].Children[0].Name)
	}
	if timings[0].Children[1].Name != "child2" {
		t.Errorf("expected second child=child2, got %s", timings[0].Children[1].Name)
	}
}

func TestTracker_IsEnabled(t *testing.T) {
	enabled := New(true)
	if !enabled.IsEnabled() {
		t.Error("expected IsEnabled()=true for enabled tracker")
	}

	disabled := New(false)
	if disabled.IsEnabled() {
		t.Error("expected IsEnabled()=false for disabled tracker")
	}
}

func TestTracker_TrackRecordsDuration(t *testing.T) {
	tracker := New(true)

	err := tracker.Track("slow", func() error {
		time.Sleep(10 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("Track returned error: %v", err)
	}

	timings := tracker.GetTimings()
	if len(timings) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(timings))
	}
	// 10ms sleep should produce at least a few ms of recorded duration
	if timings[0].DurationMs < 5 {
		t.Errorf("expected duration >= 5ms, got %d", timings[0].DurationMs)
	}
}

func TestTracker_TrackPropagatesError(t *testing.T) {
	tracker := New(true)

	sentinel := errors.New("test error")
	err := tracker.Track("failing", func() error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}

	// Entry should still be recorded even on error
	timings := tracker.GetTimings()
	if len(timings) != 1 {
		t.Fatalf("expected 1 entry even on error, got %d", len(timings))
	}
	if timings[0].Name != "failing" {
		t.Errorf("expected name=failing, got %s", timings[0].Name)
	}
}

func TestTracker_MultipleTrackCalls(t *testing.T) {
	tracker := New(true)

	names := []string{"phase1", "phase2", "phase3"}
	for _, name := range names {
		err := tracker.Track(name, func() error { return nil })
		if err != nil {
			t.Fatalf("Track(%s) returned error: %v", name, err)
		}
	}

	timings := tracker.GetTimings()
	if len(timings) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(timings))
	}
	for i, name := range names {
		if timings[i].Name != name {
			t.Errorf("entry[%d]: expected name=%s, got %s", i, name, timings[i].Name)
		}
	}
}

func TestTracker_GetTimingsOrder(t *testing.T) {
	tracker := New(true)

	for i := 0; i < 5; i++ {
		name := []string{"a", "b", "c", "d", "e"}[i]
		_ = tracker.Track(name, func() error { return nil })
	}

	timings := tracker.GetTimings()
	expected := []string{"a", "b", "c", "d", "e"}
	if len(timings) != len(expected) {
		t.Fatalf("expected %d entries, got %d", len(expected), len(timings))
	}
	for i, exp := range expected {
		if timings[i].Name != exp {
			t.Errorf("entry[%d]: expected %s, got %s", i, exp, timings[i].Name)
		}
	}
}

func TestTracker_GetTimingsReturnsCopy(t *testing.T) {
	tracker := New(true)
	_ = tracker.Track("original", func() error { return nil })

	timings1 := tracker.GetTimings()
	timings1[0].Name = "mutated"

	timings2 := tracker.GetTimings()
	if timings2[0].Name != "original" {
		t.Errorf("GetTimings should return a copy; mutation leaked: got %s", timings2[0].Name)
	}
}

func TestTracker_SerialRecordsDuration(t *testing.T) {
	tracker := New(true)

	child := tracker.Serial("timed-section")
	time.Sleep(10 * time.Millisecond)
	child.End()

	timings := tracker.GetTimings()
	if len(timings) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(timings))
	}
	if timings[0].DurationMs < 5 {
		t.Errorf("expected serial duration >= 5ms, got %d", timings[0].DurationMs)
	}
}

func TestTracker_SerialChildIsEnabled(t *testing.T) {
	tracker := New(true)
	child := tracker.Serial("sub")
	if !child.IsEnabled() {
		t.Error("serial child of enabled tracker should be enabled")
	}
	child.End()
}

func TestTracker_NestedSerial(t *testing.T) {
	tracker := New(true)

	level1 := tracker.Serial("level1")
	_ = level1.Track("l1-entry", func() error { return nil })

	level2 := level1.Serial("level2")
	_ = level2.Track("l2-entry", func() error { return nil })
	level2.End()

	level1.End()

	timings := tracker.GetTimings()
	if len(timings) != 1 {
		t.Fatalf("expected 1 top-level entry, got %d", len(timings))
	}
	if timings[0].Name != "level1" {
		t.Errorf("expected top name=level1, got %s", timings[0].Name)
	}

	l1Children := timings[0].Children
	if len(l1Children) != 2 {
		t.Fatalf("expected 2 level1 children, got %d", len(l1Children))
	}
	if l1Children[0].Name != "l1-entry" {
		t.Errorf("expected first child=l1-entry, got %s", l1Children[0].Name)
	}
	if l1Children[1].Name != "level2" {
		t.Errorf("expected second child=level2, got %s", l1Children[1].Name)
	}

	l2Children := l1Children[1].Children
	if len(l2Children) != 1 {
		t.Fatalf("expected 1 level2 child, got %d", len(l2Children))
	}
	if l2Children[0].Name != "l2-entry" {
		t.Errorf("expected nested child=l2-entry, got %s", l2Children[0].Name)
	}
}

func TestTracker_EndOnRootReturnsRoot(t *testing.T) {
	tracker := New(true)

	result := tracker.End()
	if result != tracker {
		t.Error("End() on root tracker should return itself")
	}
}

func TestTracker_EndReturnsParent(t *testing.T) {
	tracker := New(true)

	child := tracker.Serial("sub")
	parent := child.End()
	if parent != tracker {
		t.Error("End() on child should return parent tracker")
	}
}

func TestTracker_ConcurrentAccess(t *testing.T) {
	tracker := New(true)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			name := "goroutine"
			_ = tracker.Track(name, func() error {
				time.Sleep(time.Millisecond)
				return nil
			})
		}(i)
	}

	wg.Wait()

	timings := tracker.GetTimings()
	if len(timings) != goroutines {
		t.Errorf("expected %d entries from concurrent Track calls, got %d", goroutines, len(timings))
	}
}

func TestTracker_ConcurrentSerialEnd(t *testing.T) {
	tracker := New(true)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			child := tracker.Serial("concurrent-child")
			_ = child.Track("work", func() error {
				time.Sleep(time.Millisecond)
				return nil
			})
			child.End()
		}()
	}

	wg.Wait()

	timings := tracker.GetTimings()
	if len(timings) != goroutines {
		t.Errorf("expected %d entries from concurrent Serial/End, got %d", goroutines, len(timings))
	}
	for i, entry := range timings {
		if entry.Name != "concurrent-child" {
			t.Errorf("entry[%d]: expected name=concurrent-child, got %s", i, entry.Name)
		}
		if len(entry.Children) != 1 {
			t.Errorf("entry[%d]: expected 1 child, got %d", i, len(entry.Children))
		}
	}
}

func TestAddEntry(t *testing.T) {
	tracker := New(true)

	AddEntry(tracker, "custom-phase", 42*time.Millisecond)

	timings := tracker.GetTimings()
	if len(timings) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(timings))
	}
	if timings[0].Name != "custom-phase" {
		t.Errorf("expected name=custom-phase, got %s", timings[0].Name)
	}
	if timings[0].DurationMs != 42 {
		t.Errorf("expected duration=42ms, got %d", timings[0].DurationMs)
	}

	// AddEntry on a noop tracker should be a no-op
	noop := New(false)
	AddEntry(noop, "ignored", 100*time.Millisecond)
	if noop.GetTimings() != nil {
		t.Error("expected nil timings from noop tracker after AddEntry")
	}
}

func TestNoop_TrackPropagatesError(t *testing.T) {
	tracker := New(false)

	sentinel := errors.New("noop error")
	err := tracker.Track("anything", func() error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("noop Track should propagate error, got %v", err)
	}
}

func TestNoop_SerialReturnsSelf(t *testing.T) {
	tracker := New(false)

	child := tracker.Serial("sub")
	if child != tracker {
		t.Error("noop Serial should return itself")
	}
}

func TestNoop_EndReturnsSelf(t *testing.T) {
	tracker := New(false)

	result := tracker.End()
	if result != tracker {
		t.Error("noop End should return itself")
	}
}

func TestNoop_ChainingDoesNotPanic(t *testing.T) {
	tracker := New(false)

	child := tracker.Serial("a")
	_ = child.Track("b", func() error { return nil })
	grandchild := child.Serial("c")
	_ = grandchild.Track("d", func() error { return nil })
	grandchild.End()
	child.End()

	timings := tracker.GetTimings()
	if timings != nil {
		t.Errorf("expected nil timings from noop, got %v", timings)
	}
}

func TestNoop_IsEnabled(t *testing.T) {
	tracker := New(false)
	if tracker.IsEnabled() {
		t.Error("noop tracker should report IsEnabled()=false")
	}
}

func TestTracker_EmptyGetTimings(t *testing.T) {
	tracker := New(true)

	timings := tracker.GetTimings()
	if len(timings) != 0 {
		t.Errorf("expected 0 entries from fresh tracker, got %d", len(timings))
	}
}

func TestTracker_SerialWithNoChildren(t *testing.T) {
	tracker := New(true)

	child := tracker.Serial("empty-section")
	child.End()

	timings := tracker.GetTimings()
	if len(timings) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(timings))
	}
	if timings[0].Name != "empty-section" {
		t.Errorf("expected name=empty-section, got %s", timings[0].Name)
	}
	if len(timings[0].Children) != 0 {
		t.Errorf("expected 0 children, got %d", len(timings[0].Children))
	}
}

func TestTracker_MixedTrackAndSerial(t *testing.T) {
	tracker := New(true)

	_ = tracker.Track("before", func() error { return nil })

	child := tracker.Serial("middle")
	_ = child.Track("inner", func() error { return nil })
	child.End()

	_ = tracker.Track("after", func() error { return nil })

	timings := tracker.GetTimings()
	if len(timings) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(timings))
	}
	if timings[0].Name != "before" {
		t.Errorf("expected entry[0]=before, got %s", timings[0].Name)
	}
	if timings[1].Name != "middle" {
		t.Errorf("expected entry[1]=middle, got %s", timings[1].Name)
	}
	if timings[2].Name != "after" {
		t.Errorf("expected entry[2]=after, got %s", timings[2].Name)
	}
	if len(timings[1].Children) != 1 {
		t.Fatalf("expected 1 child in middle, got %d", len(timings[1].Children))
	}
	if timings[1].Children[0].Name != "inner" {
		t.Errorf("expected child=inner, got %s", timings[1].Children[0].Name)
	}
}

func TestNewWithClock_DeterministicDurations(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	tracker := NewWithClock(fake)

	const step = 13 * time.Millisecond
	if err := tracker.Track("phase-a", func() error {
		fake.Advance(step)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Track("phase-b", func() error {
		fake.Advance(2 * step)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	timings := tracker.GetTimings()
	if len(timings) != 2 {
		t.Fatalf("len(timings) = %d, want 2", len(timings))
	}
	if timings[0].DurationMs != step.Milliseconds() {
		t.Errorf("phase-a duration = %d, want %d", timings[0].DurationMs, step.Milliseconds())
	}
	if timings[1].DurationMs != (2 * step).Milliseconds() {
		t.Errorf("phase-b duration = %d, want %d", timings[1].DurationMs, (2 * step).Milliseconds())
	}
}

func TestNewWithClock_NestedSerialUsesInjectedClock(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	tracker := NewWithClock(fake)

	const step = 7 * time.Millisecond
	child := tracker.Serial("outer")
	_ = child.Track("inner", func() error {
		fake.Advance(step)
		return nil
	})
	fake.Advance(2 * step)
	_ = child.End()

	timings := tracker.GetTimings()
	if len(timings) != 1 || timings[0].Name != "outer" {
		t.Fatalf("timings = %+v, want one outer entry", timings)
	}
	// outer covers inner (step) + extra (2*step) = 3*step.
	if timings[0].DurationMs != (3 * step).Milliseconds() {
		t.Errorf("outer DurationMs = %d, want %d", timings[0].DurationMs, (3 * step).Milliseconds())
	}
	if len(timings[0].Children) != 1 {
		t.Fatalf("outer.Children len = %d, want 1", len(timings[0].Children))
	}
	if timings[0].Children[0].DurationMs != step.Milliseconds() {
		t.Errorf("inner DurationMs = %d, want %d", timings[0].Children[0].DurationMs, step.Milliseconds())
	}
}

func TestNewWithClock_NilFallsBackToDefault(t *testing.T) {
	t.Parallel()

	tracker := NewWithClock(nil)
	if !tracker.IsEnabled() {
		t.Fatal("NewWithClock(nil) returned a disabled tracker")
	}
	// Just exercise it once; we only assert there's no panic.
	_ = tracker.Track("noop", func() error { return nil })
}
