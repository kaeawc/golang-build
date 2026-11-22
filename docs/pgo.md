# Profile-guided optimization (PGO)

The server is built with profile-guided optimization. `cmd/server/default.pgo`
is a CPU profile of the server under load. Because it sits in the main
package's directory, `go build ./cmd/server` uses it automatically (the default
is `-pgo=auto`). The compiler inlines and devirtualizes more aggressively on the
hot paths the profile shows. Go's docs report a typical 2-14% CPU gain.

Every build of the server uses the profile: `make build`, `ci/Dockerfile` (it
copies all of `cmd/`, and `.dockerignore` doesn't exclude `*.pgo`) and
goreleaser. Other binaries don't, because they have no profile in their
directory. To check a binary:

```console
$ go version -m bin/server | grep pgo
	build	-pgo=default.pgo
```

## Regenerating the profile

```bash
make pgo                 # 30s profile window
make pgo DURATION=60     # longer window, more samples
```

`scripts/pgo.sh` does the following:

1. Builds `cmd/loadgen` and the profiling harness.
2. Starts the harness with the debug listener on loopback.
3. Runs headless `loadgen` against `/api/users` (16 workers), `/healthz` (4),
   `/api/admin/traffic` (2) and an unknown `/api/` path (2).
4. Fetches `/debug/pprof/profile?seconds=$DURATION` from the debug listener.
5. Merges the result with any profiles in `PGO_MERGE` using
   `go tool pprof -proto`, writes `cmd/server/default.pgo`, and prints the top
   functions.

Commit the new `default.pgo` with the change that called for it.

### The harness and what it leaves out

The server can't boot without Postgres and Valkey. So by default, `make pgo`
profiles a harness instead: `cmd/server`'s test binary with `PGO_HARNESS_ADDR`
set (see `TestMain` in `cmd/server/pgo_harness_test.go`). It serves the real
`buildRouter`: the same routes, the full `/api` middleware chain (request ID,
logging, recover, traffic recorder, gzip, content type) and the real handlers,
wrapped in otelhttp as in production, with the trace-aware log handler. It also
runs the real debug listener. The differences:

- **Fakes in place of I/O.** The user store is a fixed 50-row table, and the
  cache is an in-process map that honors TTLs, so `/api/users` gets the
  production hit/miss mix. The pgx and valkey-go client code never runs, so
  the profile doesn't cover it.
- **No River workers or SPA assets.** `web/dist` isn't built, and the harness
  doesn't send load to `/`.
- **Package `main` symbols differ.** In the test binary, `cmd/server`'s own
  functions have the package's import path instead of `main.`, so profile
  samples for them don't match the real binary. Little work happens in
  package `main` per request (routing setup runs once). The hot code is in
  `internal/...`, `net/http`, `compress/flate` and `log/slog`, and its symbols
  match.
- **Synthetic traffic.** The mix above is a guess, not production's.
- **Telemetry off.** `OTEL_EXPORTER_OTLP_ENDPOINT` isn't set, so otelhttp runs
  against the no-op providers. With an endpoint set, span recording, metric
  aggregation and OTLP export add work the profile doesn't see.
- **Devirtualization targets.** For hot interface calls, such as
  `cache.Cache.Get` in the users handler, the profile names the fake types
  (`memCache`, `harnessUsers`). They don't exist in the production binary, so
  the compiler adds no devirtualized fast path at those call sites. There is
  no harm, but also no gain until a production profile is merged in.

For a more faithful profile, run the real server and point the script at it:

```bash
DEBUG_ADDR=127.0.0.1:6060 make run    # with DATABASE_URL, VALKEY_ADDR, ...
PGO_SERVER_URL=http://127.0.0.1:8080 PGO_DEBUG_URL=http://127.0.0.1:6060 make pgo
```

The best profile comes from production itself. Capture one from a machine's
debug listener. It's loopback-only by default, so on Fly see
[Profiling in deployment.md](system-design/deployment.md#profiling). Then merge
it in:

```bash
curl -o prod.pprof 'http://127.0.0.1:6060/debug/pprof/profile?seconds=60'
PGO_MERGE=prod.pprof make pgo
```

## The debug listener

pprof is served only on a separate listener, never on the public `:8080` mux:

| Variable | Default | Meaning |
| --- | --- | --- |
| `DEBUG_ADDR` | empty (disabled) | Listen address for `/debug/pprof/*`, e.g. `127.0.0.1:6060` |
| `DEBUG_ALLOW_NON_LOOPBACK` | `false` | Allow a host that isn't loopback, including `:6060` (all interfaces) |

pprof can reveal heap contents and command lines, and its CPU profile and
trace endpoints are expensive to run. Because of that, `config.Load` refuses a
`DEBUG_ADDR` whose host isn't `127.0.0.0/8`, `::1` or `localhost` unless
`DEBUG_ALLOW_NON_LOOPBACK=true`. Only set that on a private network.

## Coverage runs and `-pgo=off`

With `default.pgo` present, Go 1.27.4 fails to link `cmd/server`'s test
binary in a multi-package coverage run such as `go test -cover ./...`. The
error is `fingerprint mismatch` on `internal/db`, a package that has no tests.
`make test` and `make test-junit` pass `-pgo=off`, which avoids the error, and
PGO gives nothing in a coverage build anyway. If you run coverage by hand, add
`-pgo=off` too. Plain `go test ./...`, `-race`, and single-package `-cover`
runs aren't affected.

## When to refresh

PGO tolerates a stale profile. The compiler matches samples by function name
and line offset within the function, so edits elsewhere don't invalidate it,
and unmatched samples are ignored. Refresh the profile:

- After a change to the request hot path: middleware, handlers, the router,
  or JSON and gzip encoding.
- After a Go toolchain upgrade (each release changes inlining and the runtime).
- After `make rename`. Profile symbols carry the module path, so after a
  rename the samples for this module's packages no longer match anything.
- When production traffic shifts, for example to a new heavy endpoint.
- In any case, every few months, so the profile keeps up with the code.

Building with the old profile in place doesn't hurt. A refreshed profile from
a PGO-built binary converges, it doesn't oscillate.

## Measuring the gain

Compare the same benchmark built with and without the profile, then check the
difference with [benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat).
`BenchmarkRouter` in `cmd/server` drives the public request path with the
harness fakes. Build the two test binaries once and alternate runs, so drift
on the machine (thermal throttling, noisy neighbors) affects both sides
equally:

```bash
go test -c -pgo=off                     -o off.test ./cmd/server
go test -c -pgo=cmd/server/default.pgo  -o pgo.test ./cmd/server
for i in $(seq 10); do
  ./off.test -test.run='^$' -test.bench=Router -test.benchmem >> off.txt
  ./pgo.test -test.run='^$' -test.bench=Router -test.benchmem >> pgo.txt
done
go run golang.org/x/perf/cmd/benchstat@latest off.txt pgo.txt
```

`make bench` and `make bench-compare` (benchstat pinned in `tools/go.mod`)
come with [#65](https://github.com/kaeawc/golang-build/pull/65). Once it has
merged, `make bench-compare OLD=off.txt NEW=pgo.txt` replaces the last line.
`make bench` itself builds with `-pgo=auto`, so to get the "off" side, run
`go test -pgo=off` as shown above.

Only trust a delta when benchstat reports `p < 0.05` and the ± range is small
compared to it. Microbenchmarks understate PGO, because inlining decisions
depend on the whole program's profile. For a real number, compare CPU per
request in production before and after a deploy.
