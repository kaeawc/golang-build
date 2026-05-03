package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaeawc/golang-build/internal/tui"
)

func TestHeadlessUnknownPreset(t *testing.T) {
	dir := t.TempDir()
	_, err := runHeadless(&bytes.Buffer{}, dir, "nope")
	if err == nil {
		t.Fatalf("expected error for unknown preset")
	}
}

func TestHeadlessStandardWritesEnv(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	path, err := runHeadless(&buf, dir, "standard")
	if err != nil {
		t.Fatalf("runHeadless: %v", err)
	}
	if path != filepath.Join(dir, ".env") {
		t.Errorf("unexpected path: %s", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	want := []string{
		"PORT=8080",
		"LOG_LEVEL=info",
		"ENABLE_WEBSOCKETS=false",
		"DATABASE_URL=postgres://",
		"VALKEY_ADDR=localhost:6379",
	}
	for _, w := range want {
		if !strings.Contains(string(body), w) {
			t.Errorf("expected %q in .env, got:\n%s", w, body)
		}
	}
}

func TestHeadlessMinimalCommentsOutDatastores(t *testing.T) {
	dir := t.TempDir()
	if _, err := runHeadless(&bytes.Buffer{}, dir, "minimal"); err != nil {
		t.Fatalf("runHeadless: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".env"))
	for _, line := range []string{"# DATABASE_URL=", "# VALKEY_ADDR="} {
		if !strings.Contains(string(body), line) {
			t.Errorf("expected commented %q, got:\n%s", line, body)
		}
	}
}

// TestModelFlowEndToEnd drives the wizard through the in-process API:
// pick the "minimal" preset, decline websockets, confirm the write, and
// check the final state. This exercises every transition without a TTY.
func TestModelFlowEndToEnd(t *testing.T) {
	dir := t.TempDir()
	m := newModel(dir)
	if cmd := m.Init(); cmd != nil {
		// Picker has no Init; nothing to do.
		_ = cmd
	}

	// Step 1: pick the first preset (minimal).
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPresetPick, Index: 0, Item: tui.PickerItem{Label: "minimal"}})
	if m.preset.Name != "minimal" {
		t.Fatalf("preset = %s, want minimal", m.preset.Name)
	}

	// Step 2: confirm websockets off.
	m = step(t, m, tui.ConfirmDoneMsg{Tag: tagWebSockets, Yes: false})
	if m.cfg.WebSockets {
		t.Errorf("websockets should be off")
	}

	// Step 3: write task should run on Init; simulate its TaskDoneMsg
	// directly by invoking the worker we know is queued.
	wrote, err := writeConfig(dir, m.cfg)
	if err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	m = step(t, m, tui.TaskDoneMsg{Tag: tagWriteFile, Result: wrote, Err: nil})
	if !m.done || m.wrotePath == "" {
		t.Errorf("expected done, got %+v", m)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); err != nil {
		t.Errorf(".env not written: %v", err)
	}
}

func step(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(model)
}
