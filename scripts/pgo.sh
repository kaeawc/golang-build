#!/usr/bin/env bash
set -euo pipefail

# Regenerates cmd/server/default.pgo (make pgo). See docs/pgo.md.
#
# By default it profiles the harness in cmd/server's test binary: the
# production router and middleware chain with in-memory fakes in place of
# Postgres and Valkey, so it needs nothing but Go and curl. To profile a
# real server instead, start it with DEBUG_ADDR set and point
# PGO_SERVER_URL and PGO_DEBUG_URL at it.
#
#   DURATION        profile window in seconds (default 30)
#   PGO_OUT         output path (default cmd/server/default.pgo)
#   PGO_MERGE       extra profiles to merge in, space separated (e.g. ones
#                   pulled from production's debug listener)
#   PGO_ADDR        harness public address (default 127.0.0.1:18080)
#   PGO_DEBUG_ADDR  harness debug address (default 127.0.0.1:16060)
#   PGO_SERVER_URL  profile this server instead of the harness
#   PGO_DEBUG_URL   its debug listener, e.g. http://127.0.0.1:6060

GO=${GO:-go}
DURATION=${DURATION:-30}
OUT=${PGO_OUT:-cmd/server/default.pgo}
ADDR=${PGO_ADDR:-127.0.0.1:18080}
DEBUG_ADDR=${PGO_DEBUG_ADDR:-127.0.0.1:16060}
SERVER_URL=${PGO_SERVER_URL:-}
DEBUG_URL=${PGO_DEBUG_URL:-}

if [[ ! $DURATION =~ ^[1-9][0-9]*$ ]]; then
    echo "DURATION must be a whole number of seconds, got '$DURATION'" >&2
    exit 2
fi
if [[ -n $SERVER_URL && -z $DEBUG_URL ]]; then
    echo "PGO_DEBUG_URL is required with PGO_SERVER_URL" >&2
    exit 2
fi

# Arrays are never expanded while empty: under set -u that is an error in
# bash < 4.4 (macOS ships 3.2).
work=$(mktemp -d)
pids=""
harness=""
cleanup() {
    for pid in $pids; do
        kill "$pid" 2>/dev/null || true
    done
    wait 2>/dev/null || true
    rm -rf "$work"
}
trap cleanup EXIT

echo "==> building loadgen"
$GO build -o "$work/loadgen" ./cmd/loadgen

if [[ -z $SERVER_URL ]]; then
    echo "==> building harness (cmd/server test binary)"
    $GO test -c -o "$work/harness" ./cmd/server
    PGO_HARNESS_ADDR=$ADDR PGO_HARNESS_DEBUG_ADDR=$DEBUG_ADDR "$work/harness" &
    harness=$!
    pids="$pids $harness"
    SERVER_URL=http://$ADDR
    DEBUG_URL=http://$DEBUG_ADDR
fi

# Fails if the harness exited (e.g. a port was taken), so a different
# server answering on the same port is never profiled by mistake.
harness_alive() {
    if [[ -n $harness ]] && ! kill -0 "$harness" 2>/dev/null; then
        echo "harness exited; is $ADDR or $DEBUG_ADDR already in use? (set PGO_ADDR / PGO_DEBUG_ADDR)" >&2
        return 1
    fi
}

echo "==> waiting for $SERVER_URL/healthz"
for _ in $(seq 100); do
    harness_alive
    curl -fsS -o /dev/null "$SERVER_URL/healthz" 2>/dev/null && break
    sleep 0.1
done
curl -fsS -o /dev/null "$SERVER_URL/healthz"
harness_alive

# Traffic mix as "path concurrency". Load starts a second before the
# profile window and ends a second after it, so the window is steady state.
loaders=""
for spec in "/api/users 16" "/healthz 4" "/api/admin/traffic 2" "/api/no-such-endpoint 2"; do
    read -r path conc <<<"$spec"
    "$work/loadgen" --yes --concurrency "$conc" --duration "$((DURATION + 2))s" \
        "$SERVER_URL$path" >"$work/load${path//\//_}.txt" &
    loaders="$loaders $!"
    pids="$pids $!"
done
sleep 1

echo "==> profiling $DEBUG_URL for ${DURATION}s"
curl -fsS -o "$work/cpu.pprof" "$DEBUG_URL/debug/pprof/profile?seconds=$DURATION"
harness_alive
# shellcheck disable=SC2086 # word splitting is the point
wait $loaders
for f in "$work"/load_*.txt; do
    cat "$f"
done

# shellcheck disable=SC2086 # PGO_MERGE is a space-separated list
set -- "$work/cpu.pprof" ${PGO_MERGE:-}
echo "==> merging $# profile(s) into $OUT"
$GO tool pprof -proto "$@" >"$work/default.pgo"
mv "$work/default.pgo" "$OUT"
$GO tool pprof -top -nodecount=15 "$OUT" 2>/dev/null
