package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaeawc/golang-build/internal/tui"
)

func TestFakeBackendListUsers(t *testing.T) {
	b := NewFakeBackend()
	users, err := b.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 3 {
		t.Errorf("expected 3 users, got %d", len(users))
	}
	// Mutating the returned slice must not affect the backend's data.
	users[0].Name = "mutated"
	again, _ := b.ListUsers(context.Background())
	if again[0].Name == "mutated" {
		t.Errorf("ListUsers leaked internal slice")
	}
}

func TestFakeBackendRunHealthchecksHasMix(t *testing.T) {
	b := NewFakeBackend()
	probes, err := b.RunHealthchecks(context.Background())
	if err != nil {
		t.Fatalf("RunHealthchecks: %v", err)
	}
	healthy, failing := 0, 0
	for _, p := range probes {
		if p.Healthy {
			healthy++
		} else {
			failing++
		}
	}
	if healthy == 0 || failing == 0 {
		t.Errorf("expected mix of healthy and failing probes, got %d/%d", healthy, failing)
	}
}

func TestFakeBackendPropagatesError(t *testing.T) {
	want := errors.New("connection refused")
	b := &FakeBackend{Err: want}
	if _, err := b.ListUsers(context.Background()); !errors.Is(err, want) {
		t.Errorf("ListUsers should propagate err, got %v", err)
	}
	if _, err := b.RunHealthchecks(context.Background()); !errors.Is(err, want) {
		t.Errorf("RunHealthchecks should propagate err, got %v", err)
	}
}

func TestFormatProbeRendersAllStates(t *testing.T) {
	cases := []struct {
		in   Probe
		want string
	}{
		{Probe{Name: "db", Healthy: true, Latency: 5 * time.Millisecond}, "db ok"},
		{Probe{Name: "x", Healthy: false, Latency: 1 * time.Second, Err: "timeout"}, "x FAIL"},
		{Probe{Name: "y", Healthy: false, Latency: 1 * time.Second, Err: "timeout"}, "timeout"},
	}
	for _, c := range cases {
		got := formatProbe(c.in)
		if !strings.Contains(got, c.want) {
			t.Errorf("formatProbe(%+v) = %q, want substring %q", c.in, got, c.want)
		}
	}
}

func TestModelFlowListUsersToDetail(t *testing.T) {
	b := NewFakeBackend()
	m := newModel(b)
	if cmd := m.Init(); cmd != nil {
		_ = cmd
	}

	// Pick "List users".
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickAction, Index: int(actListUsers)})

	// Simulate the load task completing.
	users, _ := b.ListUsers(context.Background())
	m = step(t, m, tui.TaskDoneMsg{Tag: tagLoadUsers, Result: users})

	if _, ok := m.phase.(tui.Picker); !ok {
		t.Fatalf("expected Picker phase after task done, got %T", m.phase)
	}

	// Pick the second user.
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickUser, Index: 1})
	if _, ok := m.phase.(tui.Done); !ok {
		t.Errorf("expected Done phase, got %T", m.phase)
	}
	if !strings.Contains(m.phase.View(), users[1].Name) {
		t.Errorf("Done view should mention selected user")
	}
}

func TestModelFlowHealthchecksToDetail(t *testing.T) {
	b := NewFakeBackend()
	m := newModel(b)
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickAction, Index: int(actHealthchecks)})

	probes, _ := b.RunHealthchecks(context.Background())
	m = step(t, m, tui.TaskDoneMsg{Tag: tagLoadProbes, Result: probes})

	// Select the failing probe (last in the canned dataset).
	idx := len(probes) - 1
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickProbe, Index: idx})

	view := m.phase.View()
	if !strings.Contains(view, probes[idx].Name) {
		t.Errorf("expected probe name in detail view")
	}
	if !strings.Contains(view, probes[idx].Err) {
		t.Errorf("expected probe error in detail view")
	}
}

func TestModelFlowEmptyResults(t *testing.T) {
	b := &FakeBackend{}
	m := newModel(b)
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickAction, Index: int(actListUsers)})
	m = step(t, m, tui.TaskDoneMsg{Tag: tagLoadUsers, Result: []User{}})
	if _, ok := m.phase.(tui.Done); !ok {
		t.Errorf("expected Done phase for empty result")
	}
}

func TestModelFlowQuitAction(t *testing.T) {
	b := NewFakeBackend()
	m := newModel(b)
	_, cmd := m.Update(tui.PickerDoneMsg{Tag: tagPickAction, Index: int(actQuit)})
	if cmd == nil {
		t.Fatalf("expected quit cmd")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("expected QuitMsg")
	}
}

func TestModelTaskErrorSurfacesAsError(t *testing.T) {
	b := NewFakeBackend()
	m := newModel(b)
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickAction, Index: int(actListUsers)})

	want := errors.New("boom")
	next, cmd := m.Update(tui.TaskDoneMsg{Tag: tagLoadUsers, Err: want})
	if cmd == nil {
		t.Fatalf("expected quit cmd on err")
	}
	if next.(model).err == nil || !strings.Contains(next.(model).err.Error(), "boom") {
		t.Errorf("expected error captured on model")
	}
}

func TestRunHeadlessListUsers(t *testing.T) {
	b := NewFakeBackend()
	var buf bytes.Buffer
	if code := runHeadlessListUsers(&buf, b); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(buf.String(), "alice") {
		t.Errorf("expected 'alice' in output, got: %s", buf.String())
	}
}

func TestRunHeadlessHealthchecks(t *testing.T) {
	b := NewFakeBackend()
	var buf bytes.Buffer
	if code := runHeadlessHealthchecks(&buf, b); code != 0 {
		t.Fatalf("exit %d", code)
	}
	out := buf.String()
	if !strings.Contains(out, "ok") || !strings.Contains(out, "FAIL") {
		t.Errorf("expected mix of ok/FAIL, got: %s", out)
	}
}

func TestBuildBackendForceFake(t *testing.T) {
	b, closer, err := buildBackend(true)
	defer closer()
	if err != nil {
		t.Fatalf("buildBackend: %v", err)
	}
	if _, ok := b.(*FakeBackend); !ok {
		t.Errorf("expected *FakeBackend, got %T", b)
	}
}

func step(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(model)
}
