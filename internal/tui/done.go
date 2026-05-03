package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Done is a terminal phase that renders a final summary and quits the
// program when the user presses enter or esc.
type Done struct {
	title string
	body  string
}

// NewDone constructs a Done phase. body is rendered verbatim under the
// title; callers are expected to style it themselves with the exported
// styles.
func NewDone(title, body string) Done {
	return Done{title: title, body: body}
}

func (d Done) Update(msg tea.Msg) (Phase, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "enter", "esc":
			return d, tea.Quit
		}
	}
	return d, nil
}

func (d Done) View() string {
	var b strings.Builder
	b.WriteString(TitleStyle.Render(d.title))
	b.WriteString("\n\n")
	b.WriteString(d.body)
	if !strings.HasSuffix(d.body, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(DimStyle.Render("press enter to exit"))
	return b.String()
}
