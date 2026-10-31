module github.com/yshsu0918/yi-arena/nakama-lobby-go-room/lobby-plugin

go 1.27.1

require (
	github.com/heroiclabs/nakama-common v1.48.0
	github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto v0.0.0
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
)

// Pinned to the exact versions linked into Nakama 3.41.0. `go mod tidy` alone
// picks older ones and plugin.Open then fails with "plugin was built with a
// different version of package golang.org/x/sys/unix". Check with
// scripts/check-plugin-deps.sh after any dependency change.
require (
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260908043556-f8649ddbbfe6 // indirect
)

replace github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto => ../proto
