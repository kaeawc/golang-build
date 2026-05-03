package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaeawc/golang-build/internal/tui"
)

const (
	tagPickConcurrency = "concurrency"
	tagPickDuration    = "duration"
	tagDashboard       = "dashboard"
)

type concurrencyChoice struct {
	label string
	value int
}

type durationChoice struct {
	label string
	value time.Duration
}

var (
	concurrencyChoices = []concurrencyChoice{
		{"low (4 workers)", 4},
		{"medium (16 workers)", 16},
		{"high (64 workers)", 64},
	}
	durationChoices = []durationChoice{
		{"5s", 5 * time.Second},
		{"15s", 15 * time.Second},
		{"30s", 30 * time.Second},
	}
)

// engineSampler adapts an *Engine into a tui.LiveSampler. Sample is
// safe to call from the bubbletea goroutine.
type engineSampler struct{ engine *Engine }

func (s engineSampler) Sample() (any, bool) {
	snap := s.engine.Snapshot()
	return snap, snap.Done
}

type model struct {
	url    string
	cfg    Config
	engine *Engine
	cancel context.CancelFunc
	final  Snapshot
	phase  tui.Phase
	err    error
}

func newModel(url string) model {
	m := model{url: url, cfg: Config{URL: url, Method: http.MethodGet}}
	m.phase = tui.NewPicker(tagPickConcurrency, "loadgen — concurrency", nil, concurrencyItems())
	return m
}

func concurrencyItems() []tui.PickerItem {
	out := make([]tui.PickerItem, len(concurrencyChoices))
	for i, c := range concurrencyChoices {
		out[i] = tui.PickerItem{Label: c.label}
	}
	return out
}

func durationItems() []tui.PickerItem {
	out := make([]tui.PickerItem, len(durationChoices))
	for i, c := range durationChoices {
		out[i] = tui.PickerItem{Label: c.label}
	}
	return out
}

func (m model) Init() tea.Cmd { return tui.PhaseInit(m.phase) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd := tui.HandleGlobal(msg); cmd != nil {
		if m.cancel != nil {
			m.cancel()
		}
		return m, cmd
	}

	switch msg := msg.(type) {
	case tui.PickerDoneMsg:
		return m.onPickerDone(msg)
	case tui.LiveDoneMsg:
		return m.onDashboardDone(msg)
	}

	next, cmd := m.phase.Update(msg)
	m.phase = next
	return m, cmd
}

func (m model) onPickerDone(msg tui.PickerDoneMsg) (tea.Model, tea.Cmd) {
	switch msg.Tag {
	case tagPickConcurrency:
		m.cfg.Concurrency = concurrencyChoices[msg.Index].value
		next := tui.NewPicker(tagPickDuration, "loadgen — duration", nil, durationItems())
		m.phase = next
		return m, tui.PhaseInit(next)

	case tagPickDuration:
		m.cfg.Duration = durationChoices[msg.Index].value
		m.engine = NewEngine(m.cfg)
		ctx, cancel := context.WithCancel(context.Background())
		m.cancel = cancel
		go m.engine.Start(ctx)

		title := fmt.Sprintf("loadgen — %s @ %d workers for %s",
			m.cfg.URL, m.cfg.Concurrency, m.cfg.Duration)
		next := tui.NewLiveView(tagDashboard, title, engineSampler{engine: m.engine},
			250*time.Millisecond, func(s any) string {
				return renderSnapshot(s.(Snapshot))
			})
		m.phase = next
		return m, tui.PhaseInit(next)
	}
	return m, nil
}

func (m model) onDashboardDone(msg tui.LiveDoneMsg) (tea.Model, tea.Cmd) {
	if m.cancel != nil {
		m.cancel()
	}
	m.final, _ = msg.Final.(Snapshot)
	m.phase = tui.NewDone("loadgen — done", renderFinal(m.final))
	return m, nil
}

func (m model) View() string {
	if m.err != nil {
		return tui.ErrorStyle.Render("error: ") + m.err.Error() + "\n"
	}
	return m.phase.View()
}

