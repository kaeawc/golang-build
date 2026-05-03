package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaeawc/golang-build/internal/tui"
)

func TestModelFlowEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := newModel(srv.URL)
	if cmd := m.Init(); cmd != nil {
		_ = cmd
	}

	// Step 1: pick concurrency 0 (low → 4 workers)
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickConcurrency, Index: 0})
	if m.cfg.Concurrency != 4 {
		t.Fatalf("concurrency = %d, want 4", m.cfg.Concurrency)
	}

	// Step 2: pick duration 0 (5s) — but override to a tiny value to keep
	// the test fast. We can't override after the picker fires because the
	// engine is started inline; use a follow-up shortcut: simulate the
	// LiveDoneMsg directly with a synthetic snapshot.
	//
	// First, confirm the picker fires the dashboard transition.
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickDuration, Index: 0})
	if m.engine == nil {
		t.Fatalf("expected engine to be started")
	}
	if _, ok := m.phase.(tui.LiveView); !ok {
		t.Fatalf("expected LiveView phase, got %T", m.phase)
	}

	// Cancel the engine immediately so the test doesn't take 5s.
	m.cancel()

	// Step 3: simulate the dashboard finishing.
	final := Snapshot{Total: 10, Errors: 0, RPS: 100, Done: true}
	m = step(t, m, tui.LiveDoneMsg{Tag: tagDashboard, Final: final})
	if _, ok := m.phase.(tui.Done); !ok {
		t.Errorf("expected Done phase, got %T", m.phase)
	}
	if m.final.Total != 10 {
		t.Errorf("final not captured: %+v", m.final)
	}
}

func TestModelGlobalQuitCancelsEngine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := newModel(srv.URL)
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickConcurrency, Index: 0})
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickDuration, Index: 2}) // 30s

	// Send 'q'. The model should call cancel and emit tea.Quit.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatalf("expected quit cmd")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("expected QuitMsg")
	}
	_ = next

	// Verify the engine actually stopped within a reasonable window.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m.engine.Snapshot().Done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("engine did not stop after global quit")
}

func step(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(model)
}
