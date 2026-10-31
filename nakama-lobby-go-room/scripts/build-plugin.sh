#!/usr/bin/env bash
# Builds the Nakama Go plugin with the official builder image, which has the
# same Go toolchain (1.27.1) that Nakama 3.41.0 was built with. Output:
# lobby-plugin/build/lobby.so (mounted into the Nakama container).
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p lobby-plugin/build
docker run --rm \
  -u "$(id -u):$(id -g)" -e HOME=/tmp -e GOCACHE=/gocache/build -e GOMODCACHE=/gocache/mod \
  -v poc-nlgr-gocache:/gocache \
  -v "$PWD":/src -w /src/lobby-plugin \
  --entrypoint sh heroiclabs/nakama-pluginbuilder:3.41.0 -c '
    go build -buildmode=plugin -trimpath -o ./build/lobby.so . &&
    go version -m ./build/lobby.so | grep -E "^\s+dep" > ./build/deps.txt'
echo "built lobby-plugin/build/lobby.so"
