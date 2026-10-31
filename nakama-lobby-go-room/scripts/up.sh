#!/usr/bin/env bash
# Builds the plugin and the room service, then starts everything.
set -euo pipefail
cd "$(dirname "$0")/.."
./scripts/build-plugin.sh
./scripts/check-plugin-deps.sh >/dev/null || { ./scripts/check-plugin-deps.sh; exit 1; }
docker compose up -d --build --wait
echo "up: Nakama :8920 (console :8921, gRPC :8922), room service :8923 (gRPC :8924), Postgres :5444"
