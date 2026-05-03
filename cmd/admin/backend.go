package main

import (
	"context"
	"fmt"
	"time"
)

// User is the admin-facing view of a row. Mirrors internal/db.User but
// kept local so the TUI doesn't depend on the sqlc-generated package.
type User struct {
	ID   int64
	Name string
}

// Probe is one healthcheck result.
type Probe struct {
	Name    string
	Healthy bool
	Latency time.Duration
	Err     string
}

// Backend abstracts the data the admin TUI displays. The real backend
// talks to Postgres/Valkey; the fake serves canned data and is used in
// tests + when env vars are missing.
type Backend interface {
	ListUsers(ctx context.Context) ([]User, error)
	RunHealthchecks(ctx context.Context) ([]Probe, error)
}

// FakeBackend is an in-memory Backend used by tests and when no
// connection env vars are set.
type FakeBackend struct {
	Users  []User
	Probes []Probe
	Err    error // injected error for negative tests
}

func (f *FakeBackend) ListUsers(ctx context.Context) ([]User, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	out := make([]User, len(f.Users))
	copy(out, f.Users)
	return out, nil
}

func (f *FakeBackend) RunHealthchecks(ctx context.Context) ([]Probe, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	out := make([]Probe, len(f.Probes))
	copy(out, f.Probes)
	return out, nil
}

// NewFakeBackend constructs a FakeBackend with a small canned dataset.
// Useful as a default when no real connection is available.
func NewFakeBackend() *FakeBackend {
	return &FakeBackend{
		Users: []User{
			{ID: 1, Name: "alice"},
			{ID: 2, Name: "bob"},
			{ID: 3, Name: "carol"},
		},
		Probes: []Probe{
			{Name: "db", Healthy: true, Latency: 4 * time.Millisecond},
			{Name: "valkey", Healthy: true, Latency: 1 * time.Millisecond},
			{Name: "downstream-api", Healthy: false, Latency: 30 * time.Millisecond, Err: "503"},
		},
	}
}

// formatProbe renders a one-line summary for a probe row.
func formatProbe(p Probe) string {
	if p.Healthy {
		return fmt.Sprintf("%s ok (%s)", p.Name, p.Latency)
	}
	if p.Err != "" {
		return fmt.Sprintf("%s FAIL (%s): %s", p.Name, p.Latency, p.Err)
	}
	return fmt.Sprintf("%s FAIL (%s)", p.Name, p.Latency)
}
