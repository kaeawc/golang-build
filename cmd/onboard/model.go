package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaeawc/golang-build/internal/tui"
)

// Tags identifying each phase. The root model uses these to dispatch
// completion messages emitted by the generic tui phases.
const (
	tagPresetPick = "preset"
	tagWebSockets = "websockets"
	tagWriteFile  = "write"
)

// model is the wizard's root tea.Model. It owns the accumulated Config
// and the currently active tui.Phase, and routes phase completion
// messages to phase transitions.
type model struct {
	target string
	cfg    Config
	preset Preset

	phase tui.Phase
	err   error
	done  bool

	wrotePath string
}

func newModel(target string) model {
	m := model{target: target}
	m.phase = m.presetPickerPhase()
	return m
}

func (m model) Init() tea.Cmd {
	return tui.PhaseInit(m.phase)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd := tui.HandleGlobal(msg); cmd != nil {
		return m, cmd
	}

	switch msg := msg.(type) {
	case tui.PickerDoneMsg:
		switch msg.Tag {
		case tagPresetPick:
			m.preset = presets[msg.Index]
			m.cfg = fromPreset(m.preset)
			next := m.websocketsConfirmPhase()
			m.phase = next
			return m, tui.PhaseInit(next)
		}

	case tui.ConfirmDoneMsg:
		switch msg.Tag {
		case tagWebSockets:
			m.cfg.WebSockets = msg.Yes
			next := m.writePhase()
			m.phase = next
			return m, tui.PhaseInit(next)
		}

	case tui.TaskDoneMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, tea.Quit
		}
		if msg.Tag == tagWriteFile {
			m.wrotePath = msg.Result.(string)
			m.phase = m.donePhase()
			m.done = true
			return m, tui.PhaseInit(m.phase)
		}
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

// ---------- phase factories -------------------------------------------------

func (m model) presetPickerPhase() tui.Phase {
	headers := []string{"Preset", "Postgres", "Valkey", "WebSockets", "Logs", "Description"}
	items := make([]tui.PickerItem, len(presets))
	for i, p := range presets {
		items[i] = tui.PickerItem{
			Label: p.Name,
			Columns: []string{
				yesNo(p.Postgres),
				yesNo(p.Valkey),
				yesNo(p.WebSockets),
				p.LogLevel,
				p.Description,
			},
		}
	}
	return tui.NewPicker(tagPresetPick, "onboard — pick a preset", headers, items)
}

func (m model) websocketsConfirmPhase() tui.Phase {
	desc := "The /ws handler is registered in cmd/server. Disable to drop the\n" +
		"websocket dependency and skip route registration."
	return tui.NewConfirm(tagWebSockets, "onboard — websockets",
		desc, m.preset.WebSockets)
}

func (m model) writePhase() tui.Phase {
	target := m.target
	cfg := m.cfg
	return tui.NewAsyncTask(tagWriteFile, "onboard — writing config", "writing .env...",
		func() (any, error) {
			return writeConfig(target, cfg)
		})
}

func (m model) donePhase() tui.Phase {
	var b strings.Builder
	b.WriteString(tui.AccentStyle.Render("✓ wrote ") + m.wrotePath + "\n\n")
	fmt.Fprintf(&b, "preset:    %s\n", m.preset.Name)
	fmt.Fprintf(&b, "port:      %d\n", m.cfg.Port)
	fmt.Fprintf(&b, "postgres:  %s\n", yesNo(m.cfg.Postgres))
	fmt.Fprintf(&b, "valkey:    %s\n", yesNo(m.cfg.Valkey))
	fmt.Fprintf(&b, "websocket: %s\n", yesNo(m.cfg.WebSockets))
	fmt.Fprintf(&b, "log level: %s\n", m.cfg.LogLevel)
	b.WriteString("\n")
	b.WriteString(tui.DimStyle.Render("Next: source the .env and `make build && ./server`"))
	return tui.NewDone("onboard — done", b.String())
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
