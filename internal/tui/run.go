package tui

import (
	tea "charm.land/bubbletea/v2"
)

// Run wraps tea.NewProgram with sensible defaults (alt-screen, mouse
// off) and returns the final model. Callers can pass additional
// tea.ProgramOption values.
func Run(m tea.Model, opts ...tea.ProgramOption) (tea.Model, error) {
	p := tea.NewProgram(altScreen{m}, opts...)
	final, err := p.Run()
	if a, ok := final.(altScreen); ok {
		final = a.Model
	}
	return final, err
}

// altScreen enables the alternate screen on every frame. Bubble Tea v2
// dropped tea.WithAltScreen in favour of the View.AltScreen field, so
// Run applies it here rather than requiring every root model to.
type altScreen struct{ tea.Model }

func (a altScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := a.Model.Update(msg)
	return altScreen{m}, cmd
}

func (a altScreen) View() tea.View {
	v := a.Model.View()
	v.AltScreen = true
	return v
}
