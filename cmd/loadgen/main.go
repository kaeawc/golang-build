// Command loadgen is a TUI HTTP load generator that demonstrates the
// internal/tui LiveView (live dashboard) phase.
//
// Interactive (default):
//
//	loadgen http://localhost:8080/healthz
//
// Headless:
//
//	loadgen --concurrency 16 --duration 5s --yes http://localhost:8080/healthz
//
// Set OTEL_EXPORTER_OTLP_ENDPOINT to export client spans; trace context
// propagates to the target so its server spans join the same traces.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/kaeawc/golang-build/internal/telemetry"
	"github.com/kaeawc/golang-build/internal/tui"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("loadgen", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	conc := fs.Int("concurrency", 16, "Number of concurrent workers (headless only)")
	dur := fs.Duration("duration", 5*time.Second, "How long to run (headless only)")
	yes := fs.Bool("yes", false, "Run headless with the supplied flags")
	fs.BoolVar(yes, "y", false, "Alias for --yes")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(fs.Args()) == 0 {
		fmt.Fprintln(os.Stderr, "error: target URL is required")
		return 2
	}
	url := fs.Args()[0]

	// Telemetry errors go nowhere in TUI mode: stderr writes would corrupt
	// the display.
	telemetryLog := slog.New(slog.DiscardHandler)
	if *yes {
		telemetryLog = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	stopTelemetry, err := telemetry.Setup(context.Background(), telemetry.Service{Name: "golang-build-loadgen", Logger: telemetryLog})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer func() {
		// Bounded: exporters retry for up to a minute when the collector
		// is down.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = stopTelemetry(ctx)
	}()

	if *yes {
		if _, err := runHeadless(os.Stdout, url, *conc, *dur); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}

	final, err := tui.Run(newModel(url))
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
