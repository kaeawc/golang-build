# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Experimental Go HTTP server with CI pipeline tooling. Uses the standard library `http.ServeMux` (method + wildcard patterns) for routing and serves a JSON API on port 8080.

## Build & Development Commands

```bash
make help            # List every target
make build           # Build all cmd/* binaries into bin/ (make build-server for one)
make test            # Tests with -race and coverage
go test ./internal/handlers/ -run TestGetUsers   # Single test
make test-junit      # Tests with JUnit output, as CI does
make test-integration # //go:build integration tests against Postgres/Valkey containers (needs Docker)
make fuzz FUZZTIME=10s   # Every Fuzz* target in turn (scripts/fuzz.sh); see docs/testing.md
make bench           # b.Loop benchmarks; BENCH=, PKG=, COUNT=; make bench-compare runs benchstat
make lint            # golangci-lint v2 (config: .golangci.yml); make lint-fix to autofix
make fmt             # gofumpt + goimports
make complexity      # gocyclo, flags functions over 10
make security        # gosec
make vuln            # govulncheck
make licenses        # go-licenses check
make check           # Fast pre-push gate; make ci runs everything CI does except CodeQL and the Docker jobs (image build, test-integration)
make docker          # Build the server image from ci/Dockerfile
make rename MODULE=github.com/you/app [NAME=app]   # Rename module + project (scripts/rename-module); make rename-test proves it
```

Dev tools are pinned in `tools/go.mod` with `tool` directives and run via
`go tool -modfile=tools/go.mod <tool>` (the Makefile does this). Nothing to
install beyond Go.

## Architecture

- **`cmd/server/main.go`** — Entry point. `buildRouter` registers `http.ServeMux` patterns (`"GET /api/users"`, SPA on `"GET /"`, so wrong methods get 405) and wraps each `/api` route in the `httpmw.Compose` chain (Logging → Recover → Traffic → Gzip → ContentType), then starts the HTTP server.
- **`cmd/onboard/`** — TUI wizard that walks the user through generating a `.env` for the server. Demonstrates the `internal/tui` framework.
- **`cmd/loadgen/`** — TUI HTTP load generator with a live-updating dashboard. Demonstrates the `LiveView` phase.
- **`cmd/scaffold/`** — TUI code generator. Demonstrates the `TextInput` phase with inline validation.
- **`cmd/admin/`** — TUI deployment inspector. Demonstrates the dynamic Picker pattern (Picker fed by an AsyncTask result). Has a `Backend` interface with both a live (pgxpool + valkey) and a fake implementation.
- **`cmd/devup/`** — TUI wrapper around `docker compose`. Uses `internal/proc.Runner` for subprocess execution (`proc.OS` shells out, `proc.Fake` for tests).
- **`cmd/migrate/`** — TUI for schema migrations. Has a `Migrator` interface with `LiveMigrator` (golang-migrate against the real DB) and `FakeMigrator` (in-memory). Composes existing phases without adding new framework muscle.
- **`cmd/inspect/`** — TUI demo wiring `internal/eventbus` into `LiveView`. The `Inspector` subscribes to an `eventbus.Async[Event]`, accumulates per-level/-kind counters and a ring buffer of recent events, exposes itself as a `tui.LiveSampler`. Synthetic producers (steady/bursty/noisy) drive the bus.
- **`cmd/mcp/`** — MCP server binary (`-transport=stdio|http`, `-backend=memory|postgres`). Logs to stderr via slog, `signal.NotifyContext` + bounded graceful `http.Server` shutdown. Bearer auth is enabled by `MCP_AUTH_SECRET`; without it, non-loopback binds are refused. See `docs/mcp.md`.
- **`cmd/mcpclient/`** — Plain CLI MCP client (not a TUI). `demo` walks every server feature over stdio (spawns `cmd/mcp` via `CommandTransport`) or HTTP (`-url`); `-protocol 2025-11-25` pins the legacy protocol; `-yes` auto-answers elicitations.
- **`internal/mcpserver/`** — `New(Deps) *mcp.Server` and `NewHTTPHandler`. Targets MCP spec 2026-07-28 (stateless HTTP, `server/discover`, multi round-trip elicitation, cacheable lists) with 2025-11-25 compatibility. Has a `UserStore` interface with `MemoryStore` (fake) and `PgxStore` (internal/db). Tests use `mcp.NewInMemoryTransports`, `testing/synctest`, and httptest; the Postgres contract test runs only when `MCP_TEST_DATABASE_URL` is set (`make test-integration` points it at a container).
- **`internal/handlers/`** — Route handlers. Handlers return `http.HandlerFunc` closures.
- **`internal/middleware/`** — HTTP middleware (logging, panic recovery, content-type detection). Each middleware is a `func(http.Handler) http.Handler`, composed with `httpmw.Compose` and applied per route (see `handleAPI` in `cmd/server/main.go`).
- **`internal/tui/`** — Reusable TUI scaffolding built on bubbletea/lipgloss. Exports a `Phase` interface and reusable phases (`Picker`, `Confirm`, `TextInput`, `AsyncTask`, `LiveView`, `Done`); callers assemble them into a root `tea.Model`. See `cmd/onboard/model.go`, `cmd/loadgen/model.go`, and `cmd/scaffold/model.go` for examples.
- **`internal/testinfra/`** — testcontainers-go helpers for integration tests. `Run(m, setup)` starts Postgres/Valkey (images match `docker-compose.yml`) once per package from `TestMain` and terminates them after; `Postgres.NewDatabase(t)` / `NewMigratedDatabase(t)` give each test its own database. Integration tests live in `*_integration_test.go` files tagged `//go:build integration`, so `make test` needs no Docker.
- **`ci/Dockerfile`** — Multi-stage build: bun frontend, Go binary cross-compiled with `-trimpath`, `distroless/static` non-root runtime. Base images pinned by digest.
- **`scripts/rename-module/`** — Go program behind `make rename`: rewrites the module path and project name across the repo (skips its own dir), then runs `go mod tidy` and `make check`. Refuses a dirty tree; idempotent.
- **`tools/`** — Separate module pinning dev tools (golangci-lint, gosec, govulncheck, gotestsum, ...) with `tool` directives.
- **`.goreleaser.yaml`** — Release archives for every binary, built by `.github/workflows/release.yml` on `v*` tags.

## CI Pipeline

Defined in `.github/workflows/commit.yml`. Runs on PRs and pushes to `main`. Jobs: `compile-binary`, `build-image` (depends on compile; pushes to GHCR only from `main`), `test` (race + coverage), `integration` (`make test-integration` against real containers, race + coverage), `complexity`, `static-analysis` (tidy, format, vet, golangci-lint, actionlint), `check-licenses`, `security`, `vulnerabilities`, `release-config`, `template-rename` (`make rename-test`), `codeql-analysis`. Each maps to a `make` target. Go version is read from `go.mod`. `deploy.yml` runs after `commit.yml` succeeds on `main`; `release.yml` runs on `v*` tags; `fuzz.yml` fuzzes every target nightly (and on `workflow_dispatch`) and uploads `testdata/fuzz` on failure.
