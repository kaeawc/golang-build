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

func TestFakeRunnerRecordsCallsAndReturnsCanned(t *testing.T) {
	r := NewFakeRunner()
	r.SetOutput("docker", "compose", "postgres\nvalkey\napi\n", nil)

	out, err := r.Run(context.Background(), "docker", "compose", "config", "--services")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "postgres") {
		t.Errorf("expected canned output, got %q", out)
	}

	calls := r.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Command != "docker" {
		t.Errorf("command = %q, want docker", calls[0].Command)
	}
	if len(calls[0].Args) != 3 || calls[0].Args[0] != "compose" || calls[0].Args[2] != "--services" {
		t.Errorf("args = %v", calls[0].Args)
	}
}

func TestFakeRunnerErrorsForUnstubbed(t *testing.T) {
	r := NewFakeRunner()
	if _, err := r.Run(context.Background(), "echo", "hi"); err == nil {
		t.Errorf("expected error for unstubbed call")
	}
}

func TestFakeRunnerHonorsContextCancel(t *testing.T) {
	r := NewFakeRunner()
	r.SetOutput("docker", "", "ok", nil)
	r.SetDelay(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := r.Run(ctx, "docker", "ps")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

func TestListComposeServicesParsesLines(t *testing.T) {
	r := NewFakeRunner()
	r.SetOutput("docker", "compose", "postgres\nvalkey\n  api  \n\n", nil)

	got, err := listComposeServices(context.Background(), r)
	if err != nil {
		t.Fatalf("listComposeServices: %v", err)
	}
	want := []string{"postgres", "valkey", "api"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("service[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestListComposeServicesPropagatesError(t *testing.T) {
	r := NewFakeRunner()
	r.SetOutput("docker", "compose", "no compose file", errors.New("exit 1"))

	if _, err := listComposeServices(context.Background(), r); err == nil {
		t.Errorf("expected error")
	}
}

func TestListComposeServicesEmpty(t *testing.T) {
	r := NewFakeRunner()
	r.SetOutput("docker", "compose", "", nil)
	if _, err := listComposeServices(context.Background(), r); err == nil {
		t.Errorf("expected error for empty service list")
	}
}

func TestComposeActionWiresArgs(t *testing.T) {
	r := NewFakeRunner()
	r.SetOutput("docker", "compose", "ok", nil)

	if _, err := composeAction(context.Background(), r, "up", "postgres"); err != nil {
		t.Fatalf("composeAction: %v", err)
	}

	calls := r.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	args := calls[0].Args
	// up gets a -d appended
	if args[0] != "compose" || args[1] != "up" || args[2] != "postgres" || args[3] != "-d" {
		t.Errorf("up args wrong: %v", args)
	}
}

func TestComposeActionPropagatesError(t *testing.T) {
	r := NewFakeRunner()
	r.SetOutput("docker", "compose", "boom", errors.New("exit 2"))
	_, err := composeAction(context.Background(), r, "stop", "postgres")
	if err == nil {
		t.Errorf("expected error")
	}
}

// ---------- model flow -----------------------------------------------------

func TestModelFlowEndToEnd(t *testing.T) {
	r := NewFakeRunner()
	r.SetOutput("docker", "compose", "postgres\nvalkey\napi\n", nil)
	m := newModel(r)
	if cmd := m.Init(); cmd == nil {
		t.Fatalf("Init should return a cmd (the load task)")
	}

	// Simulate the load-services task completing.
	services := []string{"postgres", "valkey", "api"}
	m = step(t, m, tui.TaskDoneMsg{Tag: tagLoadServices, Result: services})
	if _, ok := m.phase.(tui.Picker); !ok {
		t.Fatalf("expected Picker after services load, got %T", m.phase)
	}

	// Pick the first service.
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickService, Index: 0})
	if m.service != "postgres" {
		t.Errorf("service = %q, want postgres", m.service)
	}

	// Pick an action ("ps" is index 0).
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickAction, Index: 0})
	if m.action != "ps" {
		t.Errorf("action = %q, want ps", m.action)
	}

	// Simulate the action-run task completing.
	m = step(t, m, tui.TaskDoneMsg{Tag: tagRunAction, Result: "Name        State\npostgres    Up\n"})
	if _, ok := m.phase.(tui.Done); !ok {
		t.Errorf("expected Done phase, got %T", m.phase)
	}
	if !strings.Contains(m.phase.View(), "postgres") {
		t.Errorf("Done view should mention service, got: %s", m.phase.View())
	}
}

func TestModelTaskErrorQuits(t *testing.T) {
	r := NewFakeRunner()
	m := newModel(r)
	want := errors.New("docker not running")
	next, cmd := m.Update(tui.TaskDoneMsg{Tag: tagLoadServices, Err: want})
	if cmd == nil {
		t.Fatalf("expected quit cmd")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("expected QuitMsg")
	}
	if next.(model).err == nil {
		t.Errorf("expected err captured")
	}
}

func TestModelEmptyServicesFallsThroughToDone(t *testing.T) {
	r := NewFakeRunner()
	m := newModel(r)
	m = step(t, m, tui.TaskDoneMsg{Tag: tagLoadServices, Result: []string{}})
	if _, ok := m.phase.(tui.Done); !ok {
		t.Errorf("expected Done phase for empty services, got %T", m.phase)
	}
}

// ---------- headless -------------------------------------------------------

func TestRunHeadlessListPrintsServices(t *testing.T) {
	r := NewFakeRunner()
	r.SetOutput("docker", "compose", "postgres\nvalkey\n", nil)

	var buf bytes.Buffer
	if code := runHeadlessList(&buf, r); code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "postgres\nvalkey\n") {
		t.Errorf("missing services in output: %q", buf.String())
	}
}

func TestRunHeadlessActionRejectsBadAction(t *testing.T) {
	r := NewFakeRunner()
	if code := runHeadlessAction(&bytes.Buffer{}, r, "wat", "postgres"); code == 0 {
		t.Errorf("expected non-zero for invalid action")
	}
}

func TestRunHeadlessActionHappyPath(t *testing.T) {
	r := NewFakeRunner()
	r.SetOutput("docker", "compose", "Recreated postgres\n", nil)

	var buf bytes.Buffer
	if code := runHeadlessAction(&buf, r, "restart", "postgres"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(buf.String(), "Recreated postgres") {
		t.Errorf("missing output: %q", buf.String())
	}
}

func TestValidActionMatrix(t *testing.T) {
	for _, a := range composeActions {
		if !validAction(a) {
			t.Errorf("validAction(%q) should be true", a)
		}
	}
	if validAction("destroy") {
		t.Errorf("validAction(destroy) should be false")
	}
}

func step(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(model)
}
