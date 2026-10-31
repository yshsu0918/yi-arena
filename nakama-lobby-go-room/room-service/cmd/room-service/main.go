// Command room-service runs rooms (one goroutine each). Clients connect over
// WebSocket with an entry ticket; the lobby calls it over gRPC and receives
// room events through Nakama's gRPC API.
package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"time"

	"google.golang.org/grpc"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/roompb"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/ticket"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/room-service/internal/gateway"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/room-service/internal/lobbyclient"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/room-service/internal/room"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/room-service/internal/rpcserver"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("environment variable %s is required", key)
	}
	return v
}

func duration(key, def string) time.Duration {
	d, err := time.ParseDuration(env(key, def))
	if err != nil {
		log.Fatalf("%s: %v", key, err)
	}
	return d
}

func main() {
	secret := []byte(mustEnv("TICKET_SECRET"))
	instanceID := ticket.NewID()[:12]

	events, err := lobbyclient.NewNakama(env("NAKAMA_GRPC_ADDR", "passthrough:///nakama:7349"), mustEnv("NAKAMA_HTTP_KEY"), 8)
	if err != nil {
		log.Fatal(err)
	}
	rooms := room.NewManager(room.Config{
		ArrivalTimeout:  duration("ARRIVAL_TIMEOUT", "30s"),
		DisconnectGrace: duration("DISCONNECT_GRACE", "60s"),
	}, events)

	lis, err := net.Listen("tcp", env("GRPC_ADDR", ":8924"))
	if err != nil {
		log.Fatal(err)
	}
	gs := grpc.NewServer()
	roompb.RegisterRoomServiceServer(gs, &rpcserver.Server{Rooms: rooms, PublicWSBase: env("PUBLIC_WS_BASE", "ws://localhost:8923"), InstanceID: instanceID})
	go func() { log.Fatal(gs.Serve(lis)) }()

	mux := http.NewServeMux()
	(&gateway.Gateway{Rooms: rooms, Secret: secret}).Register(mux)
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, _ *http.Request) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"instance_id":    instanceID,
			"goroutines":     runtime.NumGoroutine(),
			"heap_alloc_mb":  float64(m.HeapAlloc) / (1 << 20),
			"sys_mb":         float64(m.Sys) / (1 << 20),
			"rooms":          rooms.Count(),
			"rooms_opened":   room.Metrics.RoomsOpened.Load(),
			"connections":    room.Metrics.Connections.Load(),
			"messages_out":   room.Metrics.MessagesOut.Load(),
			"slow_kicks":     room.Metrics.SlowKicks.Load(),
			"events_sent":    events.Sent.Load(),
			"events_retries": events.Retries.Load(),
			"events_pending": events.Pending(),
		})
	})
	addr := env("WS_ADDR", ":8923")
	log.Printf("room service %s: websocket %s, grpc %s", instanceID, addr, lis.Addr())
	log.Fatal(http.ListenAndServe(addr, mux))
}
