// Command devup is a TUI wrapper for docker compose: pick a service,
// pick an action (ps, up, stop, restart, logs), see the output.
// Demonstrates the framework with subprocess management abstracted
// through a Runner interface (FakeRunner for tests, ExecRunner in prod).
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
	fs := flag.NewFlagSet("devup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	listOnly := fs.Bool("list", false, "Headless: list services and exit")
	action := fs.String("action", "", "Headless: action to run (ps|up|stop|restart|logs)")
	service := fs.String("service", "", "Headless: service to target")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	runner := ExecRunner{}

	if *listOnly {
		return runHeadlessList(os.Stdout, runner)
	}
	if *action != "" {
		return runHeadlessAction(os.Stdout, runner, *action, *service)
	}
	return runInteractive(runner)
}

func runInteractive(runner Runner) int {
	final, err := tui.Run(newModel(runner))
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

func runHeadlessList(out io.Writer, runner Runner) int {
	services, err := listComposeServices(context.Background(), runner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	for _, s := range services {
		fmt.Fprintln(out, s)
	}
	return 0
}

func runHeadlessAction(out io.Writer, runner Runner, action, service string) int {
	if !validAction(action) {
		fmt.Fprintf(os.Stderr, "error: invalid action %q (valid: ps, up, stop, restart, logs)\n", action)
		return 2
	}
	output, err := composeAction(context.Background(), runner, action, service)
	fmt.Fprint(out, output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func validAction(a string) bool {
	for _, valid := range composeActions {
		if a == valid {
			return true
		}
	}
	return false
}
