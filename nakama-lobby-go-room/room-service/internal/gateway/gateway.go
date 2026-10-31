// Package gateway accepts client WebSockets at /rooms/{id}, checks the entry
// ticket in the first message and routes messages to the room.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/ticket"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/room-service/internal/room"
)

const (
	SendQueue    = 256
	helloTimeout = 10 * time.Second
	writeTimeout = 5 * time.Second
	readLimit    = 4096
)

type Gateway struct {
	Rooms  *room.Manager
	Secret []byte
	Queue  int              // per-connection send queue; SendQueue when 0
	Now    func() time.Time // for tests; time.Now when nil

	used usedTickets
}

func (g *Gateway) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /rooms/{id}", g.handleWS)
}

func (g *Gateway) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Gateway) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(readLimit)
	ctx := r.Context()

	rm, c, code := g.admit(ctx, conn, r.PathValue("id"))
	if code != "" {
		reject(ctx, conn, code)
		return
	}
	room.Metrics.Connections.Add(1)
	defer room.Metrics.Connections.Add(-1)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		writeLoop(ctx, conn, c)
	}()
	readLoop(ctx, conn, rm, c)
	rm.Disconnect(c) // ignored by the room if the member already left
	c.Kick()
	<-writerDone
}

// admit reads the hello message and checks the ticket. It returns an error
// code for the client when the connection is refused.
func (g *Gateway) admit(ctx context.Context, conn *websocket.Conn, roomID string) (*room.Room, *room.Conn, string) {
	hctx, cancel := context.WithTimeout(ctx, helloTimeout)
	defer cancel()
	_, data, err := conn.Read(hctx)
	if err != nil {
		return nil, nil, "no_ticket"
	}
	var in room.Inbound
	if json.Unmarshal(data, &in) != nil || in.Type != "hello" || in.Ticket == "" {
		return nil, nil, "no_ticket"
	}
	now := g.now()
	claims, err := ticket.Verify(g.Secret, in.Ticket, now)
	switch {
	case errors.Is(err, ticket.ErrExpired):
		return nil, nil, "ticket_expired"
	case err != nil:
		return nil, nil, "bad_ticket"
	case claims.RoomID != roomID:
		return nil, nil, "wrong_room"
	case claims.Role != ticket.RolePlayer:
		return nil, nil, "bad_ticket"
	}
	rm := g.Rooms.Get(roomID)
	if rm == nil {
		return nil, nil, "room_not_found"
	}
	if !g.used.use(claims.ID, claims.ExpiresAt, now) {
		return nil, nil, "ticket_used"
	}
	queue := g.Queue
	if queue == 0 {
		queue = SendQueue
	}
	c := room.NewConn(claims.UserID, claims.Name, queue)
	if err := rm.Join(c); err != nil {
		return nil, nil, err.Error()
	}
	return rm, c, ""
}

func reject(ctx context.Context, conn *websocket.Conn, code string) {
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	_ = conn.Write(wctx, websocket.MessageText, room.ErrorMsg(code))
	conn.Close(websocket.StatusPolicyViolation, code)
}

func readLoop(ctx context.Context, conn *websocket.Conn, rm *room.Room, c *room.Conn) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var in room.Inbound
		if json.Unmarshal(data, &in) != nil {
			c.Send(room.ErrorMsg("bad_json"))
			continue
		}
		rm.Deliver(c, in)
		select {
		case <-c.Done(): // kicked
			return
		default:
		}
	}
}

func writeLoop(ctx context.Context, conn *websocket.Conn, c *room.Conn) {
	for {
		select {
		case msg := <-c.Outbox():
			if msg == nil { // Close(): everything before it has been sent
				conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				c.Kick()
				return
			}
		case <-c.Done():
			conn.Close(websocket.StatusPolicyViolation, "kicked")
			return
		case <-ctx.Done():
			return
		}
	}
}

// usedTickets remembers ticket IDs until they expire, so each ticket is
// accepted once.
type usedTickets struct {
	mu   sync.Mutex
	ids  map[string]int64 // ticket ID -> expiry (unix seconds)
	adds int
}

func (u *usedTickets) use(id string, exp int64, now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.ids == nil {
		u.ids = map[string]int64{}
	}
	if _, ok := u.ids[id]; ok {
		return false
	}
	u.ids[id] = exp
	if u.adds++; u.adds%1024 == 0 { // expired tickets fail Verify anyway
		for k, e := range u.ids {
			if e <= now.Unix() {
				delete(u.ids, k)
			}
		}
	}
	return true
}
