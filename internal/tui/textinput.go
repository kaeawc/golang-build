package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// TextInputDoneMsg is emitted when the user submits a non-empty value
// that passes validation.
type TextInputDoneMsg struct {
	Tag   string
	Value string
}

// TextInput is a single-line input phase backed by bubbles/textinput.
// An optional Validate function is invoked on submit; if it returns a
// non-nil error, the error is shown inline and the value is not emitted.
type TextInput struct {
	tag      string
	title    string
	prompt   string
	input    textinput.Model
	validate func(string) error
	errMsg   string
}

// NewTextInput constructs a TextInput. placeholder is shown when the
// field is empty; validate may be nil for no validation.
func NewTextInput(tag, title, prompt, placeholder string, validate func(string) error) TextInput {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.Prompt = "> "
	ti.CharLimit = 128
	ti.Focus()
	return TextInput{
		tag:      tag,
		title:    title,
		prompt:   prompt,
		input:    ti,
		validate: validate,
	}
}

func (t TextInput) Init() tea.Cmd {
	return textinput.Blink
}

func (t TextInput) Update(msg tea.Msg) (Phase, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok && km.Type == tea.KeyEnter {
		val := strings.TrimSpace(t.input.Value())
		if val == "" {
			t.errMsg = "value is required"
			return t, nil
		}
		if t.validate != nil {
			if err := t.validate(val); err != nil {
				t.errMsg = err.Error()
				return t, nil
			}
		}
		tag := t.tag
		return t, func() tea.Msg { return TextInputDoneMsg{Tag: tag, Value: val} }
	}

	var cmd tea.Cmd
	t.input, cmd = t.input.Update(msg)
	t.errMsg = "" // any keystroke clears the error
	return t, cmd
}

func (t TextInput) View() string {
	var b strings.Builder
	b.WriteString(TitleStyle.Render(t.title))
	b.WriteString("\n\n")
	if t.prompt != "" {
		b.WriteString(t.prompt)
		b.WriteString("\n\n")
	}
	b.WriteString(t.input.View())
	b.WriteString("\n")
	if t.errMsg != "" {
		b.WriteString("\n")
		b.WriteString(ErrorStyle.Render("✗ " + t.errMsg))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(DimStyle.Render("type to enter · enter submit · q quit"))
	return b.String()
}
