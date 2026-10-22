#!/usr/bin/env bash
# Full PoC run: build, reset this framework's database, start, E2E, DB check.
set -euo pipefail
cd "$(dirname "$0")/.."
export PORT="${PORT:-8902}" DB_PORT="${DB_PORT:-5442}"
export BASE_URL="${BASE_URL:-http://localhost:$PORT}"
P=poc-colyseus

(cd server && { [ -d node_modules ] || npm ci --silent --no-audit --no-fund; } && npx tsc && npx tsx --test test/*.test.ts)
(cd web && { [ -d node_modules ] || npm ci --silent --no-audit --no-fund; } && npx vite build --logLevel warn)
(cd e2e && { [ -d node_modules ] || npm ci --silent --no-audit --no-fund; })

docker compose -p $P down -v --remove-orphans   # wipe the database volume
docker compose -p $P up -d
until [ "$(docker compose -p $P ps db --format '{{.Health}}')" = healthy ]; do sleep 1; done

(cd server && exec node --import tsx src/index.ts) > server.log 2>&1 &
SERVER_PID=$!
trap 'kill $SERVER_PID 2>/dev/null || true' EXIT
until curl -sf "$BASE_URL/" >/dev/null; do sleep 1; done

rm -f test-screenshot/*.png
node e2e/run.mjs
node e2e/db-check.mjs
