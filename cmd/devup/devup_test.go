package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaeawc/golang-build/internal/proc"
	"github.com/kaeawc/golang-build/internal/tui"
)

// stubFake returns a proc.Fake with a single rule for the docker
// compose call, so cmd/devup tests don't have to repeat boilerplate.
func stubFake(stdout string, exit int) *proc.Fake {
	return proc.NewFake().On(
		proc.MatchPrefix("docker", "compose"),
		proc.Response{Result: proc.Result{Stdout: []byte(stdout), ExitCode: exit}},
	)
}

func TestListComposeServicesParsesLines(t *testing.T) {
	r := stubFake("postgres\nvalkey\n  api  \n\n", 0)

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

func TestListComposeServicesPropagatesNonZeroExit(t *testing.T) {
	r := proc.NewFake().On(
		proc.MatchPrefix("docker", "compose"),
		proc.Response{Result: proc.Result{Stderr: []byte("no compose file"), ExitCode: 1}},
	)
	if _, err := listComposeServices(context.Background(), r); err == nil {
		t.Errorf("expected error for non-zero exit")
	}
}

func TestListComposeServicesPropagatesRunError(t *testing.T) {
	r := proc.NewFake().On(
		proc.MatchPrefix("docker", "compose"),
		proc.Response{Err: errors.New("docker not found")},
	)
	if _, err := listComposeServices(context.Background(), r); err == nil {
		t.Errorf("expected error from runner")
	}
}

func TestListComposeServicesEmpty(t *testing.T) {
	r := stubFake("", 0)
	if _, err := listComposeServices(context.Background(), r); err == nil {
		t.Errorf("expected error for empty service list")
	}
}

func TestComposeActionWiresArgsAndAppendsDetachOnUp(t *testing.T) {
	r := stubFake("ok\n", 0)

	if _, err := composeAction(context.Background(), r, "up", "postgres"); err != nil {
		t.Fatalf("composeAction: %v", err)
	}

	calls := r.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	want := []string{"compose", "up", "postgres", "-d"}
	if !slicesEqual(calls[0].Args, want) {
		t.Errorf("args = %v, want %v", calls[0].Args, want)
	}
}

func TestComposeActionPropagatesNonZeroExit(t *testing.T) {
	r := stubFake("boom", 2)
	out, err := composeAction(context.Background(), r, "stop", "postgres")
	if err == nil {
		t.Errorf("expected error")
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("expected stdout in output, got %q", out)
	}
}

// ---------- model flow -----------------------------------------------------

func TestModelFlowEndToEnd(t *testing.T) {
	r := stubFake("postgres\nvalkey\napi\n", 0)
	m := newModel(r)
	if cmd := m.Init(); cmd == nil {
		t.Fatalf("Init should return a cmd (the load task)")
	}

	services := []string{"postgres", "valkey", "api"}
	m = step(t, m, tui.TaskDoneMsg{Tag: tagLoadServices, Result: services})
	if _, ok := m.phase.(tui.Picker); !ok {
		t.Fatalf("expected Picker after services load, got %T", m.phase)
	}

	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickService, Index: 0})
	if m.service != "postgres" {
		t.Errorf("service = %q, want postgres", m.service)
	}

	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickAction, Index: 0})
	if m.action != "ps" {
		t.Errorf("action = %q, want ps", m.action)
	}

	m = step(t, m, tui.TaskDoneMsg{Tag: tagRunAction, Result: "Name        State\npostgres    Up\n"})
	if _, ok := m.phase.(tui.Done); !ok {
		t.Errorf("expected Done phase, got %T", m.phase)
	}
	if !strings.Contains(m.phase.View(), "postgres") {
		t.Errorf("Done view should mention service, got: %s", m.phase.View())
	}
}

func TestModelTaskErrorQuits(t *testing.T) {
	r := proc.NewFake()
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
	r := proc.NewFake()
	m := newModel(r)
	m = step(t, m, tui.TaskDoneMsg{Tag: tagLoadServices, Result: []string{}})
	if _, ok := m.phase.(tui.Done); !ok {
		t.Errorf("expected Done phase for empty services, got %T", m.phase)
	}
}

// ---------- headless -------------------------------------------------------

func TestRunHeadlessListPrintsServices(t *testing.T) {
	r := stubFake("postgres\nvalkey\n", 0)

	var buf bytes.Buffer
	if code := runHeadlessList(&buf, r); code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "postgres\nvalkey\n") {
		t.Errorf("missing services in output: %q", buf.String())
	}
}

func TestRunHeadlessActionRejectsBadAction(t *testing.T) {
	r := proc.NewFake()
	if code := runHeadlessAction(&bytes.Buffer{}, r, "wat", "postgres"); code == 0 {
		t.Errorf("expected non-zero for invalid action")
	}
}

func TestRunHeadlessActionHappyPath(t *testing.T) {
	r := stubFake("Recreated postgres\n", 0)

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

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
