# Deployment

> Related: [Architecture](architecture.md) · [Secrets](secrets.md) · [Deploy runbook](runbooks/deploy-and-rollback.md)

## Environments

| Env | Fly app | Neon branch | URL |
|---|---|---|---|
| Production | `golang-build` | `main` | `golang-build.fly.dev` |
| Preview | `golang-build-pr-<num>` | `preview-pr-<num>` | `golang-build-pr-<num>.fly.dev` |
| Local | n/a | docker compose postgres | `localhost:8080` |

## Pipeline

- `.github/workflows/commit.yml` — runs on PRs and pushes to `main` (build, image, race tests + coverage, lint, complexity, gosec, govulncheck, licenses, goreleaser check, codeql).
- `.github/workflows/deploy.yml` — triggered by a successful `commit.yml` run on a push to `main` (`workflow_run`); checks out that exact commit, runs `flyctl deploy --remote-only --strategy rolling`, then smoke-tests `/healthz`.
- `.github/workflows/preview.yml` — comment `preview` on a PR (or `preview 1h` etc.) to spin up an ephemeral env. One preview at a time across the repo.

## Image

`ci/Dockerfile` is a 3-stage build: bun frontend → Go binary → `gcr.io/distroless/static-debian13:nonroot` runtime. Distroless (not scratch) because it ships `ca-certificates` (pgx TLS to Neon) and tzdata; it has no shell and runs as uid 65532. Base images are pinned by digest and bumped by Dependabot.

## Secrets

Set via `flyctl secrets set -a <app>`:

- `DATABASE_URL` (pooled)
- `DATABASE_URL_DIRECT` (direct, for migrations)
- `VALKEY_ADDR`

## Logging

The server logs with `log/slog` to stderr. Optional settings (plain env, not secrets):

- `LOG_LEVEL`: `debug`, `info` (default), `warn` or `error`.
- `LOG_FORMAT`: `json` or `text`. Defaults to `text` when `ENVIRONMENT=development` and `json` everywhere else.

Each `/api` request gets a request-scoped logger carrying `request_id` (from `X-Request-ID`, or generated and echoed back), `method` and `path`. Handlers retrieve it with `logger.FromContext(r.Context())`.

## Profiling

`DEBUG_ADDR` (e.g. `127.0.0.1:6060`) serves `/debug/pprof/*` on a separate listener; unset (the default) disables it. Non-loopback hosts are refused unless `DEBUG_ALLOW_NON_LOOPBACK=true`.

On Fly, a loopback listener can't be reached from outside the Machine: `fly proxy` tunnels to the Machine's private 6PN address, not to its 127.0.0.1, and the distroless image has no shell for `fly ssh console`. To profile a Machine, bind the listener to its private address for the session and proxy to it:

```bash
flyctl secrets set -a <app> DEBUG_ADDR=fly-local-6pn:6060 DEBUG_ALLOW_NON_LOOPBACK=true
fly proxy 6060:6060 -a <app>
curl -o prod.pprof 'http://127.0.0.1:6060/debug/pprof/profile?seconds=60'
flyctl secrets unset -a <app> DEBUG_ADDR DEBUG_ALLOW_NON_LOOPBACK
```

The 6PN address is private to the organization's WireGuard network, but every app and member in the organization can reach it, so unset the variables when you're done. (This procedure hasn't been exercised against a live Machine yet.) The server is built with PGO from `cmd/server/default.pgo`; see [docs/pgo.md](../pgo.md).
