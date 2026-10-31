package room

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/roompb"
)

type recorder struct {
	mu     sync.Mutex
	events []*roompb.RoomEvent
	ch     chan *roompb.RoomEvent
}

func newRecorder() *recorder { return &recorder{ch: make(chan *roompb.RoomEvent, 100)} }

func (r *recorder) Emit(ev *roompb.RoomEvent) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
	r.ch <- ev
}

func (r *recorder) next(t *testing.T, want string) *roompb.RoomEvent {
	t.Helper()
	select {
	case ev := <-r.ch:
		if ev.Type != want {
			t.Fatalf("event %s, want %s", ev.Type, want)
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatalf("no %s event", want)
		return nil
	}
}

func (r *recorder) none(t *testing.T) {
	t.Helper()
	select {
	case ev := <-r.ch:
		t.Fatalf("unexpected event %s", ev.Type)
	case <-time.After(50 * time.Millisecond):
	}
}

var cfg = Config{ArrivalTimeout: time.Minute, DisconnectGrace: time.Minute}

func members() []*roompb.Member {
	return []*roompb.Member{{UserId: "a", Name: "Alice"}, {UserId: "b", Name: "Bob"}}
}

// recv waits for the next message of the given type on a connection.
func recv(t *testing.T, c *Conn, typ string) map[string]any {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case b := <-c.Outbox():
			if b == nil {
				t.Fatalf("connection closed while waiting for %s", typ)
			}
			var m map[string]any
			json.Unmarshal(b, &m)
			if m["type"] == typ {
				return m
			}
		case <-deadline:
			t.Fatalf("no %s message", typ)
		}
	}
}

func joinBoth(t *testing.T, r *Room) (*Conn, *Conn) {
	t.Helper()
	a, b := NewConn("a", "Alice", 64), NewConn("b", "Bob", 64)
	if err := r.Join(a); err != nil {
		t.Fatal(err)
	}
	if err := r.Join(b); err != nil {
		t.Fatal(err)
	}
	return a, b
}

func TestStartChatAndCloseWhenEmpty(t *testing.T) {
	rec := newRecorder()
	m := NewManager(cfg, rec)
	r, _ := m.Create("r1", members())
	a, b := joinBoth(t, r)
	rec.next(t, EventStarted)
	if got := r.Info().Status; got != roompb.RoomStatus_ROOM_STATUS_PLAYING {
		t.Fatalf("status %v", got)
	}

	r.Deliver(a, Inbound{Type: "chat", Text: "hi", ID: "m1"})
	if msg := recv(t, b, "chat"); msg["text"] != "hi" || msg["from"] != "Alice" {
		t.Fatalf("chat %v", msg)
	}

	r.Deliver(a, Inbound{Type: "leave"})
	recv(t, b, "member_left")
	if m.Get("r1") == nil {
		t.Fatal("room closed with a member still inside")
	}
	r.Deliver(b, Inbound{Type: "leave"})
	ev := rec.next(t, EventClosed)
	if ev.Reason != "empty" {
		t.Errorf("reason %q", ev.Reason)
	}
	<-r.Done()
	if m.Get("r1") != nil || len(m.List()) != 0 {
		t.Error("closed room still listed")
	}
}

func TestEventSeqIncreases(t *testing.T) {
	rec := newRecorder()
	m := NewManager(cfg, rec)
	r, _ := m.Create("r1", members())
	a, b := joinBoth(t, r)
	rec.next(t, EventStarted)
	r.Disconnect(a)
	rec.next(t, EventDisconnected)
	a2 := NewConn("a", "Alice", 64)
	if err := r.Join(a2); err != nil {
		t.Fatal(err)
	}
	rec.next(t, EventReconnected)
	r.Deliver(a2, Inbound{Type: "leave"})
	r.Deliver(b, Inbound{Type: "leave"})
	rec.next(t, EventClosed)

	for i, ev := range rec.events {
		if ev.Seq != uint64(i+1) {
			t.Errorf("event %d (%s) seq %d", i, ev.Type, ev.Seq)
		}
		if ev.Room.GetSeq() != ev.Seq {
			t.Errorf("snapshot seq %d != event seq %d", ev.Room.GetSeq(), ev.Seq)
		}
	}
}

func TestClosedRoomNeverBlocks(t *testing.T) {
	rec := newRecorder()
	m := NewManager(cfg, rec)
	r, _ := m.Create("r1", members())
	a := NewConn("a", "Alice", 64)
	r.Join(a)
	m.Close("r1", "admin")
	rec.next(t, EventClosed)
	<-r.Done()

	done := make(chan struct{})
	go func() {
		// Far more than the room's event buffer (256).
		for range 1000 {
			r.Deliver(a, Inbound{Type: "chat", Text: "x"})
			r.Disconnect(a)
		}
		if err := r.Join(NewConn("b", "Bob", 1)); err != ErrClosed {
			t.Errorf("Join on closed room: %v", err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sending to a closed room blocked")
	}
}

func TestArrivalTimeoutCancels(t *testing.T) {
	rec := newRecorder()
	m := NewManager(Config{ArrivalTimeout: 50 * time.Millisecond, DisconnectGrace: time.Minute}, rec)
	r, _ := m.Create("r1", members())
	a := NewConn("a", "Alice", 64)
	r.Join(a)
	ev := rec.next(t, EventCancelled)
	if ev.Reason != "arrival_timeout" {
		t.Errorf("reason %q", ev.Reason)
	}
	if msg := recv(t, a, "room_closed"); msg["reason"] != "arrival_timeout" {
		t.Errorf("room_closed %v", msg)
	}
	<-r.Done()
}

func TestArrivalTimerStopsOnStart(t *testing.T) {
	rec := newRecorder()
	m := NewManager(Config{ArrivalTimeout: 50 * time.Millisecond, DisconnectGrace: time.Minute}, rec)
	r, _ := m.Create("r1", members())
	joinBoth(t, r)
	rec.next(t, EventStarted)
	time.Sleep(100 * time.Millisecond)
	rec.none(t)
}

func TestDisconnectGraceLeaves(t *testing.T) {
	rec := newRecorder()
	m := NewManager(Config{ArrivalTimeout: time.Minute, DisconnectGrace: 50 * time.Millisecond}, rec)
	r, _ := m.Create("r1", members())
	a, b := joinBoth(t, r)
	rec.next(t, EventStarted)
	r.Disconnect(a)
	rec.next(t, EventDisconnected)
	recv(t, b, "member_left") // grace expired
	if err := r.Join(NewConn("a", "Alice", 64)); err != ErrLeft {
		t.Fatalf("rejoin after grace: %v", err)
	}
	r.Deliver(b, Inbound{Type: "leave"})
	rec.next(t, EventClosed)
}

func TestNonMemberRejected(t *testing.T) {
	m := NewManager(cfg, newRecorder())
	r, _ := m.Create("r1", members())
	if err := r.Join(NewConn("carol", "Carol", 8)); err != ErrNotMember {
		t.Fatalf("Join = %v", err)
	}
}

func TestSlowConnectionKicked(t *testing.T) {
	rec := newRecorder()
	m := NewManager(cfg, rec)
	r, _ := m.Create("r1", members())
	a := NewConn("a", "Alice", 256)
	slow := NewConn("b", "Bob", 4) // never drained
	r.Join(a)
	r.Join(slow)
	rec.next(t, EventStarted)
	for i := range 20 {
		r.Deliver(a, Inbound{Type: "chat", Text: "spam", ID: string(rune('a' + i))})
	}
	select {
	case <-slow.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("slow connection not kicked")
	}
	// The room keeps serving the others.
	r.Deliver(a, Inbound{Type: "chat", Text: "still here"})
	for {
		if msg := recv(t, a, "chat"); msg["text"] == "still here" {
			break
		}
	}
}
