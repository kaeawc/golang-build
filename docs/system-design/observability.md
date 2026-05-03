# Observability

> Related: [Architecture](architecture.md) · [Deployment](deployment.md)

## Logs

Structured access logs via `internal/middleware.Logging`. Stdout only — Fly captures and forwards to `flyctl logs`.

## Healthchecks

- `GET /healthz` — pings the DB pool with a 1s timeout. 200 if `SELECT 1` succeeds, 503 otherwise.
- Mounted outside the logging chain to keep access logs clean.
- Used by Fly's `http_service.checks` and any external uptime monitor.

## Metrics

(Not wired yet — when added, prefer Prometheus exposition + Fly's metrics scrape.)

## Tracing

(Not wired yet — when added, OTLP via OpenTelemetry, sampling at 1%.)
