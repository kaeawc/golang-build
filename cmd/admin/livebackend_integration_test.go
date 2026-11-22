//go:build integration

package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/kaeawc/golang-build/internal/db"
	"github.com/kaeawc/golang-build/internal/testinfra"
)

var stack *testinfra.Stack

func TestMain(m *testing.M) {
	os.Exit(testinfra.Run(m, func(ctx context.Context, s *testinfra.Stack) error {
		stack = s
		if _, err := s.StartPostgres(ctx); err != nil {
			return err
		}
		_, err := s.StartValkey(ctx)
		return err
	}))
}

// newLive builds a LiveBackend the way main does: from DATABASE_URL and
// VALKEY_ADDR. Tests using it cannot run in parallel (t.Setenv).
func newLive(t *testing.T, dsn, valkeyAddr string) *LiveBackend {
	t.Helper()
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("VALKEY_ADDR", valkeyAddr)
	lb, err := NewLiveBackend(t.Context())
	if err != nil {
		t.Fatalf("NewLiveBackend: %v", err)
	}
	if lb == nil {
		t.Fatal("NewLiveBackend returned nil with DATABASE_URL set")
	}
	t.Cleanup(lb.Close)
	return lb
}

func seedUsers(t *testing.T, dsn string, names ...string) []db.User {
	t.Helper()
	q := db.New(testinfra.NewPool(t, dsn))
	out := make([]db.User, 0, len(names))
	for _, n := range names {
		u, err := q.CreateUser(t.Context(), n)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, u)
	}
	return out
}

func TestLiveBackendListUsers(t *testing.T) {
	dsn := stack.Postgres.NewMigratedDatabase(t)
	lb := newLive(t, dsn, "")

	users, err := lb.ListUsers(t.Context())
	if err != nil || len(users) != 0 {
		t.Fatalf("ListUsers on empty db = %+v, %v", users, err)
	}

	seeded := seedUsers(t, dsn, "alice", "bob")
	users, err = lb.ListUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0] != User(seeded[0]) || users[1] != User(seeded[1]) {
		t.Errorf("ListUsers = %+v, want %+v", users, seeded)
	}
}

func TestLiveBackendListUsersWithoutSchema(t *testing.T) {
	lb := newLive(t, stack.Postgres.NewDatabase(t), "")
	if _, err := lb.ListUsers(t.Context()); err == nil {
		t.Error("ListUsers without users table: want error")
	}
}

func TestLiveBackendHealthchecks(t *testing.T) {
	lb := newLive(t, stack.Postgres.NewDatabase(t), stack.Valkey.Addr)

	probes, err := lb.RunHealthchecks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(probes) != 2 {
		t.Fatalf("probes = %+v, want db + valkey", probes)
	}
	for _, p := range probes {
		// The valkey probe reads a key that does not exist: a nil reply
		// must count as healthy.
		if !p.Healthy || p.Err != "" || p.Latency <= 0 {
			t.Errorf("probe %+v, want healthy", p)
		}
	}
	if probes[0].Name != "db" || probes[1].Name != "valkey" {
		t.Errorf("probe names = %s, %s", probes[0].Name, probes[1].Name)
	}
}

func TestLiveBackendHealthchecksWithoutValkey(t *testing.T) {
	lb := newLive(t, stack.Postgres.NewDatabase(t), "")
	probes, err := lb.RunHealthchecks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(probes) != 1 || probes[0].Name != "db" || !probes[0].Healthy {
		t.Errorf("probes = %+v, want only a healthy db probe", probes)
	}
}

func TestLiveBackendUnhealthy(t *testing.T) {
	lb := newLive(t, stack.Postgres.NewDatabase(t), stack.Valkey.Addr)
	// Closing the pool and cache makes both round-trips fail without
	// touching the shared containers.
	lb.pool.Close()
	lb.cache.Close()

	probes, err := lb.RunHealthchecks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range probes {
		if p.Healthy || p.Err == "" {
			t.Errorf("probe %+v, want unhealthy with an error", p)
		}
		if !strings.Contains(formatProbe(p), "FAIL") {
			t.Errorf("formatProbe(%+v) = %q", p, formatProbe(p))
		}
	}
}

func TestNewLiveBackendUnreachableValkey(t *testing.T) {
	t.Setenv("DATABASE_URL", stack.Postgres.NewDatabase(t))
	t.Setenv("VALKEY_ADDR", "127.0.0.1:1")
	lb, err := NewLiveBackend(t.Context())
	if err == nil {
		lb.Close()
		t.Fatal("NewLiveBackend with dead VALKEY_ADDR: want error")
	}
}

func TestNewLiveBackendBadDSN(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://%zz")
	t.Setenv("VALKEY_ADDR", "")
	if _, err := NewLiveBackend(t.Context()); err == nil {
		t.Error("NewLiveBackend with unparseable DSN: want error")
	}
}

func TestHeadlessCommandsAgainstLiveBackend(t *testing.T) {
	dsn := stack.Postgres.NewMigratedDatabase(t)
	seedUsers(t, dsn, "carol")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("VALKEY_ADDR", stack.Valkey.Addr)

	backend, closer, err := buildBackend(false)
	if err != nil {
		t.Fatal(err)
	}
	defer closer()
	if _, ok := backend.(*LiveBackend); !ok {
		t.Fatalf("buildBackend with DATABASE_URL = %T, want *LiveBackend", backend)
	}

	var out bytes.Buffer
	if code := runHeadlessListUsers(&out, backend); code != 0 || !strings.Contains(out.String(), "\tcarol") {
		t.Errorf("list-users exit=%d output=%q", code, out.String())
	}
	out.Reset()
	if code := runHeadlessHealthchecks(&out, backend); code != 0 ||
		!strings.Contains(out.String(), "db ok") || !strings.Contains(out.String(), "valkey ok") {
		t.Errorf("healthchecks exit=%d output=%q", code, out.String())
	}
}
