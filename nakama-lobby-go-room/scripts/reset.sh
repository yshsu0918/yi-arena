#!/usr/bin/env bash
# Wipes the database (and all in-memory state) and starts again.
set -euo pipefail
cd "$(dirname "$0")/.."
docker compose down -v --remove-orphans >/dev/null 2>&1
docker compose up -d --wait >/dev/null 2>&1
# The lobby is ready once it has reached the room service once.
for _ in $(seq 1 50); do
  if curl -fsS -X POST "localhost:8920/v2/rpc/lobby_stats?http_key=${NAKAMA_HTTP_KEY:-local-only-http-key}&unwrap=true" -d '{}' 2>/dev/null | grep -q '"last_reconcile_ok":"2'; then
    echo "reset: clean stack is up"; exit 0
  fi
  sleep 0.2
done
echo "reset: lobby did not reach the room service" >&2; exit 1
