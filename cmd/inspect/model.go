package main

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaeawc/golang-build/internal/eventbus"
	"github.com/kaeawc/golang-build/internal/tui"
)

const (
	tagPickScenario = "scenario"
	tagDashboard    = "dashboard"
)

type model struct {
	scenario  Scenario
	bus       *eventbus.Async[Event]
	inspector *Inspector
	cancel    context.CancelFunc
	final     Snapshot
	phase     tui.Phase
	err       error
}

func newModel() model {
	m := model{}
	m.phase = m.scenarioPicker()
	return m
}

func (m model) Init() tea.Cmd { return tui.PhaseInit(m.phase) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd := tui.HandleGlobal(msg); cmd != nil {
		// Stop the producer + inspector before quitting so the goroutines
		// don't outlive the program.
		m.shutdown()
		return m, cmd
	}
	switch msg := msg.(type) {
	case tui.PickerDoneMsg:
		return m.onPicker(msg)
	case tui.LiveDoneMsg:
		return m.onDashboardDone(msg)
	}
	next, cmd := m.phase.Update(msg)
	m.phase = next
	return m, cmd
}

func (m model) View() string {
	if m.err != nil {
		return tui.ErrorStyle.Render("error: ") + m.err.Error() + "\n"
	}
	return m.phase.View()
}

func (m *model) shutdown() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.inspector != nil {
		m.inspector.Stop()
	}
	if m.bus != nil {
		m.bus.Close()
		m.bus = nil
	}
}

// ---------- transitions ----------------------------------------------------

func (m model) onPicker(msg tui.PickerDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Tag != tagPickScenario {
		return m, nil
	}
	m.scenario = Scenarios[msg.Index]
	m.bus = eventbus.NewAsync[Event](eventbus.AsyncConfig{
		BufferSize:   64,
		DropWhenFull: true,
	})
	m.inspector = NewInspector(m.bus, 10)
	m.inspector.Start()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go m.scenario.Run(ctx, m.bus)

	title := fmt.Sprintf("inspect — %s (%s)", m.scenario.Name, m.scenario.Description)
	next := tui.NewLiveView(tagDashboard, title, m.inspector,
		200*time.Millisecond, func(s any) string {
			return renderSnapshot(s.(Snapshot))
		})
	m.phase = next
	return m, tui.PhaseInit(next)
}

func (m model) onDashboardDone(msg tui.LiveDoneMsg) (tea.Model, tea.Cmd) {
	m.shutdown()
	if snap, ok := msg.Final.(Snapshot); ok {
		m.final = snap
	}
	m.phase = tui.NewDone("inspect — done", renderFinal(m.final))
	return m, nil
}

// ---------- phase factories ------------------------------------------------

func (m model) scenarioPicker() tui.Phase {
	headers := []string{"scenario", "description"}
	items := make([]tui.PickerItem, len(Scenarios))
	for i, s := range Scenarios {
		items[i] = tui.PickerItem{Label: s.Name, Columns: []string{s.Description}}
	}
	return tui.NewPicker(tagPickScenario, "inspect — pick a scenario", headers, items)
}
