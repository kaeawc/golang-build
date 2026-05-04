package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kaeawc/golang-build/internal/tui"
)

// renderSnapshot is the LiveView render fn passed to the dashboard.
func renderSnapshot(s Snapshot) string {
	var b strings.Builder
	b.WriteString(renderHeader(s))
	b.WriteString(renderLevelTable(s.ByLevel))
	b.WriteString(renderKindTable(s.ByKind))
	b.WriteString(renderRecent(s.Recent))
	if !s.Done {
		b.WriteString("\n")
		b.WriteString(tui.DimStyle.Render("watching... q to stop"))
	}
	return b.String()
}

func renderHeader(s Snapshot) string {
	rate := 0.0
	if s.Elapsed.Seconds() > 0 {
		rate = float64(s.Total) / s.Elapsed.Seconds()
	}
	return fmt.Sprintf(
		"%s %d  %s %.1f/s  %s %s\n%s %d  %s %d\n\n",
		tui.DimStyle.Render("events:"), s.Total,
		tui.DimStyle.Render("rate:"), rate,
		tui.DimStyle.Render("elapsed:"), fmtDuration(s.Elapsed),
		tui.DimStyle.Render("subs:"), s.Subscribers,
		tui.DimStyle.Render("dropped:"), s.Dropped,
	)
}

func renderLevelTable(byLevel map[string]int64) string {
	rows := [][]string{}
	for _, lv := range []string{"info", "warn", "error"} {
		if c := byLevel[lv]; c > 0 {
			rows = append(rows, []string{lv, fmt.Sprintf("%d", c)})
		}
	}
	if len(rows) == 0 {
		return ""
	}
	return tui.RenderTable([]string{"level", "count"}, rows, -1) + "\n"
}

func renderKindTable(byKind map[string]int64) string {
	if len(byKind) == 0 {
		return ""
	}
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	rows := make([][]string, 0, len(kinds))
	for _, k := range kinds {
		rows = append(rows, []string{k, fmt.Sprintf("%d", byKind[k])})
	}
	return tui.RenderTable([]string{"kind", "count"}, rows, -1) + "\n"
}

func renderRecent(events []Event) string {
	if len(events) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(tui.DimStyle.Render("recent:") + "\n")
	for _, e := range events {
		b.WriteString(renderEventLine(e) + "\n")
	}
	return b.String()
}

// renderFinal is the body of the Done phase.
func renderFinal(s Snapshot) string {
	return renderSnapshot(s) + "\n" + tui.AccentStyle.Render("✓ stopped")
}

func renderEventLine(e Event) string {
	stamp := e.At.Format("15:04:05.000")
	level := levelBadge(e.Level)
	return fmt.Sprintf("  %s %s %s %s",
		tui.DimStyle.Render(stamp),
		level,
		tui.DimStyle.Render("["+e.Kind+"]"),
		e.Message,
	)
}

func levelBadge(level string) string {
	switch level {
	case "error":
		return tui.ErrorStyle.Render("ERR ")
	case "warn":
		return tui.WarningStyle.Render("WARN")
	default:
		return tui.AccentStyle.Render("INFO")
	}
}

func fmtDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Second {
		return fmt.Sprintf("%.0fms", float64(d.Nanoseconds())/1e6)
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
