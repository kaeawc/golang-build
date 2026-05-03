#!/usr/bin/env bash
# Local dev bootstrap: starts dockerized postgres + valkey, then native api.
# Usage: scripts/dev.sh [up|down|db-only|logs]
set -euo pipefail

cd "$(dirname "$0")/.."

DB_URL="postgres://appuser:apppass@localhost:5432/appdb?sslmode=disable"
VALKEY_ADDR="localhost:6379"

free_port() {
  local port=$1
  local pids
  pids=$(lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true)
  if [ -n "$pids" ]; then
    echo "Freeing port $port (killing PIDs: $pids)"
    # shellcheck disable=SC2086
    kill $pids 2>/dev/null || true
    sleep 1
    pids=$(lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true)
    if [ -n "$pids" ]; then
      # shellcheck disable=SC2086
      kill -9 $pids 2>/dev/null || true
      sleep 1
    fi
  fi
}

ensure_docker() {
  if ! docker info >/dev/null 2>&1; then
    echo "Docker daemon not running; launching Docker Desktop..."
    open -a Docker
    until docker info >/dev/null 2>&1; do sleep 2; done
  fi
}

start_db() {
  ensure_docker
  # If something other than our compose postgres holds 5432, abort rather
  # than kill — it's likely the user's own database.
  if lsof -tiTCP:5432 -sTCP:LISTEN >/dev/null 2>&1; then
    if [ -z "$(docker ps -q -f name=golang-build-postgres-1 2>/dev/null)" ]; then
      echo "Port 5432 is in use by a non-docker process; aborting." >&2
      exit 1
    fi
  fi
  docker compose up -d postgres valkey
  echo "Waiting for postgres to be healthy..."
  until [ "$(docker inspect -f '{{.State.Health.Status}}' golang-build-postgres-1 2>/dev/null)" = "healthy" ]; do
    sleep 1
  done
}

start_api() {
  free_port 8080
  DATABASE_URL="$DB_URL" VALKEY_ADDR="$VALKEY_ADDR" ENVIRONMENT=development PORT=8080 \
    go run ./cmd/server &
  API_PID=$!
  echo "api pid: $API_PID"
}

start_frontend() {
  free_port 5173
  bun install --silent
  (cd web && API_PROXY_TARGET="http://localhost:8080" bun run dev) &
  FE_PID=$!
  echo "frontend pid: $FE_PID"
}

cmd=${1:-up}
case "$cmd" in
  up)
    start_db
    start_api
    start_frontend
    trap 'kill ${API_PID:-0} ${FE_PID:-0} 2>/dev/null || true' INT TERM EXIT
    wait
    ;;
  db-only)
    start_db
    ;;
  down)
    docker compose down
    ;;
  logs)
    docker compose logs -f
    ;;
  *)
    echo "Usage: $0 [up|down|db-only|logs]" >&2
    exit 1
    ;;
esac
