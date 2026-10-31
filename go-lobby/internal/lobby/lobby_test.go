package lobby

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// recv returns the next message for c, failing the test after a timeout.
func recv(t *testing.T, c *Client) Outbound {
	t.Helper()
	select {
	case raw := <-c.Outbox():
		var o Outbound
		if err := json.Unmarshal(raw, &o); err != nil {
			t.Fatalf("bad message for %s: %v", c.Name, err)
		}
		return o
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: no message", c.Name)
		return Outbound{}
	}
}

// expect skips messages until one of the given type arrives.
func expect(t *testing.T, c *Client, from, typ string) Outbound {
	t.Helper()
	for i := 0; i < 20; i++ {
		if o := recv(t, c); o.From == from && o.Type == typ {
			return o
		}
	}
	t.Fatalf("%s: never got %s/%s", c.Name, from, typ)
	return Outbound{}
}

// quiet asserts that c receives nothing for a short while.
func quiet(t *testing.T, c *Client) {
	t.Helper()
	select {
	case raw := <-c.Outbox():
		t.Fatalf("%s: unexpected message %s", c.Name, raw)
	case <-time.After(100 * time.Millisecond):
	}
}

func in(to, typ string, data any) Inbound {
	b, _ := json.Marshal(data)
	return Inbound{To: to, Type: typ, Data: b}
}

func setup(t *testing.T, names ...string) (*Lobby, []*Client) {
	t.Helper()
	stats := &Stats{}
	l := New(stats)
	go l.Run()
	var cs []*Client
	for _, n := range names {
		c := NewClient(n, 64, stats)
		if err := l.Join(c); err != nil {
			t.Fatal(err)
		}
		expect(t, c, "lobby", "welcome")
		cs = append(cs, c)
	}
	return l, cs
}

func TestDuplicateNameRejected(t *testing.T) {
	l, _ := setup(t, "alice")
	if err := l.Join(NewClient("alice", 8, &Stats{})); err != ErrNameTaken {
		t.Fatalf("got %v, want ErrNameTaken", err)
	}
}

func TestLobbyChatReachesEveryoneInLobby(t *testing.T) {
	l, cs := setup(t, "alice", "bob", "carol")
	l.Deliver(cs[0], in("lobby", "chat", ChatIn{Text: "hi", ID: "m1"}))
	for _, c := range cs {
		o := expect(t, c, "lobby", "chat")
		var m ChatOut
		b, _ := json.Marshal(o.Data)
		json.Unmarshal(b, &m)
		if m.From != "alice" || m.Text != "hi" || m.ID != "m1" {
			t.Fatalf("%s got %+v", c.Name, m)
		}
	}
}

func TestDirectMessageOnlyReachesTarget(t *testing.T) {
	l, cs := setup(t, "alice", "bob", "carol")
	l.Deliver(cs[0], in("lobby", "dm", DMIn{To: "bob", Text: "psst"}))
	expect(t, cs[1], "lobby", "dm")
	quiet(t, cs[0])
	quiet(t, cs[2])
}

// openRoom has alice invite bob and bob accept, and returns the room id.
func openRoom(t *testing.T, l *Lobby, alice, bob *Client) string {
	t.Helper()
	l.Deliver(alice, in("lobby", "invite", InviteIn{To: "bob"}))
	o := expect(t, bob, "lobby", "invite")
	inviteID := o.Data.(map[string]any)["invite_id"].(string)
	l.Deliver(bob, in("lobby", "invite_reply", InviteReplyIn{InviteID: inviteID, Accept: true}))
	joined := expect(t, bob, "room:r1", "room_joined")
	expect(t, alice, "room:r1", "room_joined")
	return joined.Data.(map[string]any)["room_id"].(string)
}

func TestInviteOpensRoomAndChatIsIsolated(t *testing.T) {
	l, cs := setup(t, "alice", "bob", "carol")
	alice, bob, carol := cs[0], cs[1], cs[2]
	id := openRoom(t, l, alice, bob)
	if alice.Room() == nil || alice.Room().ID != id {
		t.Fatal("alice should be routed to the room")
	}
	expect(t, carol, "lobby", "rooms") // carol sees the new room in the list

	// Room chat reaches only the members.
	alice.Room().Deliver(alice, in("room", "chat", ChatIn{Text: "gg"}))
	expect(t, alice, "room:"+id, "chat")
	expect(t, bob, "room:"+id, "chat")

	// Lobby chat does not reach players whose focus is a room.
	for len(carol.Outbox()) > 0 {
		<-carol.Outbox()
	}
	l.Deliver(carol, in("lobby", "chat", ChatIn{Text: "anyone?"}))
	expect(t, carol, "lobby", "chat")
	quiet(t, alice)
	quiet(t, bob)

	// But a direct message still reaches a player in a room.
	l.Deliver(carol, in("lobby", "dm", DMIn{To: "alice", Text: "good luck"}))
	expect(t, alice, "lobby", "dm")
}

func TestLeavingRoomResyncsAndClosesEmptyRoom(t *testing.T) {
	l, cs := setup(t, "alice", "bob", "carol")
	alice, bob, carol := cs[0], cs[1], cs[2]
	id := openRoom(t, l, alice, bob)
	room := alice.Room()

	room.Deliver(alice, in("room", "leave", nil))
	expect(t, bob, "room:"+id, "member_left")
	expect(t, alice, "lobby", "welcome") // back in the lobby with a fresh snapshot
	if alice.Room() != nil {
		t.Fatal("alice should be back in the lobby")
	}

	room.Deliver(bob, in("room", "leave", nil))
	expect(t, bob, "lobby", "welcome")
	for {
		o := expect(t, carol, "lobby", "rooms")
		if o.Data.(map[string]any)["op"] == "remove" {
			break
		}
	}
	if s := l.Snapshot(); s.Rooms != 0 || s.InLobby != 3 {
		t.Fatalf("snapshot %+v", s)
	}
	// A closed room never blocks senders.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			room.Deliver(alice, in("room", "chat", ChatIn{Text: "late"}))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sending to a closed room blocked")
	}
}

func TestInviteRules(t *testing.T) {
	l, cs := setup(t, "alice", "bob", "carol")
	alice, bob, carol := cs[0], cs[1], cs[2]

	l.Deliver(alice, in("lobby", "invite", InviteIn{To: "alice"}))
	if o := expect(t, alice, "lobby", "error"); o.Data.(map[string]any)["code"] != "invalid" {
		t.Fatalf("self invite: %v", o.Data)
	}

	l.Deliver(alice, in("lobby", "invite", InviteIn{To: "bob"}))
	o := expect(t, bob, "lobby", "invite")
	inviteID := o.Data.(map[string]any)["invite_id"].(string)

	// bob already has a pending invite, so carol cannot invite him.
	l.Deliver(carol, in("lobby", "invite", InviteIn{To: "bob"}))
	if o := expect(t, carol, "lobby", "error"); o.Data.(map[string]any)["code"] != "busy" {
		t.Fatalf("second invite: %v", o.Data)
	}

	// Only the invitee can answer.
	l.Deliver(carol, in("lobby", "invite_reply", InviteReplyIn{InviteID: inviteID, Accept: true}))
	expect(t, carol, "lobby", "error")

	// Declining tells both sides and frees them.
	l.Deliver(bob, in("lobby", "invite_reply", InviteReplyIn{InviteID: inviteID, Accept: false}))
	for _, c := range []*Client{alice, bob} {
		if o := expect(t, c, "lobby", "invite_result"); o.Data.(map[string]any)["result"] != "declined" {
			t.Fatalf("%s: %v", c.Name, o.Data)
		}
	}
	if s := l.Snapshot(); s.Invites != 0 {
		t.Fatalf("invites left: %+v", s)
	}
}

func TestDisconnectCancelsInvite(t *testing.T) {
	l, cs := setup(t, "alice", "bob")
	alice, bob := cs[0], cs[1]
	l.Deliver(alice, in("lobby", "invite", InviteIn{To: "bob"}))
	expect(t, bob, "lobby", "invite")
	l.Leave(alice)
	if o := expect(t, bob, "lobby", "invite_result"); o.Data.(map[string]any)["result"] != "cancelled" {
		t.Fatalf("got %v", o.Data)
	}
}

func TestSlowClientIsKickedNotWaitedFor(t *testing.T) {
	stats := &Stats{}
	l := New(stats)
	go l.Run()
	slow := NewClient("slow", 2, stats) // tiny queue, never drained
	fast := NewClient("fast", 256, stats)
	l.Join(slow)
	l.Join(fast)
	for i := 0; i < 10; i++ {
		l.Deliver(fast, in("lobby", "chat", ChatIn{Text: "spam"}))
	}
	l.Snapshot() // wait until the lobby processed everything
	select {
	case <-slow.Done():
	default:
		t.Fatal("slow client should have been kicked")
	}
	if stats.SlowKicks.Load() != 1 {
		t.Fatalf("slow kicks = %d", stats.SlowKicks.Load())
	}
}

func TestJoiningIsNotBroadcast(t *testing.T) {
	l, cs := setup(t, "watcher")
	stats := &Stats{}
	for i := 0; i < 50; i++ {
		l.Join(NewClient(fmt.Sprintf("p%02d", i), 64, stats))
	}
	quiet(t, cs[0])
}

func TestWhoListsPlayersWithStatus(t *testing.T) {
	l, cs := setup(t, "alice", "bob", "carol")
	openRoom(t, l, cs[0], cs[1])
	l.Deliver(cs[2], in("lobby", "who", nil))
	o := expect(t, cs[2], "lobby", "players")
	var got PlayersOut
	b, _ := json.Marshal(o.Data)
	json.Unmarshal(b, &got)
	want := []PlayerInfo{
		{Name: "alice", Status: StatusRoom, RoomID: "r1"},
		{Name: "bob", Status: StatusRoom, RoomID: "r1"},
		{Name: "carol", Status: StatusLobby},
	}
	if fmt.Sprint(got.Players) != fmt.Sprint(want) {
		t.Fatalf("got %+v, want %+v", got.Players, want)
	}
}
