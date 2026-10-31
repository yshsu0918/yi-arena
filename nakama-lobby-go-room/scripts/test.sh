#!/usr/bin/env bash
# Runs the unit tests, then scenarios A-E, each on a freshly reset stack.
# Usage: scripts/test.sh [A,B,C,D,E]   (default: all)
set -euo pipefail
cd "$(dirname "$0")/.."
scenarios=${1:-A,B,C,D,E}

echo "== unit tests"
(cd proto && go test -count=1 ./...)
(cd room-service && go test -race -count=1 ./...)

./scripts/up.sh
(cd bots && go build -o ../.tools/bots .)
status=0
for s in ${scenarios//,/ }; do
  ./scripts/reset.sh
  (cd bots && ../.tools/bots -scenario "$s" -out ../results) || status=1
done
exit $status
