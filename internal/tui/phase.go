package tui

import tea "charm.land/bubbletea/v2"

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

// TextEntry is implemented by phases that capture printable keys
// (e.g. TextInput). HandleGlobal leaves bare letters such as "q" to
// them so they can be typed; ctrl+c still quits.
type TextEntry interface {
	CapturesText() bool
}

// HandleGlobal returns a non-nil cmd if the message is a global key
// hit that the framework owns (ctrl+c, and q unless the active phase
// captures text). Root models should call this first and short-circuit
// when it returns non-nil.
func HandleGlobal(msg tea.Msg, active Phase) tea.Cmd {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "ctrl+c":
		return tea.Quit
	case "q":
		if te, ok := active.(TextEntry); ok && te.CapturesText() {
			return nil
		}
		return tea.Quit
	}
	return nil
}

// RootView renders a root model's frame: err if set, otherwise the
// active phase.
func RootView(err error, active Phase) tea.View {
	if err != nil {
		return tea.NewView(ErrorStyle.Render("error: ") + err.Error() + "\n")
	}
	return tea.NewView(active.View())
}

// PhaseInit returns the Init cmd for a phase if it implements
// PhaseIniter, otherwise nil. Convenience for transition code.
func PhaseInit(p Phase) tea.Cmd {
	if pi, ok := p.(PhaseIniter); ok {
		return pi.Init()
	}
	return nil
}
