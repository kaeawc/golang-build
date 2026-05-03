package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ConfirmDoneMsg is emitted when the user picks yes or no in a Confirm
// phase.
type ConfirmDoneMsg struct {
	Tag string
	Yes bool
}

// Confirm is a yes/no prompt phase. Cursor 0 = yes, 1 = no.
type Confirm struct {
	tag         string
	title       string
	description string
	cursor      int
}

// NewConfirm constructs a Confirm with the given default selection.
func NewConfirm(tag, title, description string, defaultYes bool) Confirm {
	cursor := 1
	if defaultYes {
		cursor = 0
	}
	return Confirm{tag: tag, title: title, description: description, cursor: cursor}
}

func (c Confirm) Update(msg tea.Msg) (Phase, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return c, nil
	}
	switch km.String() {
	case "left", "h", "y", "Y":
		c.cursor = 0
	case "right", "l", "n", "N":
		c.cursor = 1
	case "enter", " ":
		yes := c.cursor == 0
		tag := c.tag
		return c, func() tea.Msg { return ConfirmDoneMsg{Tag: tag, Yes: yes} }
	}
	return c, nil
}

func (c Confirm) View() string {
	var b strings.Builder
	b.WriteString(TitleStyle.Render(c.title))
	b.WriteString("\n\n")
	if c.description != "" {
		b.WriteString(c.description)
		b.WriteString("\n\n")
	}
	yes, no := confirmButtons(c.cursor)
	b.WriteString("  " + yes + "   " + no + "\n\n")
	b.WriteString(DimStyle.Render("←/→ y/n pick · enter confirm · q quit"))
	return b.String()
}

func confirmButtons(cursor int) (string, string) {
	if cursor == 0 {
		return SelectedStyle.Render("[ Yes ]"), DimStyle.Render("  No  ")
	}
	return DimStyle.Render("  Yes  "), SelectedStyle.Render("[ No ]")
}
