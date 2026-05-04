// Command inspect is a TUI demonstration of internal/eventbus piped
// into the LiveView phase. A synthetic producer publishes events; the
// dashboard renders live counters, per-level/-kind breakdowns, and a
// rolling tail of the most recent events.
//
// Interactive (default):
//
//	inspect
//
// Headless:
//
//	inspect --scenario steady --duration 2s --yes
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kaeawc/golang-build/internal/eventbus"
	"github.com/kaeawc/golang-build/internal/tui"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	scenario := fs.String("scenario", "", "Scenario name (steady|bursty|noisy)")
	dur := fs.Duration("duration", 2*time.Second, "Run duration (headless only)")
	yes := fs.Bool("yes", false, "Skip the TUI")
	fs.BoolVar(yes, "y", false, "Alias for --yes")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *yes {
		return runHeadless(os.Stdout, *scenario, *dur)
	}
	return runInteractive()
}

func runInteractive() int {
	final, err := tui.Run(newModel())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if fm, ok := final.(model); ok && fm.err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", fm.err)
		return 1
	}
	return 0
}

func runHeadless(out io.Writer, name string, dur time.Duration) int {
	if name == "" {
		fmt.Fprintln(os.Stderr, "error: --yes requires --scenario")
		return 2
	}
	scenario := scenarioByName(name)
	if scenario == nil {
		fmt.Fprintf(os.Stderr, "error: unknown scenario %q (steady|bursty|noisy)\n", name)
		return 2
	}
	if dur <= 0 {
		fmt.Fprintln(os.Stderr, "error: duration must be positive")
		return 2
	}

	bus := eventbus.NewAsync[Event](eventbus.AsyncConfig{
		BufferSize:   64,
		DropWhenFull: true,
	})
	insp := NewInspector(bus, 10)
	insp.Start()

	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()
	scenario.Run(ctx, bus)

	insp.Stop()
	bus.Close()

	snap, _ := insp.Sample()
	fmt.Fprintln(out, renderFinal(snap.(Snapshot)))
	return 0
}
