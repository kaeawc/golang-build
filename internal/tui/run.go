package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Run wraps tea.NewProgram with sensible defaults (alt-screen, mouse
// cell motion off) and returns the final model. Callers can pass
// additional tea.ProgramOption values to override defaults.
func Run(m tea.Model, opts ...tea.ProgramOption) (tea.Model, error) {
	defaults := []tea.ProgramOption{tea.WithAltScreen()}
	defaults = append(defaults, opts...)
	p := tea.NewProgram(m, defaults...)
	return p.Run()
}
