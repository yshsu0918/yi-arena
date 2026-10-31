#!/usr/bin/env bash
# Regenerates proto/roompb from proto/yiarena/room/v1/room.proto.
# The generated code is committed, so this only needs to run after the .proto
# changes. protoc and the Go plugins are downloaded into .tools/ (gitignored).
set -euo pipefail
cd "$(dirname "$0")/.."

PROTOC_VERSION=29.3
# protoc-gen-go must match the protobuf runtime that Nakama 3.41.0 links
# (google.golang.org/protobuf v1.36.12), see lobby-plugin/go.mod.
PROTOC_GEN_GO_VERSION=v1.36.12
PROTOC_GEN_GO_GRPC_VERSION=v1.5.1

TOOLS="$PWD/.tools"
mkdir -p "$TOOLS/bin"
if [ ! -x "$TOOLS/protoc/bin/protoc" ]; then
  curl -fsSL -o "$TOOLS/protoc.zip" \
    "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-x86_64.zip"
  unzip -q -o "$TOOLS/protoc.zip" -d "$TOOLS/protoc"
  rm "$TOOLS/protoc.zip"
fi
GOBIN="$TOOLS/bin" go install "google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION}"
GOBIN="$TOOLS/bin" go install "google.golang.org/grpc/cmd/protoc-gen-go-grpc@${PROTOC_GEN_GO_GRPC_VERSION}"

PATH="$TOOLS/bin:$PATH" "$TOOLS/protoc/bin/protoc" \
  -I proto \
  --go_out=proto/roompb --go_opt=paths=source_relative \
  --go-grpc_out=proto/roompb --go-grpc_opt=paths=source_relative \
  yiarena/room/v1/room.proto

# source_relative keeps the proto directory structure; flatten it.
mv proto/roompb/yiarena/room/v1/*.go proto/roompb/
rm -rf proto/roompb/yiarena
echo "generated: $(ls proto/roompb)"
