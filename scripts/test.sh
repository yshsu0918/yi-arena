#!/usr/bin/env bash
# Full PoC run: build, reset this framework's database, start, E2E, DB check.
set -euo pipefail
cd "$(dirname "$0")/.."
export PORT="${PORT:-8901}" DB_PORT="${DB_PORT:-5441}"
export BASE_URL="${BASE_URL:-http://localhost:$PORT}"
P=poc-nakama

(cd server && npm ci --silent && npx tsc && node --test test/)
(cd web && npm ci --silent && npx vite build --logLevel warn)
(cd e2e && npm ci --silent)

docker compose -p $P down -v --remove-orphans   # wipe the database volume
docker compose -p $P up -d
for i in $(seq 60); do
  [ "$(docker compose -p $P ps nakama --format '{{.Health}}')" = healthy ] && break
  sleep 1
done
until curl -sf "$BASE_URL/" >/dev/null; do sleep 1; done

rm -f test-screenshot/*.png
node e2e/run.mjs
node e2e/db-check.mjs
