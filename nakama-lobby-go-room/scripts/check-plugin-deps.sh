#!/usr/bin/env bash
# Compares every dependency compiled into lobby.so with the version linked
# into the Nakama 3.41.0 binary. Any mismatch makes plugin.Open fail with
# "plugin was built with a different version of package ...".
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
cid=$(docker create heroiclabs/nakama:3.41.0)
docker cp -q "$cid":/nakama/nakama "$tmp/nakama"; docker rm "$cid" >/dev/null
docker run --rm -v "$tmp":/x -v "$PWD/lobby-plugin/build":/p --entrypoint sh heroiclabs/nakama-pluginbuilder:3.41.0 -c '
  echo "nakama: $(go version /x/nakama | cut -d" " -f2)  plugin: $(go version /p/lobby.so | cut -d" " -f2)"
  go version -m /x/nakama | awk "\$1==\"dep\"{print \$2, \$3}" | sort > /x/nk
  go version -m /p/lobby.so | awk "\$1==\"dep\"{print \$2, \$3}" | sort > /x/pl
  join /x/pl /x/nk | awk "{ if (\$2 != \$3) { print \"MISMATCH\", \$1, \"plugin\", \$2, \"nakama\", \$3; bad=1 } else print \"ok      \", \$1, \$2 } END { exit bad }"'
