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
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

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
