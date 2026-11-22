# golang-build

A Go project template with the boring parts already done: a JSON HTTP API,
Postgres and Valkey wiring, a set of terminal tools, an MCP server, a React
frontend, and a CI/CD pipeline that tests, lints, scans, builds a container
image, deploys to Fly.io and publishes releases.

Start a new project from it, run `make rename`, delete what you don't need.

## What's included

- **HTTP API** (`cmd/server`) on `:8080`, routed with `net/http` ServeMux
  (the /api routes behind a request-id → logging → recover → traffic → gzip →
  content-type middleware chain), with
  Postgres (pgx + sqlc + golang-migrate), Valkey, and River background jobs.
- **Terminal tools** built on a small reusable TUI framework
  (`internal/tui`, on Charm (bubbletea/lipgloss)).
- **MCP server and client** on the official Go SDK (`cmd/mcp`, `cmd/mcpclient`).
- **Utility packages** under `internal/`: retry, rate limiting, circuit
  breaker, event bus, scheduler, caching, auth, a fake-able clock, and more.
- **Web frontend** in `web/` (React + Vite + Tailwind, built with bun),
  served by the API's container image.
- **Tooling**: golangci-lint v2, gofumpt, gosec, govulncheck, go-licenses,
  gocyclo, actionlint and gotestsum, all pinned in `tools/go.mod` and run
  with `go tool`. Nothing to install beyond Go.
- **CI/CD**: GitHub Actions for tests, analysis and the image build;
  Fly.io production and per-PR preview deploys; GoReleaser releases.

## Layout

```
cmd/        one directory per binary (see below)
internal/   application and library packages, importable only from this module
tools/      separate Go module that pins the dev tools (tool directives)
ci/         Dockerfile for the server image (bun frontend → Go → distroless)
docs/       MCP design notes and system-design docs with runbooks
scripts/    dev bootstrap, preview deploys, release checks, rename-module
sql/        migrations and sqlc queries
web/        React frontend
.github/    workflows, issue/PR templates, Dependabot, CODEOWNERS
```

### Binaries (`cmd/`)

| Binary | What it is |
| --- | --- |
| `server` | The JSON HTTP API on `:8080`; also serves the built frontend. |
| `onboard` | TUI wizard that generates a `.env` for the server (headless with `--preset standard --yes`). |
| `loadgen` | TUI HTTP load generator with a live-updating dashboard. |
| `scaffold` | TUI code generator: pick handler / middleware / package and a name, get a boilerplate file. |
| `admin` | TUI deployment inspector (users, healthchecks); fake backend when `DATABASE_URL` is unset. |
| `devup` | TUI wrapper around `docker compose`: pick a service and an action, watch the output. |
| `migrate` | TUI to inspect and apply schema migrations; `--fake` runs without a database. |
| `inspect` | TUI that pipes `internal/eventbus` into a live dashboard of synthetic events. |
| `mcp` | Model Context Protocol server for the users directory over stdio or HTTP ([docs/mcp.md](docs/mcp.md)). |
| `mcpclient` | Plain CLI MCP client that walks through every server feature. |

Everything except `server`, `mcp` and `mcpclient` is a demo of
`internal/tui`; see [Trimming the template](#trimming-the-template).

### `internal/`

Handlers return `http.HandlerFunc` closures (`internal/handlers`); middleware
is `func(http.Handler) http.Handler` (`internal/middleware`). The rest are
small packages with fakes for testing: `clock` (fake time), `proc`
(subprocess runner), `eventbus`, `scheduler`, `retry`, `ratelimit`,
`circuitbreaker`, `cache`/`cacheutil`/`kv`, `auth`/`tokens`, `db` (sqlc),
`mcpserver`, and `tui`.

**TUI framework.** `internal/tui` exposes a `Phase` interface and reusable
phases — `Picker`, `Confirm`, `TextInput`, `AsyncTask`, `LiveView`, `Done`.
Each emits a tagged completion message; your root `tea.Model` switches on it
and moves to the next phase. `cmd/onboard/model.go` is the canonical example,
and `cmd/onboard/headless.go` shows the non-TTY path used in CI.

## Quick start

Requires Go (the version in `go.mod`) and, for the full stack, Docker.

```bash
make help                     # every target, grouped
make check                    # tidy, vet, lint, race tests: the pre-push gate
make build                    # all binaries into bin/

scripts/dev.sh                # postgres + valkey in Docker, API natively on :8080
# or: docker compose up       # everything in Docker, using ci/Dockerfile
curl localhost:8080/healthz
```

The TUIs run without a database:

```bash
./bin/onboard                          # interactive; or --preset standard --yes
./bin/migrate --fake
./bin/mcpclient -yes                   # spawns bin/mcp over stdio and runs the demo
```

### Start your own project

1. Create a repository from this template (or clone it) and check out a
   clean tree.
2. Rename the module and project:

   ```bash
   make rename MODULE=github.com/you/app        # NAME defaults to "app"
   ```

   `scripts/rename-module` rewrites the module path in `go.mod`,
   `tools/go.mod`, every import and the goimports prefix; the project name
   in `.goreleaser.yaml`, `fly.toml`, the Docker image tag, the Fly app
   names in the workflows and scripts, `package.json`, the web UI and the
   docs; repository links (minus any `/vN` suffix); and the owner in
   `.github/CODEOWNERS` (GitHub paths only). It
   prints every file it changed, then runs `go mod tidy` and `make check`.
   It refuses to run on a dirty tree, so `git diff` shows exactly what
   changed, and re-running it with the same arguments is a no-op. Add
   `-dry-run` (via `go run ./scripts/rename-module -module ... -dry-run`) to
   preview.
3. Update `LICENSE` (copyright holder) — the script leaves it alone.
4. Trim what you don't need (below), then delete `scripts/rename-module/`,
   `scripts/rename-test.sh`, the `rename` / `rename-test` Makefile targets
   (and `rename-test` from `make ci`) and the `template-rename` CI job.

## Make targets

`make help` lists them all. The ones you'll use most (descriptions are the
`make help` text):

| Target | Description |
| --- | --- |
| `make build` | Build every cmd/* binary into bin/ (`make build-server` for one) |
| `make run` | Run the HTTP server |
| `make docker` | Build the server container image |
| `make test` | Run tests with the race detector and coverage |
| `make test-short` | Run tests without the race detector (fast inner loop) |
| `make test-integration` | Run integration tests against Postgres/Valkey containers (needs Docker) |
| `make cover` | Open the HTML coverage report |
| `make fuzz` | Run every fuzz target for FUZZTIME (default 30s) each |
| `make bench` / `make bench-compare` | Run benchmarks (BENCH regexp, PKG, COUNT, BENCHTIME) / compare two runs with benchstat |
| `make fmt` | Format code (gofumpt + goimports) |
| `make lint` / `make lint-fix` | Run golangci-lint / and apply automatic fixes |
| `make vuln` | Check dependencies for known vulnerabilities |
| `make tidy` | Tidy go.mod in the app and tools modules |
| `make check` | Fast pre-push gate |
| `make ci` | Everything CI runs except CodeQL and the Docker jobs (image build, test-integration) |
| `make pgo` | Regenerate cmd/server/default.pgo from a DURATION-second (default 30) load run |
| `make release-snapshot` | Build a local goreleaser snapshot into dist/ (needs goreleaser) |
| `make rename` | Make the template yours: make rename MODULE=github.com/you/app [NAME=app] |
| `make rename-test` | Rename a scratch copy to a throwaway module, then lint, test and build it |

### Profile-guided optimization

The server builds with PGO from the committed `cmd/server/default.pgo`. Go
picks it up automatically, including in `ci/Dockerfile` and goreleaser.
`make pgo` regenerates it: it drives the router with headless `cmd/loadgen`
and pulls a CPU profile from the loopback-only debug listener (`DEBUG_ADDR`).
[docs/pgo.md](docs/pgo.md) covers when to refresh the profile and how to
measure the gain.

## Testing

`make test` runs the suite with the race detector. Fuzz targets sit next to
the parsers they cover (`Fuzz*` in `_test.go`), and their seed corpora and
any committed failing inputs replay as part of `make test`. `make fuzz
FUZZTIME=10s` fuzzes every target, and a nightly workflow fuzzes each one
for 5 minutes. `make bench` runs the `b.Loop()` benchmarks.
`make bench-compare OLD=old.txt NEW=new.txt` diffs two runs with benchstat.
[docs/testing.md](docs/testing.md) covers how to write a fuzz target or
benchmark and how to read the results.

## CI and releases

- **`.github/workflows/commit.yml`** runs on every PR and on pushes to
  `main`. Each job maps to a `make` target, so `make ci` reproduces it
  locally (except the Docker jobs): `compile-binary`, `build-image` (pushes
  to GHCR, with provenance and SBOM, only from `main`), `test` (race +
  coverage, JUnit report), `integration` (`make test-integration`:
  `//go:build integration` tests against real Postgres/Valkey containers via
  testcontainers-go, race + coverage), `complexity`, `static-analysis` (tidy, format, vet, golangci-lint,
  actionlint), `check-licenses`, `security` (gosec), `vulnerabilities`
  (govulncheck), `release-config` (goreleaser check), `template-rename`
  (`make rename-test`: renames a scratch copy to a throwaway module, checks
  nothing names the old one, then formats, lints, tests and builds it) and
  `codeql-analysis`. The Go version comes from `go.mod`.
- **`fuzz.yml`** fuzzes every `Fuzz*` target for 5 minutes each night (or
  on demand from the Actions tab), one job per target, and uploads the
  failing input as an artifact. See [docs/testing.md](docs/testing.md).
- **`deploy.yml`** deploys `main` to Fly.io once `commit.yml` passes on it,
  then smoke-tests `/healthz`.
- **`preview.yml`**: a maintainer comments `preview` (or `preview 1h`) on a
  PR to get a temporary Fly app and Neon database branch for it.
- **`release.yml`**: push a `v*` tag and GoReleaser builds archives for the
  binaries listed in `.goreleaser.yaml`, with checksums, SBOMs and build
  provenance, into a draft GitHub release. `make release-snapshot` tries it
  locally.
- **Dependabot** bumps Go modules, the pinned tools, GitHub Actions and the
  Docker base images weekly.

Deploys need `FLY_API_TOKEN` (and `NEON_API_KEY` / `NEON_PROJECT_ID` for
previews); see [docs/system-design](docs/system-design/README.md) and its
[first-time setup runbook](docs/system-design/runbooks/first-time-setup.md).

## Trimming the template

To delete a binary you don't need, say `cmd/loadgen`:

1. `rm -rf cmd/loadgen`
2. Delete its entry under `builds:` in `.goreleaser.yaml`.
3. The Makefile needs no change: `make build` and the `build-<name>` targets
   are generated from `cmd/*`.
4. Drop its row from this README (and its line in `CLAUDE.md` / `AGENTS.md`).
5. Delete `internal/` packages nothing imports any more, then tidy (bash):

   ```bash
   comm -23 <(go list ./internal/... | sort) \
            <(go list -deps ./cmd/... | grep /internal/ | sort)   # unused packages
   make tidy && make check
   ```

   The list also includes library packages no binary uses yet (`retry`,
   `ratelimit`, `circuitbreaker`, ...): keep the ones you want. `go mod
   tidy` drops dependencies only the deleted code used (the Charm libraries
   go once every TUI is gone).

What each binary pulls in from `internal/`:

| Binary | Delete with it (if nothing else uses them) |
| --- | --- |
| `onboard`, `loadgen`, `scaffold`, `migrate` | `internal/tui` once all TUIs are gone |

`make pgo` builds `cmd/loadgen`. If you delete loadgen, also delete the `pgo`
Makefile target and `scripts/pgo.sh`. The committed `cmd/server/default.pgo`
keeps working until the next time you'd refresh it.
| `admin` | `internal/tui` |
| `devup` | `internal/proc`, `internal/tui` |
| `inspect` | `internal/eventbus`, `internal/tui` |
| `mcp`, `mcpclient` | `internal/mcpserver` and `docs/mcp.md`; the check above then finds helpers like `paginator` and `tokens` |

**Removing the server** (e.g. for a CLI-only project) also means deleting
`ci/Dockerfile`, `fly.toml`, `docker-compose.yml`, `scripts/dev.sh`,
`scripts/preview-deploy.sh`, `deploy.yml`, `preview.yml`, the `build-image`
and `integration` jobs in `commit.yml` (with `internal/testinfra` and the
`*_integration_test.go` files), the `docker`, `run` and `pgo` Makefile targets (and `scripts/pgo.sh`, `docs/pgo.md`), and the
Docker entry in `.github/dependabot.yml`. `web/`, `sql/`, `sqlc.yaml` and
the bun files (`package.json`, `bun.lock`, `turbo.json`) exist only for the
server.

**Not deploying to Fly?** Delete `fly.toml`, `deploy.yml`, `preview.yml` and
`scripts/preview-deploy.sh`; `build-image` still publishes to GHCR.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). In short: run `make check` before
pushing, and `make ci` for everything CI runs.
