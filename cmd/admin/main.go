// Command admin is a TUI for inspecting a running deployment of the
// server: list users, run healthchecks, etc. Demonstrates the dynamic
// Picker pattern (Picker fed by an AsyncTask result, not a static slice).
//
// When DATABASE_URL is set, talks to the real database; otherwise falls
// back to a fake backend with canned data so the wizard remains usable
// for demos.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kaeawc/golang-build/internal/tui"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("admin", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fake := fs.Bool("fake", false, "Force the in-memory fake backend")
	listUsers := fs.Bool("list-users", false, "Headless: print users and exit")
	runHC := fs.Bool("healthchecks", false, "Headless: print healthcheck results and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	backend, closer, err := buildBackend(*fake)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer closer()

	if *listUsers {
		return runHeadlessListUsers(os.Stdout, backend)
	}
	if *runHC {
		return runHeadlessHealthchecks(os.Stdout, backend)
	}

	return runInteractive(backend)
}

func buildBackend(fake bool) (Backend, func(), error) {
	if fake {
		return NewFakeBackend(), func() {}, nil
	}
	live, err := NewLiveBackend(context.Background())
	if err != nil {
		return nil, nil, err
	}
	if live == nil {
		return NewFakeBackend(), func() {}, nil
	}
	return live, live.Close, nil
}

func runInteractive(backend Backend) int {
	final, err := tui.Run(newModel(backend))
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

func runHeadlessListUsers(out io.Writer, backend Backend) int {
	users, err := backend.ListUsers(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	for _, u := range users {
		fmt.Fprintf(out, "%d\t%s\n", u.ID, u.Name)
	}
	return 0
}

func runHeadlessHealthchecks(out io.Writer, backend Backend) int {
	probes, err := backend.RunHealthchecks(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	for _, p := range probes {
		fmt.Fprintln(out, formatProbe(p))
	}
	return 0
}
