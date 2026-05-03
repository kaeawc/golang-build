package tui

import tea "github.com/charmbracelet/bubbletea"

// Phase is the contract a wizard step implements. The root tea.Model
// supplied by the caller delegates Update and View to the active phase
// and swaps phases when one emits a completion message.
type Phase interface {
	Update(msg tea.Msg) (Phase, tea.Cmd)
	View() string
}

// PhaseIniter is implemented by phases that need to fire a command when
// they become active (e.g. spinner ticks, async work). Callers should
// type-assert and call Init when transitioning into a new phase.
type PhaseIniter interface {
	Init() tea.Cmd
}

// HandleGlobal returns a non-nil cmd if the message is a global key
// hit that the framework owns (ctrl+c, q to quit). Root models should
// call this first and short-circuit when it returns non-nil.
func HandleGlobal(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "ctrl+c", "q":
		return tea.Quit
	}
	return nil
}

// PhaseInit returns the Init cmd for a phase if it implements
// PhaseIniter, otherwise nil. Convenience for transition code.
func PhaseInit(p Phase) tea.Cmd {
	if pi, ok := p.(PhaseIniter); ok {
		return pi.Init()
	}
	return nil
}
