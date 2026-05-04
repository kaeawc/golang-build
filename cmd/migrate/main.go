// Command migrate is a TUI for inspecting and applying schema
// migrations against the project database. Demonstrates composing
// existing tui phases (AsyncTask + Picker + Confirm + Done) without
// adding new framework muscle.
//
// Interactive (default):
//
//	migrate                            # uses DATABASE_URL + sql/migrations
//	migrate ./other/migrations         # different source dir
//
// Headless:
//
//	migrate --status         # print current version + pending count
//	migrate --up             # apply all pending
//	migrate --up-one         # apply next
//	migrate --down-one       # revert last
//	migrate --fake           # use FakeMigrator (CI/demo)
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kaeawc/golang-build/internal/tui"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	statusOnly := fs.Bool("status", false, "Headless: print status and exit")
	upAll := fs.Bool("up", false, "Headless: apply all pending migrations")
	upOne := fs.Bool("up-one", false, "Headless: apply the next pending migration")
	downOne := fs.Bool("down-one", false, "Headless: revert the most recently applied migration")
	fake := fs.Bool("fake", false, "Use FakeMigrator (no database needed)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	source := "sql/migrations"
	if len(fs.Args()) > 0 {
		source = fs.Args()[0]
	}

	migrator, err := buildMigrator(source, *fake)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	switch {
	case *statusOnly:
		return runHeadlessStatus(os.Stdout, migrator)
	case *upAll:
		return runHeadlessAction(os.Stdout, migrator, actUpAll)
	case *upOne:
		return runHeadlessAction(os.Stdout, migrator, actUpOne)
	case *downOne:
		return runHeadlessAction(os.Stdout, migrator, actDownOne)
	}

	return runInteractive(migrator)
}

func buildMigrator(source string, fake bool) (Migrator, error) {
	if fake {
		files, err := scanMigrations(source)
		if err != nil {
			// In fake mode, an empty source dir is acceptable.
			files = nil
		}
		return NewFakeMigrator(files), nil
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is required (or pass --fake for a demo without a DB)")
	}
	return NewLiveMigrator(source, dsn), nil
}

func runInteractive(migrator Migrator) int {
	final, err := tui.Run(newModel(migrator))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if fm, ok := final.(model); ok && fm.err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", fm.err)
		return 1
	}
	return 0
}

func runHeadlessStatus(out io.Writer, migrator Migrator) int {
	st, err := migrator.Status(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "current version: %s\n", versionLabel(st))
	fmt.Fprintf(out, "available migrations: %d\n", len(st.Migrations))
	pending := st.Pending()
	fmt.Fprintf(out, "pending: %d\n", len(pending))
	for _, m := range pending {
		fmt.Fprintf(out, "  %06d %s\n", m.Version, m.Name)
	}
	return 0
}

func runHeadlessAction(out io.Writer, migrator Migrator, a action) int {
	ctx := context.Background()
	prior, _ := migrator.Status(ctx)
	var err error
	switch a {
	case actUpAll:
		err = migrator.UpAll(ctx)
	case actUpOne:
		err = migrator.UpOne(ctx)
	case actDownOne:
		err = migrator.DownOne(ctx)
	}
	if err != nil && !strings.Contains(err.Error(), "no change") {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	now, sErr := migrator.Status(ctx)
	if sErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", sErr)
		return 1
	}
	fmt.Fprintf(out, "%s: %s → %s\n", actionLabels[a].label, versionLabel(prior), versionLabel(now))
	return 0
}
