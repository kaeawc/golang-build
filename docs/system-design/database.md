# Database

> Related: [Architecture](architecture.md) · [Deployment](deployment.md) · [Secrets](secrets.md)

## Provider

[Neon](https://neon.tech) — serverless Postgres, branchable, scale-to-zero.

## Branches

- `main` — production
- `preview-pr-<num>` — ephemeral, created/destroyed by `scripts/preview-deploy.sh`
- `dev-<username>` — optional long-lived dev branches

## Connection strings

Two URLs, both required in production:

- `DATABASE_URL` — **pooled** endpoint (`...-pooler.region.aws.neon.tech`). Used by the application pool.
- `DATABASE_URL_DIRECT` — **direct** endpoint. Used only by `golang-migrate` at boot. The pooler returns NULL for `CURRENT_SCHEMA()`, which breaks `golang-migrate`.

If `DATABASE_URL_DIRECT` is unset, the app falls back to `DATABASE_URL` (fine for local Postgres, broken against Neon's pooler).

## Migrations

- Files: `sql/migrations/NNNNNN_name.{up,down}.sql`
- Tool: [golang-migrate](https://github.com/golang-migrate/migrate)
- Applied automatically at server boot via `internal/db.Migrate`.

## sqlc

Schema is read from `sql/migrations/` (single up file path in `sqlc.yaml` — extend the list as new migrations land). Queries live in `sql/queries/`. Generated code lands in `internal/db/`.
