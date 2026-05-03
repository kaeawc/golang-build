package tui

import (
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// TaskDoneMsg is emitted when an AsyncTask's worker function returns.
// Result holds whatever the worker returned; Err is non-nil on failure.
type TaskDoneMsg struct {
	Tag    string
	Result any
	Err    error
}

// AsyncTask is a passive phase that runs a worker function on Init and
// shows a spinner with a label until it completes. The result is
// delivered to the root model via TaskDoneMsg.
type AsyncTask struct {
	tag     string
	title   string
	label   string
	fn      func() (any, error)
	spinner spinner.Model
	started bool
}

// NewAsyncTask constructs an AsyncTask. The worker fn runs once on
// Init in a goroutine managed by bubbletea.
func NewAsyncTask(tag, title, label string, fn func() (any, error)) AsyncTask {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = AccentStyle
	return AsyncTask{tag: tag, title: title, label: label, fn: fn, spinner: sp}
}

func (t AsyncTask) Init() tea.Cmd {
	tag := t.tag
	fn := t.fn
	return tea.Batch(
		t.spinner.Tick,
		func() tea.Msg {
			res, err := fn()
			return TaskDoneMsg{Tag: tag, Result: res, Err: err}
		},
	)
}

func (t AsyncTask) Update(msg tea.Msg) (Phase, tea.Cmd) {
	var cmd tea.Cmd
	t.spinner, cmd = t.spinner.Update(msg)
	return t, cmd
}

func (t AsyncTask) View() string {
	return TitleStyle.Render(t.title) + "\n\n" +
		t.spinner.View() + " " + DimStyle.Render(t.label)
}
