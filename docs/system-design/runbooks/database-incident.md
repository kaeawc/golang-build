# Database incident

## Symptoms

- `/healthz` returns 503
- API logs show `pgx: connection refused` or pool-exhaustion errors

## Triage

1. Neon status: https://neon.tech/status
2. Neon console → branch → operations / metrics
3. `flyctl logs -a golang-build` for app-side errors

## Common causes

- **Pooler down** — switch app to direct URL temporarily: `flyctl secrets set DATABASE_URL="$DATABASE_URL_DIRECT"`
- **Compute autosuspend cold start** — first request times out; retry. Disable autosuspend on `main` if it's user-visible.
- **Connection limit** — lower `pool_max_conns` or scale machines down

## Recovery

- For data corruption: PITR via Neon console → branch from a timestamp before the incident → cut over via `flyctl secrets set`
- For schema problems: write a forward migration; never edit applied migrations in place
