// Command scaffold generates boilerplate files (handler, middleware,
// or internal package) under the supplied repo root. Demonstrates the
// internal/tui TextInput phase.
//
// Interactive (default):
//
//	scaffold                 # walks you through a generation
//	scaffold ./somerepo      # target a different repo root
//
// Headless:
//
//	scaffold --kind handler --name users --yes
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kaeawc/golang-build/internal/tui"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("scaffold", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	kind := fs.String("kind", "", "Kind to generate (handler|middleware|package)")
	name := fs.String("name", "", "Name (lowercase identifier)")
	yes := fs.Bool("yes", false, "Skip the TUI; requires --kind and --name")
	fs.BoolVar(yes, "y", false, "Alias for --yes")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	repoRoot, code := resolveRoot(fs.Args())
	if code != 0 {
		return code
	}

	if *yes {
		return runHeadless(os.Stdout, repoRoot, *kind, *name)
	}
	return runInteractive(repoRoot)
}

func resolveRoot(args []string) (string, int) {
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return "", 2
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "error: target %q is not a directory\n", root)
		return "", 2
	}
	return abs, 0
}

func runInteractive(repoRoot string) int {
	final, err := tui.Run(newModel(repoRoot))
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

func runHeadless(out io.Writer, repoRoot, kind, name string) int {
	if kind == "" || name == "" {
		fmt.Fprintln(os.Stderr, "error: --yes requires --kind and --name")
		return 2
	}
	if err := validateKind(kind); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	if err := validateName(name); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	spec := Spec{Kind: Kind(kind), Name: name}
	path, err := write(repoRoot, spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "wrote %s\n", path)
	return 0
}

func validateKind(k string) error {
	for _, valid := range Kinds {
		if string(valid) == k {
			return nil
		}
	}
	return fmt.Errorf("unknown kind %q (valid: handler, middleware, package)", k)
}
