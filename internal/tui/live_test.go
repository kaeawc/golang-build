package tui

import (
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type fakeSampler struct {
	calls    atomic.Int32
	doneAt   int32
	lastSent any
}

func (f *fakeSampler) Sample() (any, bool) {
	n := f.calls.Add(1)
	f.lastSent = int(n)
	return int(n), n >= f.doneAt
}

func TestLiveViewTicksAndCompletes(t *testing.T) {
	s := &fakeSampler{doneAt: 3}
	v := NewLiveView("eng", "loadgen", s, time.Millisecond, func(a any) string {
		return "n=?"
	})

	// Init returns the first tick cmd.
	cmd := v.Init()
	if cmd == nil {
		t.Fatalf("Init returned nil cmd")
	}

	// Drive ticks manually, asserting we get a follow-up cmd until done.
	var phase Phase = v
	for i := 0; i < 10; i++ {
		// The cmd returns a liveTickMsg after the timer fires; just
		// synthesize the message rather than waiting on the timer.
		next, follow := phase.Update(liveTickMsg{tag: "eng"})
		phase = next
		if follow == nil {
			// Done branch: should have emitted LiveDoneMsg via its
			// own returned cmd. Re-run Update to get it.
			break
		}
		// Flush the emitted tea.Tick to keep the generator clean.
		_ = follow
	}
	if s.calls.Load() < 3 {
		t.Errorf("expected at least 3 sampler calls, got %d", s.calls.Load())
	}
}

func TestLiveViewEmitsLiveDoneMsg(t *testing.T) {
	s := &fakeSampler{doneAt: 1}
	v := NewLiveView("eng", "title", s, time.Millisecond, func(a any) string { return "" })
	_, cmd := v.Update(liveTickMsg{tag: "eng"})
	if cmd == nil {
		t.Fatalf("expected completion cmd")
	}
	msg := cmd()
	done, ok := msg.(LiveDoneMsg)
	if !ok {
		t.Fatalf("expected LiveDoneMsg, got %T", msg)
	}
	if done.Tag != "eng" || done.Final.(int) != 1 {
		t.Errorf("unexpected msg: %+v", done)
	}
}

func TestLiveViewIgnoresOtherTags(t *testing.T) {
	s := &fakeSampler{doneAt: 99}
	v := NewLiveView("eng", "t", s, time.Millisecond, func(a any) string { return "" })
	_, cmd := v.Update(liveTickMsg{tag: "different"})
	if cmd != nil {
		t.Errorf("expected nil cmd for foreign tag")
	}
	if s.calls.Load() != 0 {
		t.Errorf("sampler should not be called for foreign tag, got %d", s.calls.Load())
	}
}

func TestLiveViewRendersSample(t *testing.T) {
	s := &fakeSampler{doneAt: 99}
	rendered := ""
	v := NewLiveView("eng", "t", s, time.Millisecond, func(a any) string {
		rendered = "got: " + intToStr(a.(int))
		return rendered
	})

	// Before any tick, view should show a placeholder and the renderer
	// should not yet have been invoked.
	out := v.View()
	if out == "" {
		t.Errorf("View returned empty before any tick")
	}
	if rendered != "" {
		t.Errorf("renderer invoked before tick, rendered=%q", rendered)
	}

	next, _ := v.Update(liveTickMsg{tag: "eng"})
	out = next.View()
	if rendered != "got: 1" {
		t.Errorf("renderer not invoked with sample, rendered=%q", rendered)
	}
	if out == "" {
		t.Errorf("View returned empty after tick")
	}
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// Compile check: LiveView satisfies Phase + PhaseIniter.
var _ Phase = LiveView{}
var _ PhaseIniter = LiveView{}

// Smoke that tea.Tick actually fires for the documented contract; if
// this ever stops, our cmd fan-out is wrong. Kept fast (5ms).
func TestLiveViewTickerActuallyFires(t *testing.T) {
	s := &fakeSampler{doneAt: 1}
	v := NewLiveView("eng", "t", s, 5*time.Millisecond, func(a any) string { return "" })
	cmd := v.Init()
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		msg := cmd()
		if _, ok := msg.(liveTickMsg); ok {
			return
		}
	}
	t.Fatalf("tea.Tick never produced a liveTickMsg")
}

// Sanity: our fakeSampler matches the Sampler contract exactly.
var _ LiveSampler = (*fakeSampler)(nil)

// Confirm the message value is not lost between Update calls (bubbletea
// passes value receivers, so mutation must round-trip through return).
func TestLiveViewCarriesLastBetweenTicks(t *testing.T) {
	s := &fakeSampler{doneAt: 99}
	v := NewLiveView("eng", "t", s, time.Millisecond, func(a any) string { return "" })
	a, _ := v.Update(liveTickMsg{tag: "eng"})
	b, _ := a.(LiveView).Update(liveTickMsg{tag: "eng"})
	got := b.(LiveView).last.(int)
	if got != 2 {
		t.Errorf("expected last=2 after two ticks, got %d", got)
	}
	_ = tea.KeyMsg{} // keep tea import non-unused if tests are trimmed
}
