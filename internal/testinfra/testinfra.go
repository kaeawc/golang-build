// Package testinfra starts real Postgres and Valkey containers for
// integration tests via testcontainers-go.
//
// Integration tests live in `//go:build integration` files so `make test`
// stays Docker-free; run them with `make test-integration`. Each package
// starts its containers once, from TestMain:
//
//	var stack *testinfra.Stack
//
//	func TestMain(m *testing.M) {
//		os.Exit(testinfra.Run(m, func(ctx context.Context, s *testinfra.Stack) error {
//			stack = s
//			_, err := s.StartPostgres(ctx)
//			return err
//		}))
//	}
//
// Tests then call stack.Postgres.NewDatabase(t) for an isolated, freshly
// created database (optionally migrated), so they never share rows.
//
// Image tags match docker-compose.yml.
package testinfra

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"

	"github.com/kaeawc/golang-build/internal/db"
)

// Images, kept in step with docker-compose.yml.
const (
	PostgresImage = "postgres:17"
	ValkeyImage   = "valkey/valkey:8"
)

// Timeouts. Startup covers an image pull on a cold CI runner.
const (
	StartupTimeout   = 3 * time.Minute
	TeardownTimeout  = time.Minute
	statementTimeout = 30 * time.Second
)

// Credentials match docker-compose.yml so a failing test's DSN looks
// familiar.
const (
	pgUser     = "appuser"
	pgPassword = "apppass"
	pgDatabase = "appdb"
)

// Stack is the set of containers a package's tests share. Fields are nil
// until the corresponding Start method succeeds.
type Stack struct {
	Postgres *Postgres
	Valkey   *Valkey

	containers []testcontainers.Container
}

// Run starts the containers setup asks for, runs the package's tests, then
// terminates every container. It returns the exit code for os.Exit. If
// setup fails, tests are not run and the code is 1.
func Run(m *testing.M, setup func(ctx context.Context, s *Stack) error) int {
	s := &Stack{}
	defer s.terminate()

	ctx, cancel := context.WithTimeout(context.Background(), StartupTimeout)
	err := setup(ctx, s)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "testinfra: setup: %v\n", err)
		return 1
	}
	return m.Run()
}

func (s *Stack) terminate() {
	for _, c := range slices.Backward(s.containers) {
		ctx, cancel := context.WithTimeout(context.Background(), TeardownTimeout)
		err := c.Terminate(ctx)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "testinfra: terminate: %v\n", err)
		}
	}
}

// track registers c for teardown even when the caller goes on to fail, so
// a half-started container is not left behind.
func (s *Stack) track(c testcontainers.Container) {
	s.containers = append(s.containers, c)
}

// Postgres is a running Postgres container.
type Postgres struct {
	// DSN connects to the container's default database as its superuser.
	DSN string

	seq atomic.Int64
}

// StartPostgres starts a Postgres container and stores it in s.Postgres.
func (s *Stack) StartPostgres(ctx context.Context) (*Postgres, error) {
	c, err := tcpostgres.Run(ctx, PostgresImage,
		tcpostgres.WithDatabase(pgDatabase),
		tcpostgres.WithUsername(pgUser),
		tcpostgres.WithPassword(pgPassword),
		tcpostgres.BasicWaitStrategies(),
	)
	if c != nil {
		s.track(c)
	}
	if err != nil {
		return nil, fmt.Errorf("start postgres: %w", err)
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("postgres dsn: %w", err)
	}
	s.Postgres = &Postgres{DSN: dsn}
	return s.Postgres, nil
}

// NewDatabase creates an empty database for t, drops it when t ends, and
// returns its DSN. Each call gets its own database, so tests may run in
// parallel without sharing state.
func (p *Postgres) NewDatabase(t testing.TB) string {
	t.Helper()
	name := fmt.Sprintf("test_%d_%d", os.Getpid(), p.seq.Add(1))
	p.admin(t, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	t.Cleanup(func() {
		p.admin(t, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	})
	dsn, err := withDatabase(p.DSN, name)
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}

// NewMigratedDatabase is NewDatabase with the app's sql/migrations applied
// through db.Migrate, the same path the server takes at boot.
func (p *Postgres) NewMigratedDatabase(t testing.TB) string {
	t.Helper()
	dsn := p.NewDatabase(t)
	if err := db.Migrate("file://"+MigrationsDir(), dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return dsn
}

func (p *Postgres) admin(t testing.TB, sql string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), statementTimeout)
	defer cancel()
	conn, err := pgx.Connect(ctx, p.DSN)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// withDatabase swaps the database name (the URL path) in a postgres:// DSN.
func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}

// Valkey is a running Valkey container.
type Valkey struct {
	// Addr is host:port, the form cache.New and VALKEY_ADDR take.
	Addr string
}

// StartValkey starts a Valkey container and stores it in s.Valkey.
func (s *Stack) StartValkey(ctx context.Context) (*Valkey, error) {
	c, err := tcvalkey.Run(ctx, ValkeyImage)
	if c != nil {
		s.track(c)
	}
	if err != nil {
		return nil, fmt.Errorf("start valkey: %w", err)
	}
	addr, err := c.PortEndpoint(ctx, "6379/tcp", "")
	if err != nil {
		return nil, fmt.Errorf("valkey addr: %w", err)
	}
	s.Valkey = &Valkey{Addr: addr}
	return s.Valkey, nil
}

// MigrationsDir is the absolute path of the repository's sql/migrations,
// resolved from this file so tests work from any package directory.
func MigrationsDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic(errors.New("testinfra: cannot locate source file"))
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "sql", "migrations")
}
