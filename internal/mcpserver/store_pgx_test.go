package mcpserver

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestPgxStoreContract runs the UserStore contract against Postgres. It is
// skipped unless MCP_TEST_DATABASE_URL (or DATABASE_URL) is set, so
// `go test ./...` needs no infrastructure; `make test-integration` sets it
// to a container (main_integration_test.go). Everything happens inside a
// rolled-back transaction on a session-local temp table that shadows
// users, so the test never touches real data.
func TestPgxStoreContract(t *testing.T) {
	dsn := os.Getenv("MCP_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set MCP_TEST_DATABASE_URL or run make test-integration for the live Postgres store test")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE users (id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL) ON COMMIT DROP`); err != nil {
		t.Fatal(err)
	}
	testUserStore(t, NewPgxStore(tx))
}
