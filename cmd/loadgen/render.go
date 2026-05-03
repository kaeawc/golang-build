package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kaeawc/golang-build/internal/tui"
)

// renderSnapshot is the LiveView render fn passed to the dashboard
// phase. It formats a Snapshot into a stable multi-line block that the
// TUI re-renders every tick.
func renderSnapshot(s Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s  ", tui.DimStyle.Render("target:"), s.TargetURL)
	fmt.Fprintf(&b, "%s %d  ", tui.DimStyle.Render("workers:"), s.Concurrency)
	fmt.Fprintf(&b, "%s %s/%s\n",
		tui.DimStyle.Render("elapsed:"),
		fmtDuration(s.Elapsed),
		fmtDuration(time.Duration(s.TargetSeconds*float64(time.Second))),
	)
	b.WriteString("\n")

	rows := [][]string{
		{"requests", fmt.Sprintf("%d", s.Total)},
		{"errors", fmt.Sprintf("%d", s.Errors)},
		{"rps", fmt.Sprintf("%.1f", s.RPS)},
		{"p50", fmtDuration(s.P50)},
		{"p95", fmtDuration(s.P95)},
		{"p99", fmtDuration(s.P99)},
	}
	b.WriteString(tui.RenderTable([]string{"metric", "value"}, rows, -1))

	if len(s.StatusCounts) > 0 {
		b.WriteString("\n")
		codes := make([]int, 0, len(s.StatusCounts))
		for c := range s.StatusCounts {
			codes = append(codes, c)
		}
		sort.Ints(codes)
		statusRows := make([][]string, 0, len(codes))
		for _, c := range codes {
			statusRows = append(statusRows, []string{fmt.Sprintf("%d", c), fmt.Sprintf("%d", s.StatusCounts[c])})
		}
		b.WriteString(tui.RenderTable([]string{"status", "count"}, statusRows, -1))
	}

	if !s.Done {
		b.WriteString("\n")
		b.WriteString(tui.DimStyle.Render("running... q to abort"))
	}
	return b.String()
}

// renderFinal is the body of the Done phase: the snapshot plus a one-line
// "complete" note.
func renderFinal(s Snapshot) string {
	body := renderSnapshot(s)
	return body + "\n" + tui.AccentStyle.Render("✓ complete")
}

func fmtDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Microsecond {
		return fmt.Sprintf("%dns", d.Nanoseconds())
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%.1fµs", float64(d.Nanoseconds())/1000)
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d.Nanoseconds())/1e6)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}
