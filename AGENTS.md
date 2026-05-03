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
