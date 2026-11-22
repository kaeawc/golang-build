//go:build integration

package db_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kaeawc/golang-build/internal/db"
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

// latestMigration is the highest version in sql/migrations.
func latestMigration(t *testing.T) int64 {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(testinfra.MigrationsDir(), "*.up.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}
	var latest int64
	for _, f := range files {
		v, err := strconv.ParseInt(strings.SplitN(filepath.Base(f), "_", 2)[0], 10, 64)
		if err != nil {
			t.Fatalf("migration %s: %v", f, err)
		}
		latest = max(latest, v)
	}
	return latest
}

func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()
	dsn := stack.Postgres.NewDatabase(t)
	src := "file://" + testinfra.MigrationsDir()

	if err := db.Migrate(src, dsn); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// The server migrates on every boot; a second run must be a no-op.
	if err := db.Migrate(src, dsn); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var version int64
	var dirty bool
	if err := testinfra.NewPool(t, dsn).QueryRow(t.Context(),
		`SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	if want := latestMigration(t); version != want || dirty {
		t.Errorf("schema_migrations = (%d, dirty=%v), want (%d, false)", version, dirty, want)
	}
}

func TestMigrateAcceptsPostgresqlScheme(t *testing.T) {
	t.Parallel()
	dsn := stack.Postgres.NewDatabase(t)
	// libpq accepts both schemes; Migrate rewrites either to pgx5://.
	alt := "postgresql://" + dsn[len("postgres://"):]
	if err := db.Migrate("file://"+testinfra.MigrationsDir(), alt); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateErrors(t *testing.T) {
	t.Parallel()
	dsn := stack.Postgres.NewDatabase(t)
	if err := db.Migrate("file:///does/not/exist", dsn); err == nil {
		t.Error("missing source dir: want error")
	}
	bad := t.TempDir()
	if err := os.WriteFile(bad+"/000001_broken.up.sql", []byte("NOT SQL;"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate("file://"+bad, dsn); err == nil {
		t.Error("broken migration: want error")
	}
}

func TestQueries(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q := db.New(testinfra.NewPool(t, stack.Postgres.NewMigratedDatabase(t)))

	users, err := q.GetUsers(ctx)
	if err != nil || len(users) != 0 {
		t.Fatalf("GetUsers on empty table = %v, %v", users, err)
	}

	var created []db.User
	for _, name := range []string{"ada", "alan", "barbara"} {
		u, err := q.CreateUser(ctx, name)
		if err != nil {
			t.Fatalf("CreateUser(%q): %v", name, err)
		}
		if u.ID == 0 || u.Name != name {
			t.Errorf("CreateUser(%q) = %+v", name, u)
		}
		created = append(created, u)
	}

	users, err = q.GetUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 3 || users[0] != created[0] || users[2] != created[2] {
		t.Errorf("GetUsers = %+v, want %+v", users, created)
	}

	page, err := q.ListUsersAfter(ctx, db.ListUsersAfterParams{ID: created[0].ID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0] != created[1] {
		t.Errorf("ListUsersAfter(first, 1) = %+v, want [%+v]", page, created[1])
	}
	page, err = q.ListUsersAfter(ctx, db.ListUsersAfterParams{ID: created[2].ID, Limit: 10})
	if err != nil || len(page) != 0 {
		t.Errorf("ListUsersAfter(last) = %+v, %v; want empty", page, err)
	}

	u, err := q.GetUser(ctx, created[1].ID)
	if err != nil || u != created[1] {
		t.Errorf("GetUser = %+v, %v; want %+v", u, err, created[1])
	}

	n, err := q.DeleteUser(ctx, created[1].ID)
	if err != nil || n != 1 {
		t.Errorf("DeleteUser = %d, %v; want 1", n, err)
	}
	n, err = q.DeleteUser(ctx, created[1].ID)
	if err != nil || n != 0 {
		t.Errorf("DeleteUser again = %d, %v; want 0", n, err)
	}
	if _, err := q.GetUser(ctx, created[1].ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("GetUser deleted: err = %v, want pgx.ErrNoRows", err)
	}
}

func TestQueriesInTransaction(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := testinfra.NewPool(t, stack.Postgres.NewMigratedDatabase(t))

	// DBTX is satisfied by pgx.Tx as well as the pool.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.New(tx).CreateUser(ctx, "rolled back"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	users, err := db.New(pool).GetUsers(ctx)
	if err != nil || len(users) != 0 {
		t.Errorf("after rollback GetUsers = %+v, %v; want empty", users, err)
	}
}

func TestQueryErrorsSurface(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	// No migrations: every query hits a missing table.
	q := db.New(testinfra.NewPool(t, stack.Postgres.NewDatabase(t)))
	if _, err := q.GetUsers(ctx); err == nil {
		t.Error("GetUsers without table: want error")
	}
	if _, err := q.ListUsersAfter(ctx, db.ListUsersAfterParams{Limit: 1}); err == nil {
		t.Error("ListUsersAfter without table: want error")
	}
	if _, err := q.CreateUser(ctx, "x"); err == nil {
		t.Error("CreateUser without table: want error")
	}
	if _, err := q.DeleteUser(ctx, 1); err == nil {
		t.Error("DeleteUser without table: want error")
	}
}
