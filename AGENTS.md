# golang-build

Experimental Go HTTP server. Template for Go projects with a comprehensive CI pipeline.

## Working Rules

- Keep code in Go. Use the standard library where possible; only add dependencies for clear value.
- Place HTTP handlers under `internal/handlers/`, middleware under `internal/middleware/`.
- New entry points go under `cmd/<name>/`.
- Internal packages (not part of any public API) live under `internal/`.
- Filesystem writes that must survive crashes go through `internal/fsutil.WriteFileAtomic`.

## Build & Validate

```bash
go build -o server ./cmd/server/   # Build the binary
go vet ./...                        # Static analysis
go test ./... -count=1              # Full test suite
make ci                             # vet + test + complexity + lint + security + licenses
```

After any implementation change, run `go build ./... && go vet ./...`.
Use focused package tests while iterating: `go test ./internal/handlers/ -run TestX -count=1`.

## Git

- Use branch prefix `work/` for agent-created branches.
- Never push to `main` directly; always open a PR.

## Project Map

- `cmd/server/` — HTTP server entry point.
- `internal/handlers/` — route handlers (return `http.HandlerFunc` closures).
- `internal/middleware/` — HTTP middleware (each is `func(http.Handler) http.Handler`, applied via `router.Use()`).
- `internal/fsutil/` — atomic filesystem helpers (`WriteFileAtomic`).
- `internal/clock/` — `Clock` interface, `System` impl, `Fake` for deterministic time tests.
- `internal/proc/` — `Runner` interface over `os/exec` plus `Fake` with scripted matchers.
- `internal/env/` — `Reader` interface for env vars (distinguishes unset vs empty), `OS` and `Map` impls, typed getters.
- `internal/idgen/` — `Generator` interface; `UUID` (crypto/rand hex) and `Sequence` (deterministic counter for tests).
- `internal/vfs/` — `FS` interface; `OS` impl plus `Mem` in-memory fake. Shared contract test keeps the fake faithful to OS behavior.
- `internal/random/` — `Random` interface; `Crypto` (crypto/rand) + `Seeded` (deterministic PCG) impls. Float64/IntN/IntRange/Bytes/UUID plus generic `Pick` and `Shuffle` helpers.
- `internal/retry/` — `Executor` with exponential backoff, max-delay cap, and ±jitter. Takes a `Sleeper` (`Real` for production, `Instant` records delays without sleeping for tests) and an optional `Random` for jitter.
- `internal/httpx/` — `Client` interface for outbound HTTP; `Real` over `net/http`, `Fake` with `MatchExact` / `MatchPrefix` scripted responses. Records calls for assertion. Pair with `retry.Executor` for retries.
- `internal/shutdown/` — `Coordinator` runs registered hooks LIFO with per-hook timeout on SIGINT/SIGTERM. Idempotent. Catches panics. Use it from `main` to bound graceful shutdown.
- `internal/cacheutil/` — building blocks for on-disk caches. `ShardedEntryPath` writes entries under `{root}/{hash[:2]}/{hash[2:]}{ext}` to keep directory fan-out manageable. `VersionedDir` auto-nukes the entries subtree when any declared schema-token sidecar mismatches. `AsyncWriter` is a bounded worker pool with non-blocking `Submit` (returns false when full so callers can fall back to synchronous writes) plus stats. `EncodeZstdGob` / `DecodeZstdGob` are zstd+gob payload helpers (uses `klauspost/compress/zstd`).
- `internal/paginator/` — cursor-based pagination for list endpoints. Cursors are opaque base64-URL-encoded JSON envelopes with a version field so format changes don't silently break clients. `Encode(v) (string, error)` / `Decode(s, &v) error`. `NewPage(items, hasMore, encodeCursor)` builds the response envelope `{items, nextCursor, hasMore}` for `jsonresp.Write`. `SplitPage(items, pageSize)` derives `(items[:pageSize], len > pageSize)` from a fetch-N+1 query. `ClampPageSize(qsParam, def, max)` for `?pageSize=` parsing.
- `internal/errgroup/` — bounded-concurrency fan-out helper modeled on `golang.org/x/sync/errgroup`. `g, ctx := errgroup.WithContext(ctx); g.SetLimit(8); g.Go(fn); g.Wait()`. First non-nil error cancels the derived context (with cause set to the error). Bounded via `SetLimit(n)` (creates an internal `limiter.Semaphore`) or `SetLimiter(lim)` (shares an external one across multiple groups). `TryGo` for non-blocking attempts. Configure before any `Go` call.
- `internal/scheduler/` — in-process job scheduler. `Schedule(Job{Name, Func, Interval, At})` registers periodic or one-shot jobs. `Start(ctx)` runs the loop until ctx cancels; `Stop(ctx)` drains in-flight jobs with a bounded timeout. `RunDue(ctx)` is the deterministic test entry — call it after `clock.Fake.Advance` to fire due jobs without real-time waits. `WaitInflight(ctx)` blocks until the launched goroutines complete. Reads `clock.Clock`. Optional `WithConcurrencyLimit(limiter.Limiter)`, `WithLogger(logger.Logger)`, `WithOnError(fn)`. Per-job non-overlap guard prevents a slow job from starting a second invocation.
- `internal/jsonresp/` — JSON response helpers + structured error envelope. `Write(w, status, body)` and `WriteRaw(w, status, bytes)` for success; `WriteError(w, r, status, code, message)` and `WriteErrorDetails(...)` for the standard envelope `{error: {code, message, requestId, details}}` (requestId pulled from `httpmw.RequestIDFrom`). `Decode(r, &v, opts...)` enforces a body size limit (default 1 MiB), rejects unknown fields, and returns a typed `*DecodeError` classified by `DecodeKind` (Malformed, UnknownField, WrongType, TooLarge, Empty, Trailing). `WriteDecodeError(w, r, err)` translates kinds into appropriate HTTP statuses (`413` for too-large, `400` otherwise).
- `internal/httpmw/` — composable HTTP middleware. `Compose(...)` chains them; each is a `func(http.Handler) http.Handler`. Includes `RequestID(idgen.Generator)` (sanitizes inbound + bounds length), `RealIP(*TrustedProxies)` (only trusts forwarded headers from a configured CIDR/IP set), `Recover(logger.Logger)` (preserves `http.ErrAbortHandler`), `Logger(logger.Logger, WithLoggerClock(clk))` (access log via clock for testable durations; the response wrapper preserves `http.Hijacker` for websocket upgrades), `Timeout(d)`, `SecureHeaders(SecureHeadersConfig{...})` with `DefaultSecureHeaders`, and `CORS(CORSConfig{...})`. Header names are exported constants in `headers.go`.
- `internal/healthcheck/` — `Registry` with `RegisterLiveness/RegisterReadiness(name, ProbeFunc)`. `LivenessHandler()` / `ReadinessHandler()` return `http.Handler`s that run probes in parallel (latency = max not sum), JSON-encode the report, return 200 or 503. `SetShuttingDown()` flips readiness to fail-all without invoking probes — wire it from `shutdown.Coordinator` so load balancers drain before the server stops accepting. Probes get a per-probe timeout (default 2s) and panics are contained. Reads `clock.Clock` so probe `Duration` is testable with `clock.Fake`.
- `internal/circuitbreaker/` — circuit-breaker pattern for outbound calls. After `Threshold` consecutive failures the breaker opens and `Do` returns `ErrOpen` instead of contacting the downstream. After `Cooldown` it enters half-open and allows one probe; success closes it, failure re-opens. `IsFailure` filters which errors count (e.g. exclude 404). `OnStateChange(from,to)` for metrics. Reads `clock.Clock` so cooldown is testable with `clock.Fake`. Pair with `internal/retry` and `internal/httpx`.
- `internal/eventbus/` — generic in-process pub/sub. `NewSync[E]()` delivers in the publisher's goroutine in subscription order; `NewAsync[E](AsyncConfig{BufferSize, DropWhenFull})` fans events out to per-subscriber buffered channels handled by worker goroutines (slow listeners don't block the publisher or other listeners). `Subscribe` returns an idempotent `Unsubscribe`. `Async.Close()` drains queues then waits for worker exit.
- `internal/limiter/` — bounded-concurrency primitive. `Semaphore` exposes `Acquire/AcquireN/TryAcquire/Release` with `ctx` cancellation, `InFlight`/`Capacity` introspection, and cumulative `Stats` (TotalAcquired, TotalWaited). `Run(ctx, fn)` is the convenience wrapper that acquires → defers release → calls fn. Cap goroutines, outbound calls, or any expensive operation to a fixed concurrency.
- `internal/kv/` — generic in-memory `Store[V]` with optional TTL. `Memory[V]` is sharded by FNV hash to reduce contention, expires entries lazily on `Get` and proactively via an optional janitor goroutine, and reads "now" from `clock.Clock` so TTL is testable with `clock.Fake`. `GetOrSet(ctx, key, ttl, compute)` for cached compute. `Sweep()` drops expired entries on demand. The in-memory complement to `cacheutil` (on-disk).
- `internal/ratelimit/` — `Limiter` interface (`Allow/AllowN/Wait/WaitN`). `Bucket` is the production token-bucket impl driven by `clock.Clock` (so `clock.Fake` makes refills deterministic in tests). `Manual` is a fully-test-controlled limiter where the test calls `Set/Add` to release tokens; `Wait` blocks until tokens arrive or ctx cancels.
- `internal/httpserver/` — graceful HTTP server runner. `New(Config{Addr, Handler, ShutdownTimeout, OnShutdown, ...}).Run(ctx)` binds the listener, serves until ctx cancels or SIGINT/SIGTERM, then drains in-flight requests with a bounded timeout. Sane defaults (10s shutdown, 10s ReadHeaderTimeout against slowloris). Pass `Signals: []os.Signal{}` in tests to disable signal handling.
- `internal/logger/` — `Logger` interface over `log/slog`. `New(Config{...})` returns a JSON or text logger to stderr. `Capture` is an in-memory implementation that buffers records for tests (`HasMessage(substr)`, `FilterLevel(level)`, `Records()`). `Discard()` returns a no-op for benchmarks. `FromSlog(*slog.Logger)` adapts an existing slog.Logger.
- `internal/perf/` — local-only timing tracker (no OpenTelemetry, no exporter). `New(false)` is a zero-overhead no-op; `New(true)` records nested phases via `Serial(name) … End()` or via the `defer`-friendly `NewSpan(t, name).Stop()` API (spans support `SetAttr` and `AddMetric`). `NewWithClock(clock.Fake)` makes timing assertions deterministic. `RenderTree(w, entries, RenderOptions{})` prints an ASCII tree with durations and parent-percent (`├─ scan  180ms  90%`); `Summary(entries, topN)` flattens to a "where did time go" list. Designed for CLI tools that want to print their own perf summary at exit.
- `ci/Dockerfile` — multi-stage build (compile in `golang:alpine`, copy to `scratch`).
- `scripts/` — release-check, workflow validation, and other repo-wide bash scripts.
- `sql/`, `sqlc.yaml` — SQL schema and sqlc config.

## CI

`.github/workflows/commit.yml` runs on every push and PR:
`compile-binary`, `build-image`, `test`, `complexity`, `static-analysis`, `check-licenses`, `security`, `codeql-analysis`. Go version comes from `go.mod`.

## Conventions

- Errors: wrap with `fmt.Errorf("context: %w", err)` so callers can `errors.Is/As`.
- Comments: only when the *why* is non-obvious. Don't restate what the code does.
- Tests: live next to the code as `_test.go`. Use `t.TempDir()` for filesystem fixtures.
- Time, randomness, env, subprocess, filesystem: inject via the `internal/clock`, `internal/idgen`, `internal/env`, `internal/proc`, `internal/vfs` interfaces. Never call `time.Now()`, `os.Getenv`, `os/exec`, or raw `os.ReadFile` from code paths that need to be testable. Tests substitute the `Fake`/`Map`/`Mem` implementations.
