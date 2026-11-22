#!/usr/bin/env bash
set -euo pipefail

# Runs every Go fuzz target in the module, one after another.
#
#   scripts/fuzz.sh            fuzz each target for $FUZZTIME (default 30s)
#   scripts/fuzz.sh -list      print "<package> <FuzzName>" per target
#   scripts/fuzz.sh -json      print the targets as a JSON array (CI matrix)
#
# `go test -fuzz` accepts one package and one target per run, so this
# discovers targets with a single `go test -list` over ./... and then loops.
# A failing input is written to <package dir>/testdata/fuzz/<FuzzName>/;
# commit it so it replays as a regression test in every `go test` run.

GO=${GO:-go}
FUZZTIME=${FUZZTIME:-30s}

list_targets() {
  # -list prints a package's matching names followed by its "ok <pkg>" line.
  "$GO" test -list='^Fuzz' ./... | awk '
    /^Fuzz/ { names[++n] = $1; next }
    /^ok/   { for (i = 1; i <= n; i++) print $2, names[i]; n = 0; next }
    /^\?/   { next }
    { print "unexpected go test -list output: " $0 > "/dev/stderr"; exit 1 }
  '
}

case "${1:-}" in
  -list)
    list_targets
    ;;
  -json)
    list_targets | awk '
      BEGIN { printf "[" }
      { printf "%s{\"pkg\":\"%s\",\"name\":\"%s\"}", (NR > 1 ? "," : ""), $1, $2 }
      END { print "]" }
    '
    ;;
  "")
    targets=$(list_targets)
    if [ -z "$targets" ]; then
      echo "no fuzz targets found" >&2
      exit 1
    fi
    while read -r pkg name; do
      echo "== $pkg $name ($FUZZTIME)"
      "$GO" test -run='^$' -fuzz="^${name}\$" -fuzztime="$FUZZTIME" "$pkg" </dev/null
    done <<<"$targets"
    ;;
  *)
    echo "usage: $0 [-list|-json]" >&2
    exit 2
    ;;
esac
