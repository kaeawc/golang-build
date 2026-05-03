// Command onboard walks the user through generating a .env config for
// the server, demonstrating the internal/tui framework end-to-end.
//
// Interactive (default):
//
//	onboard                    # wizard in current dir
//	onboard ./somepath         # wizard against a different target
//
// Headless (no TTY required):
//
//	onboard --preset standard --yes
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kaeawc/golang-build/internal/tui"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("onboard", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	preset := fs.String("preset", "", "Preset to apply (bypasses interactive picker)")
	yes := fs.Bool("yes", false, "Accept preset defaults without prompting (requires --preset)")
	fs.BoolVar(yes, "y", false, "Alias for --yes")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	abs, code := resolveTarget(fs.Args())
	if code != 0 {
		return code
	}

	if *yes {
		return runHeadlessMode(abs, *preset)
	}
	return runInteractive(abs)
}

func resolveTarget(args []string) (string, int) {
	target := "."
	if len(args) > 0 {
		target = args[0]
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return "", 2
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "error: target %q is not a directory\n", target)
		return "", 2
	}
	return abs, 0
}

func runHeadlessMode(abs, preset string) int {
	if preset == "" {
		fmt.Fprintln(os.Stderr, "error: --yes requires --preset")
		return 2
	}
	if _, err := runHeadless(os.Stdout, abs, preset); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runInteractive(abs string) int {
	final, err := tui.Run(newModel(abs))
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
