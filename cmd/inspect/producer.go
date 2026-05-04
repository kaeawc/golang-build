package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/kaeawc/golang-build/internal/eventbus"
)

// Scenario describes a synthetic event source. Different scenarios are
// useful for exercising different bus behaviors: steady throughput,
// bursty bursts that may cause drops, error-heavy mixes for stat
// rendering.
type Scenario struct {
	Name        string
	Description string
	// Run publishes events to bus until ctx is cancelled. Implementations
	// must respect cancellation promptly.
	Run func(ctx context.Context, bus *eventbus.Async[Event])
}

// Scenarios is the canonical ordered list shown in the picker.
var Scenarios = []Scenario{
	{
		Name:        "steady",
		Description: "10 events/sec, info-level only",
		Run:         runSteady,
	},
	{
		Name:        "bursty",
		Description: "bursts of 50 events every 2s",
		Run:         runBursty,
	},
	{
		Name:        "noisy",
		Description: "20/sec mixed info/warn/error",
		Run:         runNoisy,
	},
}

// scenarioByName returns the scenario with the given name, or nil if
// not found.
func scenarioByName(name string) *Scenario {
	for i := range Scenarios {
		if Scenarios[i].Name == name {
			return &Scenarios[i]
		}
	}
	return nil
}

func runSteady(ctx context.Context, bus *eventbus.Async[Event]) {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n++
			bus.Publish(Event{
				At:      time.Now(),
				Kind:    "request",
				Level:   "info",
				Message: fmt.Sprintf("tick %d", n),
			})
		}
	}
}

func runBursty(ctx context.Context, bus *eventbus.Async[Event]) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	burst := 0
	emit := func() {
		burst++
		for j := 0; j < 50; j++ {
			select {
			case <-ctx.Done():
				return
			default:
			}
			bus.Publish(Event{
				At:      time.Now(),
				Kind:    "batch",
				Level:   "info",
				Message: fmt.Sprintf("burst %d item %d", burst, j),
			})
		}
	}
	emit() // initial burst so the user sees something quickly
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			emit()
		}
	}
}

func runNoisy(ctx context.Context, bus *eventbus.Async[Event]) {
	// Deterministic enough for snapshot tests; uses math/rand/v2 (no
	// crypto requirements here).
	// #nosec G404 -- synthetic event stream for demo, not security-sensitive
	r := rand.New(rand.NewPCG(1, 2))
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	n := 0
	kinds := []string{"request", "db", "cache", "auth"}
	levels := []string{"info", "info", "info", "info", "warn", "warn", "error"}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n++
			bus.Publish(Event{
				At:      time.Now(),
				Kind:    kinds[r.IntN(len(kinds))],
				Level:   levels[r.IntN(len(levels))],
				Message: fmt.Sprintf("noisy %d", n),
			})
		}
	}
}
