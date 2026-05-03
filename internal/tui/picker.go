package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// PickerItem is one row in a Picker. Label is the primary identifier
// returned in PickerDoneMsg.Item. Columns optionally provides extra
// table cells rendered alongside the label; the first column is always
// the label itself.
type PickerItem struct {
	Label   string
	Columns []string
}

// PickerDoneMsg is emitted when the user commits a picker selection.
// Tag carries the caller-supplied identifier so a single root model can
// route between multiple picker steps.
type PickerDoneMsg struct {
	Tag   string
	Index int
	Item  PickerItem
}

// Picker is a single-select list phase. Rendered as a table when the
// items carry Columns, or as a bullet list otherwise.
type Picker struct {
	tag     string
	title   string
	headers []string
	items   []PickerItem
	cursor  int
}

// NewPicker constructs a Picker. headers may be nil for a bullet-list
// view; otherwise len(headers) must match len(items[i].Columns)+1
// (the extra column is the label).
func NewPicker(tag, title string, headers []string, items []PickerItem) Picker {
	return Picker{tag: tag, title: title, headers: headers, items: items}
}

func (p Picker) Update(msg tea.Msg) (Phase, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch km.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.items)-1 {
			p.cursor++
		}
	case "enter", " ":
		idx := p.cursor
		item := p.items[idx]
		tag := p.tag
		return p, func() tea.Msg {
			return PickerDoneMsg{Tag: tag, Index: idx, Item: item}
		}
	}
	return p, nil
}

func (p Picker) View() string {
	var b strings.Builder
	b.WriteString(TitleStyle.Render(p.title))
	b.WriteString("\n\n")

	if len(p.headers) > 0 {
		rows := make([][]string, len(p.items))
		for i, it := range p.items {
			row := make([]string, 0, 1+len(it.Columns))
			row = append(row, it.Label)
			row = append(row, it.Columns...)
			rows[i] = row
		}
		b.WriteString(RenderTable(p.headers, rows, p.cursor))
	} else {
		for i, it := range p.items {
			marker := "  "
			line := it.Label
			if i == p.cursor {
				marker = "▸ "
				line = SelectedStyle.Render(line)
			}
			b.WriteString(marker + line + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(DimStyle.Render("↑/↓ move · enter select · q quit"))
	return b.String()
}
