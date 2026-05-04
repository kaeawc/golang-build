package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// Migration is one .up.sql file on disk.
type Migration struct {
	Version uint
	Name    string
	Path    string
}

// Status is the merged view of files-on-disk + applied-version-in-db.
type Status struct {
	CurrentVersion uint
	Dirty          bool
	HasVersion     bool // false when no migrations have been applied yet
	Migrations     []Migration
}

// Pending returns the subset of Status.Migrations that have not yet
// been applied (i.e. version > CurrentVersion).
func (s Status) Pending() []Migration {
	out := []Migration{}
	for _, m := range s.Migrations {
		if !s.HasVersion || m.Version > s.CurrentVersion {
			out = append(out, m)
		}
	}
	return out
}

// Migrator runs schema migrations. The TUI takes a Migrator so tests
// can swap in FakeMigrator without a database.
type Migrator interface {
	Status(ctx context.Context) (Status, error)
	UpAll(ctx context.Context) error
	UpOne(ctx context.Context) error
	DownOne(ctx context.Context) error
}

// ---------- live impl ------------------------------------------------------

// LiveMigrator wraps golang-migrate against a real database.
type LiveMigrator struct {
	sourceDir   string
	databaseURL string
}

// NewLiveMigrator constructs a LiveMigrator. sourceDir should contain
// the *.up.sql / *.down.sql files; databaseURL is a postgres:// URL.
func NewLiveMigrator(sourceDir, databaseURL string) *LiveMigrator {
	return &LiveMigrator{sourceDir: sourceDir, databaseURL: databaseURL}
}

func (l *LiveMigrator) open() (*migrate.Migrate, error) {
	abs, err := filepath.Abs(l.sourceDir)
	if err != nil {
		return nil, err
	}
	pgxURL := "pgx5://" + strings.TrimPrefix(strings.TrimPrefix(l.databaseURL, "postgresql://"), "postgres://")
	m, err := migrate.New("file://"+abs, pgxURL)
	if err != nil {
		return nil, fmt.Errorf("migrate: open: %w", err)
	}
	return m, nil
}

func (l *LiveMigrator) Status(ctx context.Context) (Status, error) {
	files, err := scanMigrations(l.sourceDir)
	if err != nil {
		return Status{}, err
	}
	m, err := l.open()
	if err != nil {
		return Status{}, err
	}
	defer m.Close()
	v, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return Status{}, fmt.Errorf("migrate: version: %w", err)
	}
	return Status{
		CurrentVersion: v,
		Dirty:          dirty,
		HasVersion:     !errors.Is(err, migrate.ErrNilVersion),
		Migrations:     files,
	}, nil
}

func (l *LiveMigrator) UpAll(ctx context.Context) error {
	m, err := l.open()
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}

func (l *LiveMigrator) UpOne(ctx context.Context) error {
	m, err := l.open()
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Steps(1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up one: %w", err)
	}
	return nil
}

func (l *LiveMigrator) DownOne(ctx context.Context) error {
	m, err := l.open()
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Steps(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: down one: %w", err)
	}
	return nil
}

// ---------- migration file scanning ----------------------------------------

// migrationFileRe matches `NNNNNN_name.up.sql`. The leading digits become
// the version; the rest of the basename (sans extension) becomes the name.
var migrationFileRe = regexp.MustCompile(`^(\d+)_(.+)\.up\.sql$`)

// scanMigrations returns the *.up.sql files in dir, sorted by version.
func scanMigrations(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("scan migrations: %w", err)
	}
	out := []Migration{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		match := migrationFileRe.FindStringSubmatch(e.Name())
		if match == nil {
			continue
		}
		v, err := strconv.ParseUint(match[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse version in %q: %w", e.Name(), err)
		}
		out = append(out, Migration{
			Version: uint(v),
			Name:    match[2],
			Path:    filepath.Join(dir, e.Name()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// ---------- fake impl ------------------------------------------------------

// FakeMigrator is an in-memory Migrator for tests. Tracks an applied
// list against a static set of available migrations. Safe for
// concurrent use.
type FakeMigrator struct {
	mu         sync.Mutex
	available  []Migration
	applied    map[uint]bool
	maxApplied uint
	failNext   error
}

// NewFakeMigrator constructs a FakeMigrator that knows about the given
// migrations and starts with none applied.
func NewFakeMigrator(available []Migration) *FakeMigrator {
	return &FakeMigrator{
		available: append([]Migration(nil), available...),
		applied:   map[uint]bool{},
	}
}

// FailNext queues an error to return from the next Up*/Down* call.
func (f *FakeMigrator) FailNext(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext = err
}

// Applied returns the set of applied versions, sorted.
func (f *FakeMigrator) Applied() []uint {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uint, 0, len(f.applied))
	for v := range f.applied {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (f *FakeMigrator) Status(_ context.Context) (Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	migs := make([]Migration, len(f.available))
	copy(migs, f.available)
	return Status{
		CurrentVersion: f.maxApplied,
		HasVersion:     f.maxApplied > 0,
		Migrations:     migs,
	}, nil
}

func (f *FakeMigrator) UpAll(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFailLocked(); err != nil {
		return err
	}
	for _, m := range f.available {
		if !f.applied[m.Version] {
			f.applied[m.Version] = true
			if m.Version > f.maxApplied {
				f.maxApplied = m.Version
			}
		}
	}
	return nil
}

func (f *FakeMigrator) UpOne(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFailLocked(); err != nil {
		return err
	}
	for _, m := range f.available {
		if !f.applied[m.Version] {
			f.applied[m.Version] = true
			if m.Version > f.maxApplied {
				f.maxApplied = m.Version
			}
			return nil
		}
	}
	return migrate.ErrNoChange
}

func (f *FakeMigrator) DownOne(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFailLocked(); err != nil {
		return err
	}
	if f.maxApplied == 0 {
		return migrate.ErrNoChange
	}
	delete(f.applied, f.maxApplied)
	// Recompute maxApplied.
	var newMax uint
	for v := range f.applied {
		if v > newMax {
			newMax = v
		}
	}
	f.maxApplied = newMax
	return nil
}

func (f *FakeMigrator) takeFailLocked() error {
	if f.failNext == nil {
		return nil
	}
	err := f.failNext
	f.failNext = nil
	return err
}
