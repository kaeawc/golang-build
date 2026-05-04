# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Experimental Go HTTP server with CI pipeline tooling. Uses `gorilla/mux` for routing and serves a JSON API on port 8080.

## Build & Development Commands

```bash
# Build the binary
go build cmd/server/main.go

# Run tests
go test ./...

# Run a single test (by function name)
go test ./internal/handlers/ -run TestGetUsers

# Run tests with verbose JUnit output (as CI does)
gotestsum --junitfile junit-report.xml --format standard-verbose ./...

# Lint (static analysis)
golangci-lint run

# Cyclomatic complexity check (flags functions over 10)
gocyclo -over 10 .

# Security scan
gosec ./...

# License check
go-licenses report ./...

# Build Docker image
docker build -f ci/Dockerfile -t golang-build:latest .
```

## Architecture

- **`cmd/server/main.go`** — Entry point. Sets up the mux router with middleware chain (Logging → Recover → ContentType) and starts the HTTP server.
- **`cmd/onboard/`** — TUI wizard that walks the user through generating a `.env` for the server. Demonstrates the `internal/tui` framework.
- **`cmd/loadgen/`** — TUI HTTP load generator with a live-updating dashboard. Demonstrates the `LiveView` phase.
- **`cmd/scaffold/`** — TUI code generator. Demonstrates the `TextInput` phase with inline validation.
- **`cmd/admin/`** — TUI deployment inspector. Demonstrates the dynamic Picker pattern (Picker fed by an AsyncTask result). Has a `Backend` interface with both a live (pgxpool + valkey) and a fake implementation.
- **`cmd/devup/`** — TUI wrapper around `docker compose`. Uses `internal/proc.Runner` for subprocess execution (`proc.OS` shells out, `proc.Fake` for tests).
- **`cmd/migrate/`** — TUI for schema migrations. Has a `Migrator` interface with `LiveMigrator` (golang-migrate against the real DB) and `FakeMigrator` (in-memory). Composes existing phases without adding new framework muscle.
- **`cmd/inspect/`** — TUI demo wiring `internal/eventbus` into `LiveView`. The `Inspector` subscribes to an `eventbus.Async[Event]`, accumulates per-level/-kind counters and a ring buffer of recent events, exposes itself as a `tui.LiveSampler`. Synthetic producers (steady/bursty/noisy) drive the bus.
- **`internal/handlers/`** — Route handlers. Handlers return `http.HandlerFunc` closures.
- **`internal/middleware/`** — HTTP middleware (logging, panic recovery, content-type detection). Each middleware is a `func(http.Handler) http.Handler` applied via `router.Use()`.
- **`internal/tui/`** — Reusable TUI scaffolding built on bubbletea/lipgloss. Exports a `Phase` interface and reusable phases (`Picker`, `Confirm`, `TextInput`, `AsyncTask`, `LiveView`, `Done`); callers assemble them into a root `tea.Model`. See `cmd/onboard/model.go`, `cmd/loadgen/model.go`, and `cmd/scaffold/model.go` for examples.
- **`ci/Dockerfile`** — Multi-stage build: compiles in `golang:alpine`, copies binary to `scratch` image.

## CI Pipeline

Defined in `.github/workflows/commit.yml`. Runs on all pushes and PRs. Jobs: `compile-binary`, `build-image` (depends on compile), `test`, `complexity`, `static-analysis`, `check-licenses`, `security`, `codeql-analysis`. Go version is read from `go.mod`.
