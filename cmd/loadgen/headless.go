package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"charm.land/lipgloss/v2"
)

// runHeadless executes a load run with no TUI, useful for CI / scripts.
// Mirrors the TUI flow's effective behavior given the same flags.
func runHeadless(out io.Writer, url string, concurrency int, dur time.Duration) (Snapshot, error) {
	if url == "" {
		return Snapshot{}, fmt.Errorf("url is required")
	}
	if dur <= 0 {
		return Snapshot{}, fmt.Errorf("duration must be positive")
	}
	cfg := Config{
		URL:         url,
		Method:      http.MethodGet,
		Concurrency: concurrency,
		Duration:    dur,
	}
	eng := NewEngine(cfg)
	eng.Start(context.Background())
	snap := eng.Snapshot()
	// lipgloss v2 always emits ANSI; Fprintln downsamples for the writer
	// (plain text when out is not a terminal, honors NO_COLOR).
	_, _ = lipgloss.Fprintln(out, renderFinal(snap))
	return snap, nil
}
