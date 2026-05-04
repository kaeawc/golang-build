package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaeawc/golang-build/internal/proc"
	"github.com/kaeawc/golang-build/internal/tui"
)

const (
	tagLoadServices = "load-services"
	tagPickService  = "service"
	tagPickAction   = "action"
	tagRunAction    = "run-action"
)

// composeAction names match docker compose subcommand names.
var composeActions = []string{"ps", "up", "stop", "restart", "logs"}

type model struct {
	runner   proc.Runner
	services []string
	service  string
	action   string
	output   string
	phase    tui.Phase
	err      error
}

func newModel(runner proc.Runner) model {
	m := model{runner: runner}
	m.phase = m.loadServicesPhase()
	return m
}

func (m model) Init() tea.Cmd { return tui.PhaseInit(m.phase) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd := tui.HandleGlobal(msg); cmd != nil {
		return m, cmd
	}
	switch msg := msg.(type) {
	case tui.TaskDoneMsg:
		return m.onTaskDone(msg)
	case tui.PickerDoneMsg:
		return m.onPicker(msg)
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

// ---------- transitions ----------------------------------------------------

func (m model) onTaskDone(msg tui.TaskDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.err = msg.Err
		return m, tea.Quit
	}
	switch msg.Tag {
	case tagLoadServices:
		m.services = msg.Result.([]string)
		m.phase = m.servicePicker()
		return m, nil
	case tagRunAction:
		m.output = msg.Result.(string)
		m.phase = m.donePhase()
		return m, nil
	}
	return m, nil
}

func (m model) onPicker(msg tui.PickerDoneMsg) (tea.Model, tea.Cmd) {
	switch msg.Tag {
	case tagPickService:
		m.service = m.services[msg.Index]
		m.phase = m.actionPicker()
		return m, nil
	case tagPickAction:
		m.action = composeActions[msg.Index]
		next := m.runActionPhase()
		m.phase = next
		return m, tui.PhaseInit(next)
	}
	return m, nil
}

// ---------- phase factories ------------------------------------------------

func (m model) loadServicesPhase() tui.Phase {
	r := m.runner
	return tui.NewAsyncTask(tagLoadServices, "devup — discovering services", "running docker compose config...",
		func() (any, error) {
			return listComposeServices(context.Background(), r)
		})
}

func (m model) servicePicker() tui.Phase {
	if len(m.services) == 0 {
		return tui.NewDone("devup — services", tui.DimStyle.Render("(no services)"))
	}
	items := make([]tui.PickerItem, len(m.services))
	for i, s := range m.services {
		items[i] = tui.PickerItem{Label: s}
	}
	return tui.NewPicker(tagPickService, "devup — pick a service", nil, items)
}

func (m model) actionPicker() tui.Phase {
	items := make([]tui.PickerItem, len(composeActions))
	for i, a := range composeActions {
		items[i] = tui.PickerItem{Label: a}
	}
	return tui.NewPicker(tagPickAction, fmt.Sprintf("devup — %s", m.service), nil, items)
}

func (m model) runActionPhase() tui.Phase {
	r := m.runner
	action, service := m.action, m.service
	return tui.NewAsyncTask(tagRunAction,
		fmt.Sprintf("devup — %s %s", action, service),
		"running docker compose...",
		func() (any, error) {
			return composeAction(context.Background(), r, action, service)
		})
}

func (m model) donePhase() tui.Phase {
	var b strings.Builder
	fmt.Fprintf(&b, "%s docker compose %s %s\n\n",
		tui.AccentStyle.Render("✓"), m.action, m.service)
	out := strings.TrimSpace(m.output)
	if out == "" {
		out = tui.DimStyle.Render("(no output)")
	}
	b.WriteString(out)
	return tui.NewDone("devup — done", b.String())
}
