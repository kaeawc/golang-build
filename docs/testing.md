# Fuzzing and benchmarks

Plain unit tests (`make test`) cover the cases you thought of. Fuzz targets
search for the ones you didn't; benchmarks keep hot paths from regressing.
Both are ordinary `_test.go` code next to the package they exercise.

## Fuzzing

```bash
make fuzz                          # every Fuzz* target in the module, 30s each
make fuzz FUZZTIME=10s             # shorter budget
scripts/fuzz.sh -list              # just list "<package> <FuzzName>"
go test ./internal/tokens -run='^$' -fuzz='^FuzzVerify$' -fuzztime=1m   # one target
```

`go test -fuzz` takes one package and one target per run, so
`scripts/fuzz.sh` lists every target with a single `go test -list` call and
runs them one after another. The nightly
[`Fuzz` workflow](../.github/workflows/fuzz.yml) uses the same list to fuzz
each target for 5 minutes in its own job. You can also run it from the
Actions tab with a custom budget.

### Writing a target

Copy one of the existing targets:

| Target | Example of |
| --- | --- |
| `internal/mcpserver` `FuzzParseUserURI`, `FuzzUserURIRoundTrip` | accepted input is canonical; render → parse round trip |
| `internal/tokens` `FuzzVerify`, `FuzzSignVerifyRoundTrip` | security property: no forged or tampered token verifies |
| `internal/paginator` `FuzzDecode`, `FuzzCursorRoundTrip` | untrusted decoder doesn't panic; decode → encode is a fixed point |
| `internal/config` `FuzzLoad` | exactly when parsing may fail, plus invariants on success |
| `internal/env` `FuzzGetInt` | differential test against a stdlib oracle (`strconv`) |
| `cmd/scaffold` `FuzzValidateName` | every accepted input is safe for the code generator |

What makes a target useful:

- **Assert a property, not just "no panic".** Good properties: round trips
  (`Decode(Encode(x)) == x`), canonical forms (anything accepted
  re-renders to the same input), agreement with an oracle, invariants on
  every accepted value, and rejection of anything tampered with.
- **Seed with `f.Add`.** Add valid inputs, near-misses and known edge cases.
  Random mutation rarely produces a whole keyword or a valid signature, so
  seed those too (see the keyword loop in `cmd/scaffold/fuzz_test.go`).
  Seeds run as normal subtests in every `go test`, so they also work as
  regression tests.
- **Skip inputs the property doesn't cover.** For example, use `return`
  for invalid UTF-8 when the value goes through `encoding/json`, which
  replaces it with U+FFFD.
- **Keep each execution fast and deterministic.** Don't use the network or
  real time; use `clock.Fake` and the in-memory fakes.

### When a target fails

`go test` writes the minimized input to
`<package>/testdata/fuzz/<FuzzName>/<hash>` and prints how to replay it:

```bash
go test ./internal/tokens -run='FuzzSignVerifyRoundTrip/f8818fc6826b0401'
```

Commit that file with the fix. Every later `go test` replays it, so the bug
can't come back. When the nightly workflow fails, download the
`fuzz-failure-*` artifact, copy the file into the package's `testdata/fuzz`,
and do the same. The corpus the fuzzer generates while exploring stays in
`$(go env GOCACHE)/fuzz` and isn't committed. CI caches it between nightly
runs so each run continues from the last one.

The fuzz output sometimes shows `execs: N (0/sec)` for several seconds. This
is a reporting artifact: workers report counts only when they finish a
batch. It doesn't mean the run is hung.

## Benchmarks

```bash
make bench                                           # every benchmark, once
make bench BENCH=Chain PKG=./internal/httpmw         # narrow by regexp and package
make bench BENCH=Publish PKG=./internal/eventbus COUNT=10 BENCHTIME=500ms
```

Current benchmarks: eventbus publish and dispatch
(`internal/eventbus`), the token bucket and manual limiter
(`internal/ratelimit`), the `httpmw` middleware chain (`internal/httpmw`),
and MCP URI parsing and a tool round trip (`internal/mcpserver`).

Idioms used here (Go 1.24+):

```go
func BenchmarkSyncPublish(b *testing.B) {
	for _, n := range []int{1, 8, 64} {
		b.Run("subs="+strconv.Itoa(n), func(b *testing.B) {
			bus := setup(n)  // not timed: b.Loop starts the timer itself
			b.ReportAllocs() // allocs/op is often the number you're tuning
			for b.Loop() {   // replaces `for i := 0; i < b.N; i++`
				bus.Publish(e)
			}
		})
	}
}
```

- `b.Loop()` times only the loop body, runs setup and cleanup once, and
  keeps the compiler from optimizing away the call results inside the loop.
  You don't need `b.ResetTimer()` or sink variables.
- Use sub-benchmarks for parameters that change the cost (subscriber
  count, request size, chain depth), so you can see how it scales.
- For lock contention, use `b.RunParallel`. `b.Loop` has no parallel form
  (see `BenchmarkBucketAllowParallel`).

### Comparing runs with benchstat

One benchmark run is noise. Compare at least 10 runs of each side with
[benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat), which is
pinned in `tools/go.mod`:

```bash
git switch main
make bench BENCH=Chain PKG=./internal/httpmw COUNT=10 > old.txt
git switch -                       # back to your branch
make bench BENCH=Chain PKG=./internal/httpmw COUNT=10 > new.txt
make bench-compare OLD=old.txt NEW=new.txt
```

benchstat reports the median change per benchmark with a p-value, and
prints `~` when a difference isn't statistically significant. Close other
heavy processes while benchmarking, and run both sides on the same machine.
