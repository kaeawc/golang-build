#!/usr/bin/env bash
# Preview deploy: create/tear down a Neon branch + Fly app for a PR.
# Usage: scripts/preview-deploy.sh <pr-number> [up|down]
#
# Env required:
#   NEON_API_KEY, NEON_PROJECT_ID, FLY_API_TOKEN
#
# Deps on PATH: neonctl, flyctl, jq
set -euo pipefail

cd "$(dirname "$0")/.."

pr="${1:?usage: $0 <pr-number> [up|down]}"
action="${2:-up}"

: "${NEON_API_KEY:?NEON_API_KEY not set}"
: "${NEON_PROJECT_ID:?NEON_PROJECT_ID not set}"
: "${FLY_API_TOKEN:?FLY_API_TOKEN not set}"
export NEON_API_KEY FLY_API_TOKEN

app_base="golang-build"
branch="preview-pr-${pr}"
app="${app_base}-pr-${pr}"

neon() {
  neonctl "$@" --project-id "$NEON_PROJECT_ID" --output json
}

ensure_neon_branch() {
  local branches parent
  branches="$(neon branches list)"
  parent="$(jq -r '.[] | select(.default == true) | .name' <<<"$branches")"
  if [ -z "$parent" ]; then
    echo "Could not resolve default Neon branch for project $NEON_PROJECT_ID" >&2
    exit 1
  fi
  if jq -e --arg n "$branch" '.[] | select(.name == $n)' <<<"$branches" >/dev/null; then
    echo "Neon branch $branch already exists; reusing" >&2
  else
    echo "Creating Neon branch $branch from parent $parent" >&2
    neon branches create --name "$branch" --parent "$parent" >/dev/null
  fi
}

neon_url() {
  local pooled=()
  [ "$1" = "pooled" ] && pooled=(--pooled)
  neonctl connection-string "$branch" --project-id "$NEON_PROJECT_ID" "${pooled[@]}"
}

ensure_fly_app() {
  if flyctl apps list --json | jq -e --arg n "$app" '.[] | select(.Name == $n)' >/dev/null; then
    echo "Fly app $app already exists; reusing"
  else
    flyctl apps create "$app" --org personal
  fi
}

up() {
  ensure_neon_branch
  local pooled direct
  pooled="$(neon_url pooled)"
  direct="$(neon_url direct)"
  ensure_fly_app
  flyctl secrets set -a "$app" \
    DATABASE_URL="$pooled" \
    DATABASE_URL_DIRECT="$direct" \
    ENVIRONMENT="preview"
  flyctl deploy -a "$app" --remote-only --strategy rolling
}

down() {
  flyctl apps destroy "$app" -y || true
  neon branches delete "$branch" || true
}

case "$action" in
  up)   up ;;
  down) down ;;
  *)    echo "Usage: $0 <pr-number> [up|down]" >&2; exit 1 ;;
esac
