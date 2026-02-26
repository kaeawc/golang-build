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
- **`internal/handlers/`** — Route handlers. Handlers return `http.HandlerFunc` closures.
- **`internal/middleware/`** — HTTP middleware (logging, panic recovery, content-type detection). Each middleware is a `func(http.Handler) http.Handler` applied via `router.Use()`.
- **`ci/Dockerfile`** — Multi-stage build: compiles in `golang:alpine`, copies binary to `scratch` image.

## CI Pipeline

Defined in `.github/workflows/commit.yml`. Runs on all pushes and PRs. Jobs: `compile-binary`, `build-image` (depends on compile), `test`, `complexity`, `static-analysis`, `check-licenses`, `security`, `codeql-analysis`. Go version is read from `go.mod`.
