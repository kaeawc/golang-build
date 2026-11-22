#!/usr/bin/env bash
# Proves `make rename` still turns this template into a working project:
# copies the working tree (tracked and new files, uncommitted edits
# included) to a scratch repo, renames it to a throwaway module path, checks
# nothing still names the old module or project, and that the result
# formats, lints, tests (make rename runs make check) and builds.
#
# Usage: scripts/rename-test.sh [module]    (make rename-test; CI: template-rename)
set -euo pipefail

cd "$(dirname "$0")/.."

module="${1:-example.com/throwaway/renamed-app}"
old_module="$(go list -m)"
old_name="$(awk '/^project_name:/ {print $2}' .goreleaser.yaml | tr -d "\"'")"

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

echo "== copying the working tree to $scratch"
git ls-files -z --cached --others --exclude-standard |
  while IFS= read -r -d '' f; do
    # Skip tracked files deleted from the working tree.
    if [ -e "$f" ] || [ -L "$f" ]; then printf '%s\0' "$f"; fi
  done |
  tar --null -T - -cf - | tar -C "$scratch" -xf -

cd "$scratch"
git -c init.defaultBranch=main init -q
git add -A
git -c user.name=rename-test -c user.email=rename-test@localhost commit -qm "scratch copy"

echo "== make rename MODULE=$module"
make rename MODULE="$module"

echo "== no references to $old_module or $old_name are left"
test "$(go list -m)" = "$module"
if git grep -nIF -e "$old_module" -e "$old_name" -- ':!scripts/rename-module/'; then
  echo "rename-test: the rename left the references above" >&2
  exit 1
fi

echo "== formatting (gofumpt + goimports)"
go tool -modfile=tools/go.mod golangci-lint fmt --diff ./...

echo "== make build"
make build

echo "rename-test: OK ($old_module -> $module)"
