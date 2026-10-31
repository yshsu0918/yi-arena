// Command server runs the Go lobby PoC: one process holding the gateway, the
// lobby goroutine and one goroutine per room.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/yshsu0918/yi-arena/go-lobby/internal/lobby"
	"github.com/yshsu0918/yi-arena/go-lobby/internal/server"
)

func main() {
	addr := flag.String("addr", ":8910", "listen address")
	flag.Parse()

	stats := &lobby.Stats{}
	l := lobby.New(stats)
	go l.Run()

	srv := &server.Server{Lobby: l, Stats: stats}
	log.Printf("go-lobby listening on %s (ws: /ws, stats: /stats)", *addr)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
}
