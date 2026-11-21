package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestPickerArrowSelectsItem(t *testing.T) {
	p := NewPicker("preset", "pick", nil, []PickerItem{
		{Label: "a"}, {Label: "b"}, {Label: "c"},
	})
	p2, _ := p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	p3, cmd := p2.(Picker).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, ok := p3.(Picker); !ok {
		t.Fatalf("phase should remain a Picker until cmd is consumed")
	}
	if cmd == nil {
		t.Fatalf("expected completion cmd on enter")
	}
	msg := cmd()
	done, ok := msg.(PickerDoneMsg)
	if !ok {
		t.Fatalf("expected PickerDoneMsg, got %T", msg)
	}
	if done.Tag != "preset" || done.Index != 1 || done.Item.Label != "b" {
		t.Errorf("unexpected msg: %+v", done)
	}
}

func TestPickerCannotMovePastEdges(t *testing.T) {
	p := NewPicker("t", "pick", nil, []PickerItem{{Label: "x"}, {Label: "y"}})
	for i := 0; i < 5; i++ {
		next, _ := p.Update(tea.KeyPressMsg{Code: tea.KeyUp})
		p = next.(Picker)
	}
	// Should be at index 0; selecting yields x.
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := cmd().(PickerDoneMsg)
	if got.Index != 0 || got.Item.Label != "x" {
		t.Errorf("expected first item, got %+v", got)
	}
}

func TestConfirmDefaultAndOverride(t *testing.T) {
	c := NewConfirm("ws", "title", "desc", true)
	// Default is yes; pressing enter immediately confirms yes.
	_, cmd := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := cmd().(ConfirmDoneMsg); !got.Yes || got.Tag != "ws" {
		t.Errorf("expected yes, got %+v", got)
	}

	// Now pick "no" by typing 'n' then enter.
	c2 := NewConfirm("ws", "t", "d", true)
	c2a, _ := c2.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	_, cmd2 := c2a.(Confirm).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := cmd2().(ConfirmDoneMsg); got.Yes {
		t.Errorf("expected no, got %+v", got)
	}
}

// Bubble Tea v2 reports the space bar as "space" rather than " ".
func TestPickerAndConfirmSelectOnSpace(t *testing.T) {
	space := tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}

	p := NewPicker("t", "pick", nil, []PickerItem{{Label: "x"}})
	_, cmd := p.Update(space)
	if cmd == nil {
		t.Fatalf("expected picker completion cmd on space")
	}
	if got := cmd().(PickerDoneMsg); got.Item.Label != "x" {
		t.Errorf("unexpected picker msg: %+v", got)
	}

	c := NewConfirm("c", "title", "", false)
	_, cmd = c.Update(space)
	if cmd == nil {
		t.Fatalf("expected confirm completion cmd on space")
	}
	if got := cmd().(ConfirmDoneMsg); got.Yes {
		t.Errorf("expected no, got %+v", got)
	}
}

type stubModel struct{ n int }

func (s stubModel) Init() tea.Cmd { return nil }
func (s stubModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	s.n++
	return s, nil
}
func (s stubModel) View() tea.View { return tea.NewView("stub") }

func TestAltScreenWrapperSetsAltScreen(t *testing.T) {
	var m tea.Model = altScreen{stubModel{}}
	m, _ = m.Update(nil)
	a, ok := m.(altScreen)
	if !ok {
		t.Fatalf("Update should keep the wrapper, got %T", m)
	}
	if a.Model.(stubModel).n != 1 {
		t.Errorf("inner model not updated")
	}
	v := a.View()
	if !v.AltScreen || v.Content != "stub" {
		t.Errorf("unexpected view: altscreen=%v content=%q", v.AltScreen, v.Content)
	}
}

func TestAsyncTaskRunsAndEmitsResult(t *testing.T) {
	called := false
	task := NewAsyncTask("write", "title", "label", func() (any, error) {
		called = true
		return "ok", nil
	})
	cmd := task.Init()
	if cmd == nil {
		t.Fatalf("Init should return a cmd")
	}
	// tea.Batch returns a single command that fans out; collect by
	// pulling messages until we see TaskDoneMsg.
	deadline := time.Now().Add(time.Second)
	var done TaskDoneMsg
	found := false
	for time.Now().Before(deadline) && !found {
		msg := cmd()
		switch m := msg.(type) {
		case TaskDoneMsg:
			done = m
			found = true
		case tea.BatchMsg:
			for _, c := range m {
				if c == nil {
					continue
				}
				if d, ok := c().(TaskDoneMsg); ok {
					done = d
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Fatalf("did not observe TaskDoneMsg")
	}
	if !called {
		t.Errorf("worker fn was not invoked")
	}
	if done.Tag != "write" || done.Err != nil || done.Result != "ok" {
		t.Errorf("unexpected msg: %+v", done)
	}
}

func TestAsyncTaskPropagatesError(t *testing.T) {
	want := errors.New("boom")
	task := NewAsyncTask("t", "title", "label", func() (any, error) {
		return nil, want
	})
	batch, ok := task.Init()().(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected BatchMsg, got %T", task.Init()())
	}
	for _, c := range batch {
		if c == nil {
			continue
		}
		if d, ok := c().(TaskDoneMsg); ok {
			if !errors.Is(d.Err, want) {
				t.Errorf("expected %v, got %v", want, d.Err)
			}
			return
		}
	}
	t.Fatalf("no TaskDoneMsg observed")
}

func TestHandleGlobalQuitsOnQ(t *testing.T) {
	cmd := HandleGlobal(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatalf("expected quit cmd")
	}
	// tea.Quit is a function; verify it returns a tea.QuitMsg.
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("expected QuitMsg")
	}
}

func TestHandleGlobalIgnoresOtherKeys(t *testing.T) {
	if cmd := HandleGlobal(tea.KeyPressMsg{Code: 'a', Text: "a"}); cmd != nil {
		t.Errorf("expected nil cmd for non-quit key")
	}
}

func TestRenderTableHighlightsRow(t *testing.T) {
	out := RenderTable(
		[]string{"Name", "Value"},
		[][]string{{"a", "1"}, {"b", "2"}},
		1,
	)
	if !strings.Contains(out, "▸ b") {
		t.Errorf("expected highlight marker on row 1, got:\n%s", out)
	}
}
