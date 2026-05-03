package perf

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func sample() []TimingEntry {
	return []TimingEntry{
		{
			Name: "parse", DurationMs: 200,
			Children: []TimingEntry{
				{Name: "collect", DurationMs: 20, Metrics: map[string]int64{"files": 120}},
				{Name: "scan", DurationMs: 180, Children: []TimingEntry{
					{Name: "kotlin", DurationMs: 100},
					{Name: "java", DurationMs: 80},
				}},
			},
		},
		{Name: "report", DurationMs: 50, Attributes: map[string]string{"format": "json"}},
	}
}

func TestRenderTreeShape(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderTree(&buf, sample(), RenderOptions{}); err != nil {
		t.Fatalf("RenderTree: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"parse",
		"├─ collect",
		"└─ scan",
		"   ├─ kotlin",
		"   └─ java",
		"report",
		"files=120",
		"format=json",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestRenderTreeHonorsHideFlags(t *testing.T) {
	var buf bytes.Buffer
	_ = RenderTree(&buf, sample(), RenderOptions{HidePercents: true, HideMetrics: true})
	out := buf.String()
	if strings.Contains(out, "%") {
		t.Errorf("HidePercents not honored:\n%s", out)
	}
	if strings.Contains(out, "files=") {
		t.Errorf("HideMetrics not honored:\n%s", out)
	}
}

func TestRenderTreeSortByDuration(t *testing.T) {
	entries := []TimingEntry{
		{Name: "root", DurationMs: 100, Children: []TimingEntry{
			{Name: "fast", DurationMs: 5},
			{Name: "slow", DurationMs: 95},
		}},
	}
	var buf bytes.Buffer
	_ = RenderTree(&buf, entries, RenderOptions{SortByDuration: true})
	out := buf.String()
	slowIdx := strings.Index(out, "slow")
	fastIdx := strings.Index(out, "fast")
	if slowIdx < 0 || fastIdx < 0 || slowIdx > fastIdx {
		t.Errorf("expected slow before fast when sorted by duration:\n%s", out)
	}
}

func TestRenderTreeMinDurationFilter(t *testing.T) {
	var buf bytes.Buffer
	_ = RenderTree(&buf, sample(), RenderOptions{MinDuration: 60 * time.Millisecond})
	out := buf.String()
	if strings.Contains(out, "collect") {
		t.Errorf("expected collect (20ms) to be filtered:\n%s", out)
	}
	if !strings.Contains(out, "kotlin") || !strings.Contains(out, "java") {
		t.Errorf("expected kotlin/java to remain:\n%s", out)
	}
}

func TestFormatDurUnits(t *testing.T) {
	cases := []struct {
		ms   int64
		want string
	}{
		{0, "0ns"}, // sub-µs
		{0, "0ns"}, // exact
	}
	_ = cases
	// Spot-check ranges.
	if got := formatDur(1500); !strings.Contains(got, "1.50s") {
		t.Errorf("formatDur(1500) = %q", got)
	}
	if got := formatDur(75_000); !strings.Contains(got, "1m15s") {
		t.Errorf("formatDur(75_000) = %q", got)
	}
	if got := formatDur(42); !strings.Contains(got, "42ms") {
		t.Errorf("formatDur(42) = %q", got)
	}
}

func TestSummaryFlattensAndSums(t *testing.T) {
	entries := []TimingEntry{
		{Name: "a", DurationMs: 10, Children: []TimingEntry{
			{Name: "b", DurationMs: 5},
			{Name: "a", DurationMs: 3},
		}},
		{Name: "b", DurationMs: 7},
	}
	got := Summary(entries, 0)
	totals := map[string]int64{}
	for _, e := range got {
		totals[e.Name] = e.DurationMs
	}
	if totals["a"] != 13 || totals["b"] != 12 {
		t.Errorf("Summary totals = %v, want a=13 b=12", totals)
	}
	if got[0].DurationMs < got[1].DurationMs {
		t.Errorf("Summary not sorted desc: %+v", got)
	}
}

func TestSummaryTopN(t *testing.T) {
	entries := []TimingEntry{
		{Name: "a", DurationMs: 10},
		{Name: "b", DurationMs: 8},
		{Name: "c", DurationMs: 5},
	}
	got := Summary(entries, 2)
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("Summary topN=2 = %+v", got)
	}
}

func TestRenderTreeEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderTree(&buf, nil, RenderOptions{}); err != nil {
		t.Fatalf("RenderTree(nil): %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output, got %q", buf.String())
	}
}
