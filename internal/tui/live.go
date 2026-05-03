package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// LiveSampler is the contract a LiveView uses to poll work in
// progress. Sample is called once per tick and may return done=true to
// end the live view; the last non-nil sample is delivered as the Final
// field of LiveDoneMsg.
type LiveSampler interface {
	Sample() (sample any, done bool)
}

// LiveDoneMsg is emitted when a LiveView's sampler reports done.
type LiveDoneMsg struct {
	Tag   string
	Final any
}

// liveTickMsg drives the LiveView's polling loop. Internal.
type liveTickMsg struct{ tag string }

// LiveView is a phase that polls a LiveSampler on a fixed interval and
// re-renders the current sample using a caller-supplied Render fn. It
// is the framework's escape hatch for streaming / dashboard UIs.
type LiveView struct {
	tag      string
	title    string
	sampler  LiveSampler
	interval time.Duration
	render   func(sample any) string
	last     any
}

// NewLiveView constructs a LiveView. interval is how often Sample is
// called; render is invoked with the most recent sample (or nil before
// the first tick fires).
func NewLiveView(tag, title string, sampler LiveSampler, interval time.Duration, render func(any) string) LiveView {
	return LiveView{
		tag:      tag,
		title:    title,
		sampler:  sampler,
		interval: interval,
		render:   render,
	}
}

func (v LiveView) Init() tea.Cmd {
	return v.tick()
}

func (v LiveView) tick() tea.Cmd {
	tag := v.tag
	d := v.interval
	return tea.Tick(d, func(time.Time) tea.Msg {
		return liveTickMsg{tag: tag}
	})
}

func (v LiveView) Update(msg tea.Msg) (Phase, tea.Cmd) {
	tick, ok := msg.(liveTickMsg)
	if !ok || tick.tag != v.tag {
		return v, nil
	}
	sample, done := v.sampler.Sample()
	v.last = sample
	if done {
		final := sample
		tag := v.tag
		return v, func() tea.Msg { return LiveDoneMsg{Tag: tag, Final: final} }
	}
	return v, v.tick()
}

func (v LiveView) View() string {
	body := ""
	if v.last != nil {
		body = v.render(v.last)
	} else {
		body = DimStyle.Render("waiting for first sample...")
	}
	return TitleStyle.Render(v.title) + "\n\n" + body
}
