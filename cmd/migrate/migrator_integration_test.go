//go:build integration

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"

	"github.com/kaeawc/golang-build/internal/testinfra"
)

var stack *testinfra.Stack

func TestMain(m *testing.M) {
	os.Exit(testinfra.Run(m, func(ctx context.Context, s *testinfra.Stack) error {
		stack = s
		_, err := s.StartPostgres(ctx)
		return err
	}))
}

// writeMigrations creates a source dir with n migrations, each creating
// (and on down, dropping) table tN.
func writeMigrations(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	for i := 1; i <= n; i++ {
		base := filepath.Join(dir, strings.Replace("00000N_tN", "N", string(rune('0'+i)), 2))
		if err := os.WriteFile(base+".up.sql", []byte("CREATE TABLE t"+string(rune('0'+i))+" (id int);"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(base+".down.sql", []byte("DROP TABLE t"+string(rune('0'+i))+";"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func tableExists(t *testing.T, dsn, table string) bool {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var ok bool
	if err := conn.QueryRow(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

func mustStatus(t *testing.T, m Migrator) Status {
	t.Helper()
	st, err := m.Status(t.Context())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	return st
}

func TestLiveMigratorStepUpAndDown(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dsn := stack.Postgres.NewDatabase(t)
	m := NewLiveMigrator(writeMigrations(t, 3), dsn)

	st := mustStatus(t, m)
	if st.HasVersion || st.Dirty || len(st.Migrations) != 3 || len(st.Pending()) != 3 {
		t.Fatalf("fresh Status = %+v, want no version, 3 pending", st)
	}

	if err := m.UpOne(ctx); err != nil {
		t.Fatalf("UpOne: %v", err)
	}
	st = mustStatus(t, m)
	if !st.HasVersion || st.CurrentVersion != 1 || len(st.Pending()) != 2 {
		t.Errorf("after UpOne Status = %+v, want version 1, 2 pending", st)
	}
	if !tableExists(t, dsn, "t1") || tableExists(t, dsn, "t2") {
		t.Error("UpOne applied the wrong migrations")
	}

	if err := m.UpAll(ctx); err != nil {
		t.Fatalf("UpAll: %v", err)
	}
	if st = mustStatus(t, m); st.CurrentVersion != 3 || len(st.Pending()) != 0 {
		t.Errorf("after UpAll Status = %+v, want version 3, 0 pending", st)
	}
	if err := m.UpAll(ctx); err != nil {
		t.Errorf("UpAll at head: %v, want no-op", err)
	}

	if err := m.DownOne(ctx); err != nil {
		t.Fatalf("DownOne: %v", err)
	}
	if st = mustStatus(t, m); st.CurrentVersion != 2 || tableExists(t, dsn, "t3") {
		t.Errorf("after DownOne Status = %+v (t3 exists=%v), want version 2", st, tableExists(t, dsn, "t3"))
	}

	for range 2 {
		if err := m.DownOne(ctx); err != nil {
			t.Fatalf("DownOne: %v", err)
		}
	}
	if st = mustStatus(t, m); st.HasVersion || len(st.Pending()) != 3 {
		t.Errorf("after reverting all Status = %+v, want no version", st)
	}
}

// The TUI and headless runner treat "nothing to do" as success. The fake
// models that; the live migrator must agree at both ends of the range.
func TestLiveMigratorNoChangeAtEnds(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := NewLiveMigrator(writeMigrations(t, 1), stack.Postgres.NewDatabase(t))

	if err := m.DownOne(ctx); err != nil && !isNoChange(err) {
		t.Errorf("DownOne with nothing applied: %v", err)
	}
	if err := m.UpOne(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.UpOne(ctx); err != nil && !isNoChange(err) {
		t.Errorf("UpOne at head: %v", err)
	}
	if st := mustStatus(t, m); st.CurrentVersion != 1 || st.Dirty {
		t.Errorf("Status = %+v, want version 1 clean", st)
	}
}

func isNoChange(err error) bool { return errors.Is(err, migrate.ErrNoChange) }

func TestLiveMigratorRepoMigrations(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dsn := stack.Postgres.NewDatabase(t)
	m := NewLiveMigrator(testinfra.MigrationsDir(), dsn)

	if err := m.UpAll(ctx); err != nil {
		t.Fatal(err)
	}
	if !tableExists(t, dsn, "users") {
		t.Fatal("users table missing after UpAll")
	}
	if err := m.DownOne(ctx); err != nil {
		t.Fatal(err)
	}
	if tableExists(t, dsn, "users") {
		t.Error("users table still exists after DownOne")
	}
}

func TestLiveMigratorDirtyAfterFailure(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := writeMigrations(t, 1)
	if err := os.WriteFile(filepath.Join(dir, "000002_broken.up.sql"), []byte("NOT SQL;"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewLiveMigrator(dir, stack.Postgres.NewDatabase(t))

	if err := m.UpAll(ctx); err == nil {
		t.Fatal("UpAll with broken migration: want error")
	}
	st := mustStatus(t, m)
	if st.CurrentVersion != 2 || !st.Dirty {
		t.Errorf("Status = %+v, want version 2 dirty", st)
	}
}

func TestLiveMigratorErrors(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dsn := stack.Postgres.NewDatabase(t)

	missing := NewLiveMigrator(filepath.Join(t.TempDir(), "nope"), dsn)
	if _, err := missing.Status(ctx); err == nil {
		t.Error("Status with missing dir: want error")
	}
	for name, f := range map[string]func(context.Context) error{
		"UpAll": missing.UpAll, "UpOne": missing.UpOne, "DownOne": missing.DownOne,
	} {
		if err := f(ctx); err == nil {
			t.Errorf("%s with missing dir: want error", name)
		}
	}

	// The database is at a version the source dir has no file for. Steps
	// wraps os.ErrNotExist here; that must not pass for "no change".
	if err := NewLiveMigrator(writeMigrations(t, 2), dsn).UpAll(ctx); err != nil {
		t.Fatal(err)
	}
	behind := NewLiveMigrator(writeMigrations(t, 1), dsn)
	if err := behind.UpOne(ctx); err == nil {
		t.Error("UpOne with applied version missing from source: want error")
	}
	if err := behind.DownOne(ctx); err == nil {
		t.Error("DownOne with applied version missing from source: want error")
	}

	// Port 1 refuses connections: open fails before any migration runs.
	unreachable := NewLiveMigrator(writeMigrations(t, 1), "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=2")
	if _, err := unreachable.Status(ctx); err == nil {
		t.Error("Status against unreachable db: want error")
	}
}

func TestHeadlessAgainstLiveDB(t *testing.T) {
	t.Parallel()
	dir := writeMigrations(t, 2)
	m := NewLiveMigrator(dir, stack.Postgres.NewDatabase(t))

	var out bytes.Buffer
	if code := runHeadlessStatus(&out, m); code != 0 {
		t.Fatalf("status exit = %d", code)
	}
	if !strings.Contains(out.String(), "pending: 2") {
		t.Errorf("status output:\n%s", out.String())
	}

	steps := []struct {
		a    action
		want string
	}{
		{actUpOne, "→ v1"},
		{actUpOne, "→ v2"},
		{actUpOne, "→ v2"}, // at head: still success
		{actDownOne, "→ v1"},
		{actUpAll, "→ v2"},
	}
	for _, s := range steps {
		out.Reset()
		if code := runHeadlessAction(&out, m, s.a); code != 0 {
			t.Fatalf("%s exit = %d", actionLabels[s.a].label, code)
		}
		if !strings.Contains(out.String(), s.want) {
			t.Errorf("%s output = %q, want it to contain %q", actionLabels[s.a].label, out.String(), s.want)
		}
	}
}
