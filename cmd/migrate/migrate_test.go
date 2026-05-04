package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaeawc/golang-build/internal/tui"
)

func TestScanMigrationsParsesAndSorts(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"000003_three.up.sql",
		"000003_three.down.sql",
		"000001_one.up.sql",
		"000001_one.down.sql",
		"000002_two.up.sql",
		"000002_two.down.sql",
		"README.md",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("--"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanMigrations(dir)
	if err != nil {
		t.Fatalf("scanMigrations: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d migrations, want 3", len(got))
	}
	for i, m := range got {
		if m.Version != uint(i+1) {
			t.Errorf("migrations[%d].Version = %d, want %d", i, m.Version, i+1)
		}
	}
	if got[0].Name != "one" || got[1].Name != "two" || got[2].Name != "three" {
		t.Errorf("names not parsed: %+v", got)
	}
}

func TestScanMigrationsMissingDir(t *testing.T) {
	if _, err := scanMigrations("/no/such/path"); err == nil {
		t.Errorf("expected error for missing dir")
	}
}

func TestFakeMigratorStatusReflectsApplied(t *testing.T) {
	available := []Migration{
		{Version: 1, Name: "one"},
		{Version: 2, Name: "two"},
		{Version: 3, Name: "three"},
	}
	f := NewFakeMigrator(available)

	st, _ := f.Status(context.Background())
	if st.HasVersion {
		t.Errorf("fresh migrator should report no version")
	}
	if len(st.Pending()) != 3 {
		t.Errorf("expected 3 pending, got %d", len(st.Pending()))
	}

	if err := f.UpOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, _ = f.Status(context.Background())
	if st.CurrentVersion != 1 {
		t.Errorf("after one up, version = %d, want 1", st.CurrentVersion)
	}
	if len(st.Pending()) != 2 {
		t.Errorf("after one up, pending = %d, want 2", len(st.Pending()))
	}
}

func TestFakeMigratorUpAllDownOneRoundTrip(t *testing.T) {
	available := []Migration{{Version: 1}, {Version: 2}, {Version: 3}}
	f := NewFakeMigrator(available)

	if err := f.UpAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, _ := f.Status(context.Background())
	if st.CurrentVersion != 3 {
		t.Errorf("after UpAll, version = %d, want 3", st.CurrentVersion)
	}

	if err := f.DownOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, _ = f.Status(context.Background())
	if st.CurrentVersion != 2 {
		t.Errorf("after DownOne, version = %d, want 2", st.CurrentVersion)
	}
}

func TestFakeMigratorFailNextIsConsumedOnce(t *testing.T) {
	f := NewFakeMigrator([]Migration{{Version: 1}})
	want := errors.New("simulated")
	f.FailNext(want)

	if err := f.UpAll(context.Background()); !errors.Is(err, want) {
		t.Errorf("expected injected error, got %v", err)
	}
	if err := f.UpAll(context.Background()); err != nil {
		t.Errorf("FailNext should fire only once, got %v", err)
	}
}

// ---------- model flow -----------------------------------------------------

func TestModelFlowApplyAll(t *testing.T) {
	f := NewFakeMigrator([]Migration{{Version: 1}, {Version: 2}})
	m := newModel(f)
	if cmd := m.Init(); cmd == nil {
		t.Fatalf("expected Init to return cmd (load status)")
	}

	// Status loads.
	st, _ := f.Status(context.Background())
	m = step(t, m, tui.TaskDoneMsg{Tag: tagLoadStatus, Result: st})
	if _, ok := m.phase.(tui.Picker); !ok {
		t.Fatalf("expected Picker, got %T", m.phase)
	}

	// Pick "Apply all pending".
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickAction, Index: int(actUpAll)})
	if _, ok := m.phase.(tui.Confirm); !ok {
		t.Fatalf("expected Confirm, got %T", m.phase)
	}

	// Confirm yes → run task fires; we synthesize the result inline by
	// calling the migrator directly first to advance Fake state.
	if err := f.UpAll(context.Background()); err != nil {
		t.Fatalf("UpAll: %v", err)
	}
	m = step(t, m, tui.ConfirmDoneMsg{Tag: tagConfirmRun, Yes: true})

	res := runResult{action: actUpAll, priorVer: 0, newVer: 2}
	m = step(t, m, tui.TaskDoneMsg{Tag: tagRunMig, Result: res})
	if _, ok := m.phase.(tui.Done); !ok {
		t.Errorf("expected Done, got %T", m.phase)
	}
	if !strings.Contains(m.phase.View(), "0 → 2") {
		t.Errorf("Done view should show version transition, got: %s", m.phase.View())
	}
}

func TestModelFlowConfirmNoLoopsBackToStatus(t *testing.T) {
	f := NewFakeMigrator([]Migration{{Version: 1}})
	m := newModel(f)
	st, _ := f.Status(context.Background())
	m = step(t, m, tui.TaskDoneMsg{Tag: tagLoadStatus, Result: st})
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickAction, Index: int(actUpAll)})

	// Confirm no.
	m = step(t, m, tui.ConfirmDoneMsg{Tag: tagConfirmRun, Yes: false})
	if _, ok := m.phase.(tui.AsyncTask); !ok {
		t.Errorf("expected AsyncTask (re-load status), got %T", m.phase)
	}
}

func TestModelFlowQuitAction(t *testing.T) {
	f := NewFakeMigrator(nil)
	m := newModel(f)
	st, _ := f.Status(context.Background())
	m = step(t, m, tui.TaskDoneMsg{Tag: tagLoadStatus, Result: st})

	_, cmd := m.Update(tui.PickerDoneMsg{Tag: tagPickAction, Index: int(actQuit)})
	if cmd == nil {
		t.Fatalf("expected quit cmd")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("expected QuitMsg")
	}
}

func TestModelFlowRefreshTriggersReload(t *testing.T) {
	f := NewFakeMigrator(nil)
	m := newModel(f)
	st, _ := f.Status(context.Background())
	m = step(t, m, tui.TaskDoneMsg{Tag: tagLoadStatus, Result: st})

	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickAction, Index: int(actRefresh)})
	if _, ok := m.phase.(tui.AsyncTask); !ok {
		t.Errorf("expected AsyncTask after refresh, got %T", m.phase)
	}
}

func TestModelTaskErrorQuits(t *testing.T) {
	f := NewFakeMigrator(nil)
	m := newModel(f)
	want := errors.New("db down")
	next, cmd := m.Update(tui.TaskDoneMsg{Tag: tagLoadStatus, Err: want})
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

// ---------- headless -------------------------------------------------------

func TestRunHeadlessStatusPrintsPending(t *testing.T) {
	f := NewFakeMigrator([]Migration{
		{Version: 1, Name: "one"},
		{Version: 2, Name: "two"},
	})
	var buf bytes.Buffer
	if code := runHeadlessStatus(&buf, f); code != 0 {
		t.Fatalf("exit %d", code)
	}
	out := buf.String()
	for _, want := range []string{"current version: (none)", "pending: 2", "one", "two"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRunHeadlessActionUpAllAdvances(t *testing.T) {
	f := NewFakeMigrator([]Migration{{Version: 1}, {Version: 2}})
	var buf bytes.Buffer
	if code := runHeadlessAction(&buf, f, actUpAll); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(buf.String(), "(none) → v2") {
		t.Errorf("expected version transition in output: %q", buf.String())
	}
}

func TestRunHeadlessActionPropagatesError(t *testing.T) {
	f := NewFakeMigrator([]Migration{{Version: 1}})
	f.FailNext(errors.New("schema lock"))
	if code := runHeadlessAction(&bytes.Buffer{}, f, actUpAll); code == 0 {
		t.Errorf("expected non-zero exit")
	}
}

func TestVersionLabelMatrix(t *testing.T) {
	cases := []struct {
		s    Status
		want string
	}{
		{Status{}, "(none)"},
		{Status{HasVersion: true, CurrentVersion: 7}, "v7"},
		{Status{HasVersion: true, CurrentVersion: 9, Dirty: true}, "v9 (DIRTY)"},
	}
	for _, c := range cases {
		if got := versionLabel(c.s); got != c.want {
			t.Errorf("versionLabel(%+v) = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestBuildMigratorRequiresDSN(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := buildMigrator("sql/migrations", false); err == nil {
		t.Errorf("expected error when DATABASE_URL unset")
	}
}

func TestBuildMigratorFakeMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "000001_x.up.sql"), []byte("--"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "000001_x.down.sql"), []byte("--"), 0o600); err != nil {
		t.Fatal(err)
	}
	mg, err := buildMigrator(dir, true)
	if err != nil {
		t.Fatalf("buildMigrator: %v", err)
	}
	if _, ok := mg.(*FakeMigrator); !ok {
		t.Errorf("expected *FakeMigrator, got %T", mg)
	}
}

func step(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(model)
}
