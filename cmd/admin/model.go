package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaeawc/golang-build/internal/tui"
)

const (
	tagPickAction = "action"
	tagLoadUsers  = "load-users"
	tagLoadProbes = "load-probes"
	tagPickUser   = "user-row"
	tagPickProbe  = "probe-row"
)

type action int

const (
	actListUsers action = iota
	actHealthchecks
	actQuit
)

var actionLabels = []string{
	"List users",
	"Run healthchecks",
	"Quit",
}

type model struct {
	backend Backend
	users   []User
	probes  []Probe
	phase   tui.Phase
	err     error
}

func newModel(backend Backend) model {
	m := model{backend: backend}
	m.phase = m.actionPicker()
	return m
}

func (m model) Init() tea.Cmd { return tui.PhaseInit(m.phase) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd := tui.HandleGlobal(msg); cmd != nil {
		return m, cmd
	}

	switch msg := msg.(type) {
	case tui.PickerDoneMsg:
		return m.onPicker(msg)
	case tui.TaskDoneMsg:
		return m.onTaskDone(msg)
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

func (m model) onPicker(msg tui.PickerDoneMsg) (tea.Model, tea.Cmd) {
	switch msg.Tag {
	case tagPickAction:
		return m.onActionPicked(msg.Index)
	case tagPickUser:
		u := m.users[msg.Index]
		m.phase = tui.NewDone("admin — user", renderUserDetail(u))
		return m, nil
	case tagPickProbe:
		p := m.probes[msg.Index]
		m.phase = tui.NewDone("admin — probe", renderProbeDetail(p))
		return m, nil
	}
	return m, nil
}

func (m model) onActionPicked(idx int) (tea.Model, tea.Cmd) {
	switch action(idx) {
	case actListUsers:
		next := tui.NewAsyncTask(tagLoadUsers, "admin — loading users", "querying...", func() (any, error) {
			return m.backend.ListUsers(context.Background())
		})
		m.phase = next
		return m, tui.PhaseInit(next)
	case actHealthchecks:
		next := tui.NewAsyncTask(tagLoadProbes, "admin — running healthchecks", "probing...", func() (any, error) {
			return m.backend.RunHealthchecks(context.Background())
		})
		m.phase = next
		return m, tui.PhaseInit(next)
	case actQuit:
		return m, tea.Quit
	}
	return m, nil
}

func (m model) onTaskDone(msg tui.TaskDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.err = msg.Err
		return m, tea.Quit
	}
	switch msg.Tag {
	case tagLoadUsers:
		users, _ := msg.Result.([]User)
		m.users = users
		m.phase = m.userPicker(users)
		return m, nil
	case tagLoadProbes:
		probes, _ := msg.Result.([]Probe)
		m.probes = probes
		m.phase = m.probePicker(probes)
		return m, nil
	}
	return m, nil
}

// ---------- phase factories ------------------------------------------------

func (m model) actionPicker() tui.Phase {
	items := make([]tui.PickerItem, len(actionLabels))
	for i, l := range actionLabels {
		items[i] = tui.PickerItem{Label: l}
	}
	return tui.NewPicker(tagPickAction, "admin — what to do", nil, items)
}

func (m model) userPicker(users []User) tui.Phase {
	if len(users) == 0 {
		return tui.NewDone("admin — users", tui.DimStyle.Render("(no users)"))
	}
	items := make([]tui.PickerItem, len(users))
	for i, u := range users {
		items[i] = tui.PickerItem{
			Label:   fmt.Sprintf("%d", u.ID),
			Columns: []string{u.Name},
		}
	}
	return tui.NewPicker(tagPickUser, "admin — users", []string{"id", "name"}, items)
}

func (m model) probePicker(probes []Probe) tui.Phase {
	if len(probes) == 0 {
		return tui.NewDone("admin — healthchecks", tui.DimStyle.Render("(no probes)"))
	}
	items := make([]tui.PickerItem, len(probes))
	for i, p := range probes {
		status := tui.AccentStyle.Render("ok")
		if !p.Healthy {
			status = tui.ErrorStyle.Render("FAIL")
		}
		items[i] = tui.PickerItem{
			Label:   p.Name,
			Columns: []string{status, p.Latency.String()},
		}
	}
	return tui.NewPicker(tagPickProbe, "admin — healthchecks", []string{"probe", "status", "latency"}, items)
}

func renderUserDetail(u User) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %d\n", tui.DimStyle.Render("id:  "), u.ID)
	fmt.Fprintf(&b, "%s %s\n", tui.DimStyle.Render("name:"), u.Name)
	return b.String()
}

func renderProbeDetail(p Probe) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", tui.DimStyle.Render("probe:  "), p.Name)
	if p.Healthy {
		fmt.Fprintf(&b, "%s %s\n", tui.DimStyle.Render("status: "), tui.AccentStyle.Render("ok"))
	} else {
		fmt.Fprintf(&b, "%s %s\n", tui.DimStyle.Render("status: "), tui.ErrorStyle.Render("FAIL"))
	}
	fmt.Fprintf(&b, "%s %s\n", tui.DimStyle.Render("latency:"), p.Latency)
	if p.Err != "" {
		fmt.Fprintf(&b, "%s %s\n", tui.DimStyle.Render("error:  "), p.Err)
	}
	return b.String()
}
