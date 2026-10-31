package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/roompb"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/ticket"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/room-service/internal/room"
)

type nopEmitter struct{}

func (nopEmitter) Emit(*roompb.RoomEvent) {}

var secret = []byte("test-secret")

func setup(t *testing.T) (*httptest.Server, *room.Manager) {
	t.Helper()
	rooms := room.NewManager(room.Config{ArrivalTimeout: time.Minute, DisconnectGrace: time.Minute}, nopEmitter{})
	members := []*roompb.Member{{UserId: "a", Name: "Alice"}, {UserId: "b", Name: "Bob"}}
	rooms.Create("r1", members)
	rooms.Create("r2", members)
	mux := http.NewServeMux()
	(&Gateway{Rooms: rooms, Secret: secret}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, rooms
}

func sign(t *testing.T, roomID, userID string, ttl time.Duration) string {
	t.Helper()
	tok, _, err := ticket.Sign(secret, ticket.Claims{RoomID: roomID, UserID: userID, Name: userID, Role: ticket.RolePlayer, ExpiresAt: time.Now().Add(ttl).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// hello connects to a room, sends the first message and returns the type and
// code of the reply.
func hello(t *testing.T, srv *httptest.Server, roomID, first string) (typ, code string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/rooms/"+roomID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	conn.Write(ctx, websocket.MessageText, []byte(first))
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]string
	json.Unmarshal(data, &m)
	return m["type"], m["code"]
}

func helloTicket(tok string) string {
	b, _ := json.Marshal(map[string]string{"type": "hello", "ticket": tok})
	return string(b)
}

func TestTicketChecks(t *testing.T) {
	srv, _ := setup(t)
	good := sign(t, "r1", "a", time.Minute)
	other, _, _ := ticket.Sign([]byte("other-secret"), ticket.Claims{RoomID: "r1", UserID: "a", Role: ticket.RolePlayer, ExpiresAt: time.Now().Add(time.Minute).Unix()})

	cases := []struct {
		name, room, first, want string
	}{
		{"valid", "r1", helloTicket(good), "welcome"},
		{"used twice", "r1", helloTicket(good), "ticket_used"},
		{"no ticket", "r1", `{"type":"hello"}`, "no_ticket"},
		{"not hello", "r1", `{"type":"chat","text":"hi"}`, "no_ticket"},
		{"garbage", "r1", `not json`, "no_ticket"},
		{"expired", "r1", helloTicket(sign(t, "r1", "b", -time.Second)), "ticket_expired"},
		{"other room", "r2", helloTicket(sign(t, "r1", "b", time.Minute)), "wrong_room"},
		{"wrong secret", "r1", helloTicket(other), "bad_ticket"},
		{"unknown room", "nope", helloTicket(sign(t, "nope", "b", time.Minute)), "room_not_found"},
		{"not a member", "r1", helloTicket(sign(t, "r1", "carol", time.Minute)), "not_a_member"},
	}
	for _, c := range cases {
		typ, code := hello(t, srv, c.room, c.first)
		got := typ
		if typ == "error" {
			got = code
		}
		if got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestUsedTicketsPruned(t *testing.T) {
	var u usedTickets
	now := time.Unix(1000, 0)
	if !u.use("x", 999, now) || u.use("x", 999, now) {
		t.Fatal("ticket accepted twice")
	}
	for i := range 2000 {
		u.use(ticket.NewID(), int64(1000+i%2), now)
	}
	if len(u.ids) >= 2000 {
		t.Errorf("expired ticket IDs not pruned: %d left", len(u.ids))
	}
}
