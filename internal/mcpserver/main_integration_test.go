//go:build integration

package mcpserver

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kaeawc/golang-build/internal/testinfra"
)

// stack is nil when MCP_TEST_DATABASE_URL was supplied from outside.
var stack *testinfra.Stack

// TestMain points TestPgxStoreContract (store_pgx_test.go) at a Postgres
// container, so the UserStore contract runs against a real database
// without duplicating it. An explicit MCP_TEST_DATABASE_URL wins.
func TestMain(m *testing.M) {
	if os.Getenv("MCP_TEST_DATABASE_URL") != "" {
		os.Exit(m.Run())
	}
	os.Exit(testinfra.Run(m, func(ctx context.Context, s *testinfra.Stack) error {
		stack = s
		pg, err := s.StartPostgres(ctx)
		if err != nil {
			return err
		}
		return os.Setenv("MCP_TEST_DATABASE_URL", pg.DSN)
	}))
}

// TestPgxStoreContractOnMigratedSchema runs the same contract through a
// pool against the users table as sql/migrations creates it, rather than
// the temp-table stand-in, so schema drift breaks a test.
func TestPgxStoreContractOnMigratedSchema(t *testing.T) {
	if stack == nil {
		t.Skip("MCP_TEST_DATABASE_URL is set; not creating databases on an external server")
	}
	pool, err := pgxpool.New(t.Context(), stack.Postgres.NewMigratedDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	testUserStore(t, NewPgxStore(pool))
}
