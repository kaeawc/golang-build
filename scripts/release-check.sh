#!/usr/bin/env bash
set -euo pipefail

# Release readiness checklist for the Go server.
# Run before tagging a release; exits non-zero if anything fails.

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'
PASS=0
FAIL=0

check() {
    local name="$1"
    shift
    echo -n "  $name... "
    if "$@" > /dev/null 2>&1; then
        echo -e "${GREEN}PASS${NC}"
        PASS=$((PASS + 1))
    else
        echo -e "${RED}FAIL${NC}"
        FAIL=$((FAIL + 1))
    fi
}

echo "=== Release Checklist ==="
echo ""

echo "Build:"
check "server binary" go build -ldflags "-s -w" -o server ./cmd/server/

echo ""
echo "Quality:"
check "go vet" go vet ./...
check "go test" go test ./... -count=1 -timeout 120s
check "go mod tidy is clean" bash -c 'cp go.mod go.mod.bak && cp go.sum go.sum.bak && go mod tidy && diff -q go.mod go.mod.bak && diff -q go.sum go.sum.bak; rc=$?; mv go.mod.bak go.mod; mv go.sum.bak go.sum; exit $rc'

echo ""
echo "Files:"
check "README.md exists" test -f README.md
check "LICENSE exists" test -f LICENSE
check "CHANGELOG.md exists" test -f CHANGELOG.md

echo ""
echo "Git:"
check "working tree clean" git diff --quiet HEAD
check "no untracked Go files" bash -c '[ -z "$(git ls-files --others --exclude-standard "*.go")" ]'

echo ""
echo "================================"
echo -e "Results: ${GREEN}${PASS} passed${NC}, ${RED}${FAIL} failed${NC}"

if [ "$FAIL" -gt 0 ]; then
    echo ""
    echo "Fix failures before tagging release."
    exit 1
fi

echo ""
echo "Ready to release."
