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

func TestValidateNameAccepts(t *testing.T) {
	for _, name := range []string{"users", "rate_limit", "x", "x1", "abc123"} {
		if err := validateName(name); err != nil {
			t.Errorf("validateName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateNameRejects(t *testing.T) {
	cases := []string{"", "Foo", "1abc", "with space", "with-dash", "_leading", "main", "package", "go"}
	for _, name := range cases {
		if err := validateName(name); err == nil {
			t.Errorf("validateName(%q) = nil, want error", name)
		}
	}
}

func TestSpecPath(t *testing.T) {
	cases := map[Kind]string{
		KindHandler:    "internal/handlers/users.go",
		KindMiddleware: "internal/middleware/users.go",
		KindPackage:    "internal/users/users.go",
	}
	for kind, want := range cases {
		got := Spec{Kind: kind, Name: "users"}.Path()
		if got != want {
			t.Errorf("Path(%s) = %s, want %s", kind, got, want)
		}
	}
}

func TestRenderHandlerHasExpectedShape(t *testing.T) {
	body := Spec{Kind: KindHandler, Name: "widgets"}.Render()
	for _, want := range []string{
		"package handlers",
		"func Widgets() http.HandlerFunc",
		"http.StatusNoContent",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestRenderMiddlewareHasExpectedShape(t *testing.T) {
	body := Spec{Kind: KindMiddleware, Name: "auth"}.Render()
	for _, want := range []string{
		"package middleware",
		"func Auth(next http.Handler) http.Handler",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestRenderPackageHasExpectedShape(t *testing.T) {
	body := Spec{Kind: KindPackage, Name: "queue"}.Render()
	for _, want := range []string{"// Package queue", "package queue"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestWriteCreatesFileAndDirectories(t *testing.T) {
	dir := t.TempDir()
	spec := Spec{Kind: KindPackage, Name: "myq"}
	path, err := write(dir, spec)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if path != filepath.Join(dir, "internal/myq/myq.go") {
		t.Errorf("unexpected path: %s", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "package myq") {
		t.Errorf("file body wrong:\n%s", body)
	}
}

func TestWriteRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	spec := Spec{Kind: KindHandler, Name: "users"}
	if _, err := write(dir, spec); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	if _, err := write(dir, spec); err == nil {
		t.Errorf("expected overwrite refusal")
	}
}

func TestRunHeadlessHappyPath(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	code := runHeadless(&buf, dir, "handler", "widgets")
	if code != 0 {
		t.Fatalf("exit %d, output: %s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "wrote ") {
		t.Errorf("expected 'wrote ' in output")
	}
	if _, err := os.Stat(filepath.Join(dir, "internal/handlers/widgets.go")); err != nil {
		t.Errorf("expected file: %v", err)
	}
}

func TestRunHeadlessRejectsBadKind(t *testing.T) {
	dir := t.TempDir()
	code := runHeadless(&bytes.Buffer{}, dir, "router", "widgets")
	if code == 0 {
		t.Errorf("expected non-zero exit for bad kind")
	}
}

func TestRunHeadlessRejectsBadName(t *testing.T) {
	dir := t.TempDir()
	code := runHeadless(&bytes.Buffer{}, dir, "handler", "Bad-Name")
	if code == 0 {
		t.Errorf("expected non-zero exit for bad name")
	}
}

func TestModelFlowEndToEnd(t *testing.T) {
	dir := t.TempDir()
	m := newModel(dir)
	if cmd := m.Init(); cmd != nil {
		_ = cmd
	}

	// Pick handler.
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickKind, Index: 0})
	if m.spec.Kind != KindHandler {
		t.Fatalf("kind = %s, want handler", m.spec.Kind)
	}

	// Submit name.
	m = step(t, m, tui.TextInputDoneMsg{Tag: tagInputName, Value: "things"})
	if m.spec.Name != "things" {
		t.Fatalf("name = %s, want things", m.spec.Name)
	}

	// Confirm yes.
	m = step(t, m, tui.ConfirmDoneMsg{Tag: tagConfirm, Yes: true})

	// Simulate the write task completing (since Init kicked off the worker).
	wrote, err := write(dir, m.spec)
	if err != nil {
		// Possibly already written by the actual goroutine — both are fine.
		t.Logf("inline write: %v", err)
	}
	if wrote == "" {
		wrote = filepath.Join(dir, "internal/handlers/things.go")
	}
	m = step(t, m, tui.TaskDoneMsg{Tag: tagWrite, Result: wrote, Err: nil})

	if _, ok := m.phase.(tui.Done); !ok {
		t.Errorf("expected Done phase, got %T", m.phase)
	}
	if m.wrote == "" {
		t.Errorf("wrote not captured")
	}
}

func TestModelCancelsOnConfirmNo(t *testing.T) {
	dir := t.TempDir()
	m := newModel(dir)
	m = step(t, m, tui.PickerDoneMsg{Tag: tagPickKind, Index: 1})
	m = step(t, m, tui.TextInputDoneMsg{Tag: tagInputName, Value: "nothing"})
	m = step(t, m, tui.ConfirmDoneMsg{Tag: tagConfirm, Yes: false})
	if _, ok := m.phase.(tui.Done); !ok {
		t.Errorf("expected Done phase after cancel")
	}
	if _, err := os.Stat(filepath.Join(dir, "internal/middleware/nothing.go")); err == nil {
		t.Errorf("file should not exist after cancel")
	}
}

func step(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(model)
}
