// Package server is the gateway: it accepts WebSocket connections, identifies
// the user, and routes each message to the lobby or to the user's room.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"runtime"
	"time"

	"github.com/coder/websocket"

	"github.com/yshsu0918/yi-arena/go-lobby/internal/lobby"
)

const (
	sendQueue    = 256
	helloTimeout = 10 * time.Second
	writeTimeout = 5 * time.Second
	readLimit    = 4096
)

var validName = regexp.MustCompile(`^[A-Za-z0-9_]{1,20}$`)

type Server struct {
	Lobby *lobby.Lobby
	Stats *lobby.Stats
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/stats", s.handleStats)
	return mux
}

type helloIn struct {
	Name string `json:"name"`
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(readLimit)
	ctx := r.Context()

	// The first message identifies the user. A real server would verify a
	// token here; the PoC trusts a guest name.
	name, err := readHello(ctx, conn)
	if err != nil {
		conn.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}
	c := lobby.NewClient(name, sendQueue, s.Stats)
	if err := s.Lobby.Join(c); err != nil {
		conn.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}
	s.Stats.Connected.Add(1)
	defer s.Stats.Connected.Add(-1)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		writeLoop(ctx, conn, c)
	}()

	readLoop(ctx, conn, s, c)

	// Disconnected: leave the room first (if any), then the lobby.
	if room := c.Room(); room != nil {
		room.Leave(c, true)
	}
	s.Lobby.Leave(c)
	c.Kick()
	<-writerDone
}

func readHello(ctx context.Context, conn *websocket.Conn) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, helloTimeout)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		return "", err
	}
	var in lobby.Inbound
	var h helloIn
	if json.Unmarshal(data, &in) != nil || in.Type != "hello" || json.Unmarshal(in.Data, &h) != nil {
		return "", errors.New("first message must be hello")
	}
	if !validName.MatchString(h.Name) {
		return "", errors.New("invalid name")
	}
	return h.Name, nil
}

func readLoop(ctx context.Context, conn *websocket.Conn, s *Server, c *lobby.Client) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		s.Stats.MessagesIn.Add(1)
		var in lobby.Inbound
		if err := json.Unmarshal(data, &in); err != nil {
			continue
		}
		switch in.To {
		case "lobby":
			s.Lobby.Deliver(c, in)
		case "room":
			if room := c.Room(); room != nil {
				room.Deliver(c, in)
			}
		}
		select {
		case <-c.Done(): // kicked for being too slow
			return
		default:
		}
	}
}

func writeLoop(ctx context.Context, conn *websocket.Conn, c *lobby.Client) {
	for {
		select {
		case msg := <-c.Outbox():
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				c.Kick()
				return
			}
		case <-c.Done():
			conn.Close(websocket.StatusNormalClosure, "")
			return
		case <-ctx.Done():
			return
		}
	}
}

type statsOut struct {
	Goroutines  int            `json:"goroutines"`
	HeapAllocMB float64        `json:"heap_alloc_mb"`
	Connected   int64          `json:"connected"`
	MessagesIn  int64          `json:"messages_in"`
	MessagesOut int64          `json:"messages_out"`
	SlowKicks   int64          `json:"slow_kicks"`
	RoomsOpened int64          `json:"rooms_opened"`
	Lobby       lobby.Snapshot `json:"lobby"`
}

func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	out := statsOut{
		Goroutines:  runtime.NumGoroutine(),
		HeapAllocMB: float64(m.HeapAlloc) / (1 << 20),
		Connected:   s.Stats.Connected.Load(),
		MessagesIn:  s.Stats.MessagesIn.Load(),
		MessagesOut: s.Stats.MessagesOut.Load(),
		SlowKicks:   s.Stats.SlowKicks.Load(),
		RoomsOpened: s.Stats.RoomsOpened.Load(),
		Lobby:       s.Lobby.Snapshot(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
