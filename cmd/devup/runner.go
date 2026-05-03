package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Runner abstracts subprocess execution so tests can swap in a fake.
// The real impl shells out to docker compose; the fake records calls
// and returns canned output.
type Runner interface {
	// Run executes the command with args, returning combined stdout +
	// stderr. The returned error is non-nil if the process exits non-zero.
	Run(ctx context.Context, command string, args ...string) (string, error)
}

// ExecRunner is a Runner backed by os/exec.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, command string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// FakeRunner is an in-memory Runner for tests. Calls are recorded in
// order; output and errors are looked up per (command, first-arg) pair.
type FakeRunner struct {
	mu      sync.Mutex
	calls   []FakeCall
	outputs map[string]string // key = "cmd subcmd" e.g. "docker compose"
	errors  map[string]error
	delay   time.Duration
}

// FakeCall records one Run invocation.
type FakeCall struct {
	Command string
	Args    []string
}

// NewFakeRunner returns a FakeRunner with empty maps.
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{
		outputs: make(map[string]string),
		errors:  make(map[string]error),
	}
}

// SetOutput configures the response for a given (command, subcommand) pair.
// subcommand may be empty to match any args.
func (f *FakeRunner) SetOutput(command, subcommand, output string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := command
	if subcommand != "" {
		key = command + " " + subcommand
	}
	f.outputs[key] = output
	if err != nil {
		f.errors[key] = err
	}
}

// SetDelay forces every Run call to sleep before returning. Useful for
// exercising context cancellation.
func (f *FakeRunner) SetDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

// Calls returns a copy of the recorded call log.
func (f *FakeRunner) Calls() []FakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FakeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *FakeRunner) Run(ctx context.Context, command string, args ...string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{Command: command, Args: append([]string(nil), args...)})
	delay := f.delay
	out, err := f.lookupLocked(command, args)
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return out, err
}

func (f *FakeRunner) lookupLocked(command string, args []string) (string, error) {
	if len(args) > 0 {
		key := command + " " + args[0]
		if out, ok := f.outputs[key]; ok {
			return out, f.errors[key]
		}
	}
	if out, ok := f.outputs[command]; ok {
		return out, f.errors[command]
	}
	return "", fmt.Errorf("fake runner: no canned response for %s %v", command, args)
}

// ---------- domain helpers --------------------------------------------------

// listComposeServices returns the service names declared in the
// project's docker-compose.yml.
func listComposeServices(ctx context.Context, r Runner) ([]string, error) {
	out, err := r.Run(ctx, "docker", "compose", "config", "--services")
	if err != nil {
		return nil, fmt.Errorf("docker compose config --services: %w (%s)", err, strings.TrimSpace(out))
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	services := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			services = append(services, l)
		}
	}
	if len(services) == 0 {
		return nil, errors.New("no services found in docker compose config")
	}
	return services, nil
}

// composeAction runs `docker compose <action> <service>` and returns the output.
func composeAction(ctx context.Context, r Runner, action, service string) (string, error) {
	args := []string{"compose", action}
	if service != "" {
		args = append(args, service)
	}
	if action == "up" {
		args = append(args, "-d")
	}
	out, err := r.Run(ctx, "docker", args...)
	if err != nil {
		return out, fmt.Errorf("docker compose %s %s: %w", action, service, err)
	}
	return out, nil
}
