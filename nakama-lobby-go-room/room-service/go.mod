module github.com/yshsu0918/yi-arena/nakama-lobby-go-room/room-service

go 1.27.1

require (
	github.com/coder/websocket v1.8.13
	github.com/heroiclabs/nakama-common v1.48.0
	github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto v0.0.0
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
)

require (
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
)

replace github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto => ../proto
