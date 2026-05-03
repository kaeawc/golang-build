// Package tui is a small framework for building terminal wizards on top
// of Charm's bubbletea/lipgloss stack.
//
// The framework exposes a Phase contract (see phase.go) and a handful of
// reusable phases (Picker, Confirm, AsyncTask, Done) that callers can
// assemble into a multi-step flow. The root model is supplied by the
// caller; the framework only provides the building blocks.
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Shared styles. Exported so callers can reuse them in custom phases.
var (
	TitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	AccentStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	DimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	WarningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	ErrorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	SelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	BoxStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

// RenderTable produces a plain-text comparison table with the given
// header row, data rows, and an optional highlighted row index. Pass
// highlight = -1 to render without a cursor.
func RenderTable(headers []string, rows [][]string, highlight int) string {
	cols := len(headers)
	widths := make([]int, cols)
	for i, h := range headers {
		widths[i] = lipgloss.Width(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if w := lipgloss.Width(c); w > widths[i] {
				widths[i] = w
			}
		}
	}

	var b strings.Builder
	writeRow := func(cells []string, style lipgloss.Style) {
		parts := make([]string, cols)
		for i, c := range cells {
			parts[i] = PadRight(c, widths[i])
		}
		b.WriteString(style.Render("  " + strings.Join(parts, "  ")))
		b.WriteString("\n")
	}
	writeRow(headers, TitleStyle)
	sep := make([]string, cols)
	for i, w := range widths {
		sep[i] = strings.Repeat("─", w)
	}
	b.WriteString(DimStyle.Render("  " + strings.Join(sep, "  ")))
	b.WriteString("\n")
	for i, r := range rows {
		if i == highlight {
			row := make([]string, len(r))
			copy(row, r)
			row[0] = "▸ " + row[0]
			writeRow(row, SelectedStyle)
			continue
		}
		writeRow(r, lipgloss.NewStyle())
	}
	return b.String()
}

// PadRight pads s with spaces on the right to reach the given display width.
func PadRight(s string, width int) string {
	diff := width - lipgloss.Width(s)
	if diff <= 0 {
		return s
	}
	return s + strings.Repeat(" ", diff)
}
