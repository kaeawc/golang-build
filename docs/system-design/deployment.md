# Deployment

> Related: [Architecture](architecture.md) · [Secrets](secrets.md) · [Deploy runbook](runbooks/deploy-and-rollback.md)

## Environments

| Env | Fly app | Neon branch | URL |
|---|---|---|---|
| Production | `golang-build` | `main` | `golang-build.fly.dev` |
| Preview | `golang-build-pr-<num>` | `preview-pr-<num>` | `golang-build-pr-<num>.fly.dev` |
| Local | n/a | docker compose postgres | `localhost:8080` |

## Pipeline

- `.github/workflows/commit.yml` — runs on every push/PR (build, test, lint, complexity, security, licenses, codeql).
- `.github/workflows/deploy.yml` — runs `commit.yml`, then `flyctl deploy --remote-only --strategy rolling`, then smoke-tests `/healthz`.
- `.github/workflows/preview.yml` — comment `preview` on a PR (or `preview 1h` etc.) to spin up an ephemeral env. One preview at a time across the repo.

## Image

`ci/Dockerfile` is a 3-stage build: bun frontend → Go binary → alpine runtime. Alpine (not scratch) for `ca-certificates` so pgx can TLS to Neon.

## Secrets

Set via `flyctl secrets set -a <app>`:

- `DATABASE_URL` (pooled)
- `DATABASE_URL_DIRECT` (direct, for migrations)
- `VALKEY_ADDR`
