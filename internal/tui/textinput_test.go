package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTextInputRejectsEmpty(t *testing.T) {
	ti := NewTextInput("name", "title", "prompt", "placeholder", nil)
	next, cmd := ti.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("expected no completion cmd for empty input")
	}
	if !strings.Contains(next.View(), "value is required") {
		t.Errorf("expected required-error in view, got:\n%s", next.View())
	}
}

func TestTextInputAcceptsValidValue(t *testing.T) {
	ti := NewTextInput("name", "title", "prompt", "placeholder", nil)
	// Type "foo" then enter.
	cur := typeText(ti, "foo")
	_, cmd := cur.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected completion cmd")
	}
	msg := cmd().(TextInputDoneMsg)
	if msg.Tag != "name" || msg.Value != "foo" {
		t.Errorf("unexpected msg: %+v", msg)
	}
}

func TestTextInputValidationFailure(t *testing.T) {
	ti := NewTextInput("name", "title", "prompt", "placeholder", func(s string) error {
		return errors.New("must start with x")
	})
	cur := typeText(ti, "yfoo")
	next, cmd := cur.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("expected no completion cmd on validation failure")
	}
	if !strings.Contains(next.View(), "must start with x") {
		t.Errorf("expected validation error in view")
	}
}

func TestTextInputValidationSuccess(t *testing.T) {
	called := false
	ti := NewTextInput("name", "t", "p", "ph", func(s string) error {
		called = true
		if s != "ok" {
			return errors.New("nope")
		}
		return nil
	})
	cur := typeText(ti, "ok")
	_, cmd := cur.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !called {
		t.Errorf("validate was not called")
	}
	if cmd == nil {
		t.Fatalf("expected completion cmd")
	}
}

func TestTextInputErrorClearedOnKeystroke(t *testing.T) {
	ti := NewTextInput("name", "t", "p", "ph", nil)
	// Trigger error.
	next, _ := ti.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cur := next.(TextInput)
	if cur.errMsg == "" {
		t.Fatalf("expected errMsg set")
	}
	// Any keystroke clears it.
	next2, _ := cur.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if next2.(TextInput).errMsg != "" {
		t.Errorf("errMsg should clear on keystroke")
	}
}

func TestTextInputErrorSurvivesNonKeyMessages(t *testing.T) {
	ti := NewTextInput("name", "t", "p", "ph", nil)
	next, _ := ti.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cur := next.(TextInput)
	// Blink ticks and terminal/window messages arrive without user input
	// and must not hide the validation error.
	for _, msg := range []tea.Msg{
		ti.Init()(),
		tea.WindowSizeMsg{Width: 80, Height: 24},
	} {
		n, _ := cur.Update(msg)
		cur = n.(TextInput)
		if cur.errMsg == "" {
			t.Fatalf("errMsg cleared by %T", msg)
		}
	}
}

func TestTextInputAcceptsQ(t *testing.T) {
	cur := typeText(NewTextInput("name", "t", "p", "ph", nil), "queue")
	if got := cur.input.Value(); got != "queue" {
		t.Errorf("value = %q, want %q", got, "queue")
	}
}

func TestTextInputTrimsWhitespace(t *testing.T) {
	ti := NewTextInput("name", "t", "p", "ph", nil)
	cur := typeText(ti, "  foo  ")
	_, cmd := cur.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected cmd")
	}
	if cmd().(TextInputDoneMsg).Value != "foo" {
		t.Errorf("expected trimmed value")
	}
}

// typeText feeds runes to the model one at a time.
func typeText(ti TextInput, s string) TextInput {
	cur := ti
	for _, r := range s {
		next, _ := cur.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		cur = next.(TextInput)
	}
	return cur
}

var (
	_ Phase       = TextInput{}
	_ PhaseIniter = TextInput{}
)
