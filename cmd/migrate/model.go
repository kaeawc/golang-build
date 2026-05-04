package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaeawc/golang-build/internal/tui"
)

const (
	tagLoadStatus = "load-status"
	tagPickAction = "action"
	tagConfirmRun = "confirm"
	tagRunMig     = "run"
)

type action int

const (
	actUpAll action = iota
	actUpOne
	actDownOne
	actRefresh
	actQuit
)

var actionLabels = []struct {
	a     action
	label string
}{
	{actUpAll, "Apply all pending migrations"},
	{actUpOne, "Apply next pending migration"},
	{actDownOne, "Roll back last applied migration"},
	{actRefresh, "Refresh status"},
	{actQuit, "Quit"},
}

// runResult is the payload of the migration AsyncTask.
type runResult struct {
	action   action
	priorVer uint
	newVer   uint
	err      error
}

type model struct {
	migrator Migrator
	status   Status
	pending  action
	phase    tui.Phase
	err      error
}

func newModel(migrator Migrator) model {
	m := model{migrator: migrator}
	m.phase = m.loadStatusPhase()
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
	case tui.ConfirmDoneMsg:
		return m.onConfirm(msg)
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
	case tagLoadStatus:
		m.status = msg.Result.(Status)
		m.phase = m.actionPicker()
		return m, nil
	case tagRunMig:
		res := msg.Result.(runResult)
		m.phase = m.donePhase(res)
		return m, nil
	}
	return m, nil
}

func (m model) onPicker(msg tui.PickerDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Tag != tagPickAction {
		return m, nil
	}
	a := actionLabels[msg.Index].a
	m.pending = a
	switch a {
	case actQuit:
		return m, tea.Quit
	case actRefresh:
		next := m.loadStatusPhase()
		m.phase = next
		return m, tui.PhaseInit(next)
	}
	m.phase = m.confirmPhase()
	return m, nil
}

func (m model) onConfirm(msg tui.ConfirmDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Tag != tagConfirmRun {
		return m, nil
	}
	if !msg.Yes {
		next := m.loadStatusPhase()
		m.phase = next
		return m, tui.PhaseInit(next)
	}
	next := m.runPhase(m.pending)
	m.phase = next
	return m, tui.PhaseInit(next)
}

// ---------- phase factories ------------------------------------------------

func (m model) loadStatusPhase() tui.Phase {
	mig := m.migrator
	return tui.NewAsyncTask(tagLoadStatus, "migrate — loading status", "querying database...",
		func() (any, error) { return mig.Status(context.Background()) })
}

func (m model) actionPicker() tui.Phase {
	headers := []string{"action", "description"}
	items := make([]tui.PickerItem, len(actionLabels))
	for i, al := range actionLabels {
		items[i] = tui.PickerItem{
			Label:   al.label,
			Columns: []string{m.actionDetail(al.a)},
		}
	}
	title := fmt.Sprintf("migrate — current version: %s, %d pending",
		versionLabel(m.status), len(m.status.Pending()))
	return tui.NewPicker(tagPickAction, title, headers, items)
}

func (m model) actionDetail(a action) string {
	pending := m.status.Pending()
	switch a {
	case actUpAll:
		return fmt.Sprintf("apply %d pending", len(pending))
	case actUpOne:
		if len(pending) == 0 {
			return "(nothing pending)"
		}
		return "→ " + pending[0].Name
	case actDownOne:
		if !m.status.HasVersion {
			return "(no applied versions)"
		}
		return fmt.Sprintf("revert v%d", m.status.CurrentVersion)
	case actRefresh:
		return "re-query the database"
	case actQuit:
		return "exit without changes"
	}
	return ""
}

func (m model) confirmPhase() tui.Phase {
	desc := tui.DimStyle.Render(m.actionDetail(m.pending))
	return tui.NewConfirm(tagConfirmRun,
		fmt.Sprintf("migrate — confirm %s", actionLabels[m.pending].label),
		desc, true)
}

func (m model) runPhase(a action) tui.Phase {
	mig := m.migrator
	prior := m.status.CurrentVersion
	return tui.NewAsyncTask(tagRunMig, "migrate — running", "applying migration...",
		func() (any, error) {
			ctx := context.Background()
			var err error
			switch a {
			case actUpAll:
				err = mig.UpAll(ctx)
			case actUpOne:
				err = mig.UpOne(ctx)
			case actDownOne:
				err = mig.DownOne(ctx)
			}
			if err != nil {
				return runResult{action: a, priorVer: prior, err: err}, nil
			}
			st, sErr := mig.Status(ctx)
			if sErr != nil {
				return runResult{action: a, priorVer: prior, err: sErr}, nil
			}
			return runResult{action: a, priorVer: prior, newVer: st.CurrentVersion}, nil
		})
}

func (m model) donePhase(res runResult) tui.Phase {
	var b strings.Builder
	if res.err != nil {
		fmt.Fprintf(&b, "%s %s\n\n", tui.ErrorStyle.Render("✗"), res.err.Error())
	} else {
		fmt.Fprintf(&b, "%s applied %s\n\n",
			tui.AccentStyle.Render("✓"), actionLabels[res.action].label)
		fmt.Fprintf(&b, "version: %d → %d\n", res.priorVer, res.newVer)
	}
	return tui.NewDone("migrate — done", b.String())
}

func versionLabel(s Status) string {
	if !s.HasVersion {
		return "(none)"
	}
	if s.Dirty {
		return fmt.Sprintf("v%d (DIRTY)", s.CurrentVersion)
	}
	return fmt.Sprintf("v%d", s.CurrentVersion)
}
