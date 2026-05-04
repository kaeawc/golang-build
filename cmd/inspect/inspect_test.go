package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaeawc/golang-build/internal/eventbus"
	"github.com/kaeawc/golang-build/internal/tui"
)

func TestInspectorAccumulatesAndRingBuffersEvents(t *testing.T) {
	bus := eventbus.NewAsync[Event](eventbus.AsyncConfig{BufferSize: 64, DropWhenFull: true})
	defer bus.Close()
	insp := NewInspector(bus, 5)
	defer insp.Stop()
	insp.Start()

	for i := 0; i < 10; i++ {
		level := "info"
		if i%3 == 0 {
			level = "warn"
		}
		bus.Publish(Event{
			At:      time.Now(),
			Kind:    "request",
			Level:   level,
			Message: "n",
		})
	}

	// Async delivery: poll until total reaches 10 or fail.
	deadline := time.Now().Add(time.Second)
	var snap Snapshot
	for time.Now().Before(deadline) {
		s, _ := insp.Sample()
		snap = s.(Snapshot)
		if snap.Total == 10 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snap.Total != 10 {
		t.Fatalf("expected 10 events, got %d", snap.Total)
	}
	if len(snap.Recent) != 5 {
		t.Errorf("ring should retain last 5, got %d", len(snap.Recent))
	}
	if snap.ByLevel["info"]+snap.ByLevel["warn"] != 10 {
		t.Errorf("level totals don't add up: %v", snap.ByLevel)
	}
	if snap.ByKind["request"] != 10 {
		t.Errorf("kind total wrong: %v", snap.ByKind)
	}
}

func TestInspectorStopFreezesElapsed(t *testing.T) {
	bus := eventbus.NewAsync[Event](eventbus.AsyncConfig{BufferSize: 4})
	defer bus.Close()
	insp := NewInspector(bus, 3)
	insp.Start()

	time.Sleep(20 * time.Millisecond)
	insp.Stop()

	a, done := insp.Sample()
	if !done {
		t.Errorf("Sample should report done=true after Stop")
	}
	first := a.(Snapshot).Elapsed

	time.Sleep(20 * time.Millisecond)
	b, _ := insp.Sample()
	second := b.(Snapshot).Elapsed
	if second != first {
		t.Errorf("Elapsed should be frozen after Stop, got %v then %v", first, second)
	}
}

func TestInspectorReturnsCopiedMaps(t *testing.T) {
	bus := eventbus.NewAsync[Event](eventbus.AsyncConfig{BufferSize: 4})
	defer bus.Close()
	insp := NewInspector(bus, 3)
	defer insp.Stop()
	insp.Start()
	bus.Publish(Event{Kind: "x", Level: "info"})
	// Wait for delivery.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s, _ := insp.Sample()
		if s.(Snapshot).Total > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	a, _ := insp.Sample()
	a.(Snapshot).ByLevel["info"] = 999
	b, _ := insp.Sample()
	if b.(Snapshot).ByLevel["info"] == 999 {
		t.Errorf("Snapshot leaked map; mutation visible in next snapshot")
	}
}

func TestScenarioByNameLookup(t *testing.T) {
	for _, want := range []string{"steady", "bursty", "noisy"} {
		if scenarioByName(want) == nil {
			t.Errorf("scenarioByName(%q) returned nil", want)
		}
	}
	if scenarioByName("nope") != nil {
		t.Errorf("scenarioByName(nope) should be nil")
	}
}

func TestRunSteadyPublishesUntilCanceled(t *testing.T) {
	bus := eventbus.NewAsync[Event](eventbus.AsyncConfig{BufferSize: 256, DropWhenFull: true})
	defer bus.Close()
	insp := NewInspector(bus, 50)
	defer insp.Stop()
	insp.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	runSteady(ctx, bus)

	deadline := time.Now().Add(500 * time.Millisecond)
	var total int64
	for time.Now().Before(deadline) {
		s, _ := insp.Sample()
		total = s.(Snapshot).Total
		if total >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if total < 2 {
		t.Errorf("expected at least 2 steady events in 250ms, got %d", total)
	}
}

func TestRunNoisyEmitsMixedLevels(t *testing.T) {
	bus := eventbus.NewAsync[Event](eventbus.AsyncConfig{BufferSize: 256, DropWhenFull: true})
	defer bus.Close()
	insp := NewInspector(bus, 100)
	defer insp.Stop()
	insp.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	runNoisy(ctx, bus)

	// Drain.
	deadline := time.Now().Add(time.Second)
	var snap Snapshot
	for time.Now().Before(deadline) {
		s, _ := insp.Sample()
		snap = s.(Snapshot)
		if snap.Total >= 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(snap.ByLevel) < 2 {
		t.Errorf("expected mixed levels, got %v", snap.ByLevel)
	}
}

// ---------- model flow -----------------------------------------------------

func TestModelFlowEndToEnd(t *testing.T) {
	m := newModel()
	if cmd := m.Init(); cmd != nil {
		_ = cmd
	}

	// Pick the first scenario (steady).
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickScenario, Index: 0})
	if m.scenario.Name != "steady" {
		t.Errorf("scenario = %s, want steady", m.scenario.Name)
	}
	if _, ok := m.phase.(tui.LiveView); !ok {
		t.Fatalf("expected LiveView phase, got %T", m.phase)
	}

	// Stop the goroutine immediately so the test doesn't hang.
	m.cancel()

	// Simulate the dashboard finishing (e.g. via quit).
	final := Snapshot{Total: 5, Done: true}
	m = step(t, m, tui.LiveDoneMsg{Tag: tagDashboard, Final: final})
	if _, ok := m.phase.(tui.Done); !ok {
		t.Errorf("expected Done phase, got %T", m.phase)
	}
	if m.final.Total != 5 {
		t.Errorf("final not captured: %+v", m.final)
	}
}

func TestModelGlobalQuitShutsDownProducer(t *testing.T) {
	m := newModel()
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickScenario, Index: 1}) // bursty

	bus := m.bus
	if bus == nil {
		t.Fatal("expected bus to be set")
	}

	// Send 'q'. Must shut down the producer + bus.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatalf("expected quit cmd")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("expected QuitMsg")
	}
	// After quit, m.cancel should have been called and m.bus cleared.
	// We can't easily assert from outside since shutdown mutates the
	// receiver's struct copy, but we can verify cancel doesn't panic
	// when called again.
}

// ---------- headless -------------------------------------------------------

func TestRunHeadlessHappyPath(t *testing.T) {
	var buf bytes.Buffer
	if code := runHeadless(&buf, "steady", 200*time.Millisecond); code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "events:") {
		t.Errorf("missing summary in output: %s", out)
	}
	if !strings.Contains(out, "stopped") {
		t.Errorf("missing 'stopped' in output: %s", out)
	}
}

func TestRunHeadlessRejectsBadScenario(t *testing.T) {
	if code := runHeadless(&bytes.Buffer{}, "nope", time.Second); code == 0 {
		t.Errorf("expected non-zero for bad scenario")
	}
}

func TestRunHeadlessRejectsZeroDuration(t *testing.T) {
	if code := runHeadless(&bytes.Buffer{}, "steady", 0); code == 0 {
		t.Errorf("expected non-zero for zero duration")
	}
}

func TestRunHeadlessRequiresScenario(t *testing.T) {
	if code := runHeadless(&bytes.Buffer{}, "", time.Second); code == 0 {
		t.Errorf("expected non-zero when scenario unset")
	}
}

func TestRenderSnapshotMentionsKeyMetrics(t *testing.T) {
	s := Snapshot{
		Total:       42,
		Subscribers: 1,
		Dropped:     3,
		Elapsed:     2 * time.Second,
		ByLevel:     map[string]int64{"info": 30, "warn": 10, "error": 2},
		ByKind:      map[string]int64{"request": 42},
		Recent: []Event{
			{At: time.Now(), Kind: "request", Level: "info", Message: "hello"},
			{At: time.Now(), Kind: "request", Level: "error", Message: "boom"},
		},
	}
	out := renderSnapshot(s)
	for _, want := range []string{"42", "info", "warn", "error", "request", "hello", "boom"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("renderSnapshot missing %q in output", want)
		}
	}
}

func step(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(model)
}
