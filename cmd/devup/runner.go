package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kaeawc/golang-build/internal/proc"
)

// listComposeServices returns the service names declared in the
// project's docker-compose.yml.
func listComposeServices(ctx context.Context, r proc.Runner) ([]string, error) {
	res, err := r.Run(ctx, proc.Cmd{
		Name: "docker",
		Args: []string{"compose", "config", "--services"},
	})
	if err != nil {
		return nil, fmt.Errorf("docker compose config --services: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("docker compose config --services exited %d: %s",
			res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	lines := strings.Split(strings.TrimSpace(string(res.Stdout)), "\n")
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

// composeAction runs `docker compose <action> <service>` and returns
// the combined stdout+stderr as a single string.
func composeAction(ctx context.Context, r proc.Runner, action, service string) (string, error) {
	args := []string{"compose", action}
	if service != "" {
		args = append(args, service)
	}
	if action == "up" {
		args = append(args, "-d")
	}
	res, err := r.Run(ctx, proc.Cmd{Name: "docker", Args: args})
	out := string(res.Stdout) + string(res.Stderr)
	if err != nil {
		return out, fmt.Errorf("docker compose %s %s: %w", action, service, err)
	}
	if res.ExitCode != 0 {
		return out, fmt.Errorf("docker compose %s %s exited %d", action, service, res.ExitCode)
	}
	return out, nil
}
