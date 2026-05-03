# Secret rotation

## Database password (Neon)

1. Neon console → Roles → reset password (or create a new role)
2. `flyctl secrets set DATABASE_URL=... DATABASE_URL_DIRECT=...` (Fly redeploys)
3. Revoke the old role/password

## `FLY_API_TOKEN`

1. `flyctl tokens create deploy -a golang-build`
2. Update GitHub Actions secret in the `production` environment
3. Revoke old token: `flyctl tokens list` then `flyctl tokens revoke <id>`

## `NEON_API_KEY`

1. Neon console → Account settings → API keys → create new
2. Update GitHub Actions repo secret
3. Delete old key
