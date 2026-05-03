# Secrets

> Related: [Deployment](deployment.md) · [Database](database.md) · [Secret rotation runbook](runbooks/secret-rotation.md)

## What's secret

| Name | Where it lives | Used by |
|---|---|---|
| `DATABASE_URL` | Fly secrets, GitHub Actions secrets | API runtime |
| `DATABASE_URL_DIRECT` | Fly secrets | Migrations at boot |
| `VALKEY_ADDR` | Fly secrets | API runtime (cache) |
| `FLY_API_TOKEN` | GitHub Actions secrets (`production` env + repo) | CI deploy |
| `NEON_API_KEY` | GitHub Actions secrets | Preview workflow (branch create/destroy) |
| `NEON_PROJECT_ID` | GitHub Actions secrets | Preview workflow |

## Conventions

- **Never** commit secrets. `.env*` files are gitignored.
- Fly secrets via `flyctl secrets set` — never inline in `fly.toml`.
- GitHub Actions secrets scoped to the `production` environment for deploy keys.

## Rotation

See [secret-rotation runbook](runbooks/secret-rotation.md). All secrets should be rotatable without downtime; the app re-reads on the next deploy.
