# First-time setup

## Prerequisites

- Go (matching `go.mod`)
- Bun
- Docker Desktop
- `flyctl`, `neonctl`, `gh`, `jq`

## Local

```sh
scripts/dev.sh up   # postgres + valkey + api + frontend
```

API: http://localhost:8080 — Frontend: http://localhost:5173

## Production

1. `flyctl launch --no-deploy` (accept name `golang-build`)
2. Create a Neon project; capture pooled + direct URLs
3. `flyctl secrets set DATABASE_URL=... DATABASE_URL_DIRECT=... VALKEY_ADDR=...`
4. Push to `main` — `deploy.yml` does the rest

## GitHub Actions

Set repo secrets: `FLY_API_TOKEN`, `NEON_API_KEY`, `NEON_PROJECT_ID`. Create a `production` environment and scope `FLY_API_TOKEN` to it.
