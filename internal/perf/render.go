package perf

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// RenderOptions tunes RenderTree output.
type RenderOptions struct {
	// MinDuration omits entries whose duration is shorter than this. Zero
	// disables filtering.
	MinDuration time.Duration
	// SortByDuration prints children sorted by duration descending instead
	// of insertion order.
	SortByDuration bool
	// HidePercents disables the "(NN%)" suffix on each line.
	HidePercents bool
	// HideMetrics disables the inline " {key=val key=val}" suffix.
	HideMetrics bool
}

// RenderTree writes a human-readable tree of entries to w. Each line shows
// the phase name, its wall-clock duration, and (unless disabled) its share
// of the parent's duration. Metrics and attributes appear inline.
//
//	parse                234ms  45%
//	├─ collect            12ms   5%  {files=120}
//	└─ scan              220ms  94%
//	   ├─ kotlin         100ms  45%
//	   └─ java           120ms  55%
func RenderTree(w io.Writer, entries []TimingEntry, opts RenderOptions) error {
	if len(entries) == 0 {
		return nil
	}
	totalMs := int64(0)
	for _, e := range entries {
		totalMs += e.DurationMs
	}
	for i, e := range entries {
		if err := renderNode(w, e, "", true, i == len(entries)-1, totalMs, opts); err != nil {
			return err
		}
	}
	return nil
}

func renderNode(w io.Writer, e TimingEntry, prefix string, isRoot, last bool, parentMs int64, opts RenderOptions) error {
	if opts.MinDuration > 0 && time.Duration(e.DurationMs)*time.Millisecond < opts.MinDuration {
		return nil
	}

	connector, childPrefix := nodeConnectors(prefix, isRoot, last)
	line := formatNodeLine(prefix, connector, e, parentMs, opts)
	if _, err := fmt.Fprintln(w, line); err != nil {
		return err
	}

	children := orderedChildren(e.Children, opts.SortByDuration)
	for i, c := range children {
		if err := renderNode(w, c, childPrefix, false, i == len(children)-1, e.DurationMs, opts); err != nil {
			return err
		}
	}
	return nil
}

func nodeConnectors(prefix string, isRoot, last bool) (connector, childPrefix string) {
	switch {
	case isRoot:
		return "", ""
	case last:
		return "└─ ", prefix + "   "
	default:
		return "├─ ", prefix + "│  "
	}
}

func formatNodeLine(prefix, connector string, e TimingEntry, parentMs int64, opts RenderOptions) string {
	line := fmt.Sprintf("%s%s%s %s", prefix, connector, e.Name, formatDur(e.DurationMs))
	if !opts.HidePercents && parentMs > 0 {
		line += fmt.Sprintf("  %3d%%", percent(e.DurationMs, parentMs))
	}
	if !opts.HideMetrics {
		if extra := formatExtras(e.Metrics, e.Attributes); extra != "" {
			line += "  " + extra
		}
	}
	return line
}

func orderedChildren(children []TimingEntry, sortByDuration bool) []TimingEntry {
	if !sortByDuration || len(children) <= 1 {
		return children
	}
	sorted := make([]TimingEntry, len(children))
	copy(sorted, children)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].DurationMs > sorted[j].DurationMs
	})
	return sorted
}

func formatDur(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d >= time.Minute:
		m := d / time.Minute
		s := (d % time.Minute) / time.Second
		return fmt.Sprintf("%dm%02ds", m, s)
	case d >= time.Second:
		return fmt.Sprintf("%5.2fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%4dms", ms)
	case d >= time.Microsecond:
		return fmt.Sprintf("%4dµs", d.Microseconds())
	default:
		return fmt.Sprintf("%4dns", d.Nanoseconds())
	}
}

func percent(part, total int64) int64 {
	if total <= 0 {
		return 0
	}
	return part * 100 / total
}

func formatExtras(metrics map[string]int64, attrs map[string]string) string {
	if len(metrics) == 0 && len(attrs) == 0 {
		return ""
	}
	var parts []string
	keys := make([]string, 0, len(metrics))
	for k := range metrics {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, metrics[k]))
	}
	akeys := make([]string, 0, len(attrs))
	for k := range attrs {
		akeys = append(akeys, k)
	}
	sort.Strings(akeys)
	for _, k := range akeys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, attrs[k]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

// Summary returns a flat top-N breakdown sorted by duration descending. It
// flattens the tree (every entry plus its descendants), sums duplicate names,
// and is useful for "where did time go" CLI output without nested structure.
func Summary(entries []TimingEntry, topN int) []TimingEntry {
	totals := map[string]int64{}
	var walk func([]TimingEntry)
	walk = func(es []TimingEntry) {
		for _, e := range es {
			totals[e.Name] += e.DurationMs
			walk(e.Children)
		}
	}
	walk(entries)

	out := make([]TimingEntry, 0, len(totals))
	for n, d := range totals {
		out = append(out, TimingEntry{Name: n, DurationMs: d})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DurationMs > out[j].DurationMs })
	if topN > 0 && len(out) > topN {
		out = out[:topN]
	}
	return out
}
