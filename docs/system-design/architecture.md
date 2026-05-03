# Architecture

> Related: [Database](database.md) · [Deployment](deployment.md) · [Observability](observability.md)

## Components

- **API** — Go HTTP server (`cmd/server`), routes via `gorilla/mux`.
- **Frontend** — React + Vite SPA in `web/`, served by the Go process from baked-in `web/dist/`.
- **Database** — Neon serverless Postgres.
- **Cache** — Valkey (Redis-compatible).
- **Background jobs** — River (Postgres-backed queue), workers run in-process with the API.

## Request flow

```
client ──► Fly edge ──► Go API
                        │
                        ├─► Postgres (Neon, pooled)
                        ├─► Valkey (cache)
                        └─► (static) /assets, /index.html
```

## Process model

Single Go binary serves API + SPA + background workers. Scale horizontally by adding Fly machines; River coordinates job leasing through Postgres.
