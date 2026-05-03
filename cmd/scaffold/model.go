package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaeawc/golang-build/internal/tui"
)

const (
	tagPickKind  = "kind"
	tagInputName = "name"
	tagConfirm   = "confirm"
	tagWrite     = "write"
)

type model struct {
	repoRoot string
	spec     Spec
	wrote    string

	phase tui.Phase
	err   error
}

func newModel(repoRoot string) model {
	m := model{repoRoot: repoRoot}
	m.phase = m.kindPickerPhase()
	return m
}

func (m model) Init() tea.Cmd { return tui.PhaseInit(m.phase) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd := tui.HandleGlobal(msg); cmd != nil {
		return m, cmd
	}

	switch msg := msg.(type) {
	case tui.PickerDoneMsg:
		return m.onPicker(msg)
	case tui.TextInputDoneMsg:
		return m.onTextInput(msg)
	case tui.ConfirmDoneMsg:
		return m.onConfirm(msg)
	case tui.TaskDoneMsg:
		return m.onTaskDone(msg)
	}

	next, cmd := m.phase.Update(msg)
	m.phase = next
	return m, cmd
}

func (m model) View() string {
	if m.err != nil {
		return tui.ErrorStyle.Render("error: ") + m.err.Error() + "\n"
	}
	return m.phase.View()
}

// ---------- transitions ----------------------------------------------------

func (m model) onPicker(msg tui.PickerDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Tag != tagPickKind {
		return m, nil
	}
	m.spec.Kind = Kinds[msg.Index]
	next := m.namePhase()
	m.phase = next
	return m, tui.PhaseInit(next)
}

func (m model) onTextInput(msg tui.TextInputDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Tag != tagInputName {
		return m, nil
	}
	m.spec.Name = msg.Value
	next := m.confirmPhase()
	m.phase = next
	return m, tui.PhaseInit(next)
}

func (m model) onConfirm(msg tui.ConfirmDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Tag != tagConfirm {
		return m, nil
	}
	if !msg.Yes {
		m.phase = tui.NewDone("scaffold — cancelled", tui.DimStyle.Render("nothing written"))
		return m, nil
	}
	next := m.writePhase()
	m.phase = next
	return m, tui.PhaseInit(next)
}

func (m model) onTaskDone(msg tui.TaskDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Tag != tagWrite {
		return m, nil
	}
	if msg.Err != nil {
		m.err = msg.Err
		return m, tea.Quit
	}
	m.wrote = msg.Result.(string)
	m.phase = m.donePhase()
	return m, nil
}

// ---------- phase factories ------------------------------------------------

func (m model) kindPickerPhase() tui.Phase {
	items := make([]tui.PickerItem, len(Kinds))
	for i, k := range Kinds {
		items[i] = tui.PickerItem{Label: string(k)}
	}
	return tui.NewPicker(tagPickKind, "scaffold — what to add", nil, items)
}

func (m model) namePhase() tui.Phase {
	prompt := tui.DimStyle.Render(
		"lowercase, [a-z0-9_], starts with a letter (e.g. \"users\", \"ratelimit\")")
	return tui.NewTextInput(tagInputName, "scaffold — name", prompt, "name", validateName)
}

func (m model) confirmPhase() tui.Phase {
	desc := fmt.Sprintf("Will write %s\n%s",
		tui.AccentStyle.Render(m.spec.Path()),
		tui.DimStyle.Render("Refuses to overwrite if the file already exists."))
	return tui.NewConfirm(tagConfirm, "scaffold — confirm", desc, true)
}

func (m model) writePhase() tui.Phase {
	repo := m.repoRoot
	spec := m.spec
	return tui.NewAsyncTask(tagWrite, "scaffold — writing", "writing file...", func() (any, error) {
		return write(repo, spec)
	})
}

func (m model) donePhase() tui.Phase {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n\n", tui.AccentStyle.Render("✓ wrote"), m.wrote)
	fmt.Fprintf(&b, "kind: %s\n", m.spec.Kind)
	fmt.Fprintf(&b, "name: %s\n\n", m.spec.Name)
	b.WriteString(tui.DimStyle.Render("Next: edit the file, then `go build ./...`"))
	return tui.NewDone("scaffold — done", b.String())
}
