package db

import (
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// Migrate applies pending migrations. databaseURL must be a direct (non-pooled)
// connection: Neon's pooler returns NULL for CURRENT_SCHEMA(), which breaks
// golang-migrate.
func Migrate(sourceURL, databaseURL string) error {
	pgxURL := "pgx5://" + strings.TrimPrefix(strings.TrimPrefix(databaseURL, "postgresql://"), "postgres://")
	m, err := migrate.New(sourceURL, pgxURL)
	if err != nil {
		return fmt.Errorf("migrate: open: %w", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}
