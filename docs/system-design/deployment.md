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
