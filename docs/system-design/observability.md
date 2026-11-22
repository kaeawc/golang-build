# Observability

> Related: [Architecture](architecture.md) · [Deployment](deployment.md)

## Logs

`log/slog` to stderr; Fly captures it for `flyctl logs`. `LOG_LEVEL` and `LOG_FORMAT` select the level and JSON/text output ([Deployment → Logging](deployment.md#logging)).

Every `/api` request gets a request-scoped logger (`request_id`, `method`, `path`) from `internal/middleware.Logging`, which also writes one `http request` access line per request. Handlers log through `logger.FromContext(r.Context())` and use the `*Context` methods (`WarnContext`, `LogAttrs`, ...), so that when tracing is on each record also carries `trace_id` and `span_id` (`telemetry.LogHandler`) and can be joined to its trace.

## Healthchecks

- `GET /healthz` pings the DB pool with a 1s timeout (cached for 5s): 200 if the ping succeeds, 503 otherwise.
- Mounted outside the `/api` logging chain and excluded from tracing to keep access logs and traces free of probe noise.
- Used by Fly's `http_service.checks` and any external uptime monitor.

## Traces and metrics (OpenTelemetry, opt-in)

`internal/telemetry.Setup` installs OpenTelemetry tracer and/or meter providers exporting OTLP over HTTP, **each only when an endpoint is configured for that signal**. Without one, the global no-op provider stays in place and the instrumentation below costs next to nothing. Like the OTel SDKs, it reads the standard process environment variables. SDK errors, such as failed exports, are logged through slog as `opentelemetry` warnings.

| Variable | Effect |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Enables traces and metrics, e.g. `http://otel-lgtm:4318`. |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` / `..._METRICS_ENDPOINT` | Enables just that signal (full URL, e.g. `https://tempo.example/v1/traces`). |
| `OTEL_TRACES_EXPORTER=none` / `OTEL_METRICS_EXPORTER=none` | Turns one signal off even with the shared endpoint set. |
| `OTEL_EXPORTER_OTLP_HEADERS` | Auth headers for a hosted backend (`key=value,...`). Set as a Fly secret. |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG` | Sampling. Default is parent-based always-on; for production traffic use e.g. `parentbased_traceidratio` with `0.01`. |
| `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES` | Resource attributes. The server sets `service.name`, `service.version` and `deployment.environment.name`; these variables override them. |
| `OTEL_SDK_DISABLED=true` | Forces telemetry off even with an endpoint set. |

What is instrumented:

- **HTTP server**: `otelhttp` around the router. Spans are named `{METHOD} {route}` from the matched ServeMux pattern (`GET /api/users`, `HEAD /api/users`) with `http.route`. Requests that match nothing (405, redirects) are named by method only, and non-standard methods become `HTTP`, so clients can't create arbitrary span names. W3C `traceparent` headers are honored. Emits `http.server.request.duration` and body-size metrics.
- **Postgres**: `otelpgx` tracer on the pgx pool (a span per query) plus pool stats metrics.
- **River jobs**: `otelriver` middleware (spans for insert and work, `river.*` metrics).
- **Go runtime**: memory, GC and goroutine metrics.
- **loadgen**: its HTTP client uses `otelhttp.NewTransport`, so with an endpoint set its client spans and the server's spans share traces.

Providers are flushed by the `telemetry` shutdown hook, registered first so it runs last, after the HTTP drain and the River/pool/cache hooks.

### Viewing locally

`docker compose up` (or `go run ./cmd/devup`) starts `otel-lgtm` (Grafana with Tempo, Prometheus and Loki) next to the API, and the `api` service exports to it. Open http://localhost:3000, then **Explore → Tempo** for traces or **Prometheus** for metrics. To export from a server run outside compose, set `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`.
