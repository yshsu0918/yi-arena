package lobby

import "sort"

// Room is a single goroutine that owns one table. In this PoC a room only has
// members and a chat; a real room would also hold the game, clocks and record.
type Room struct {
	ID      string
	events  chan any
	closed  chan struct{}
	lobby   *Lobby
	members map[string]*Client
	names   []string // fixed at creation, for the room list
}

type (
	roomMsg struct {
		c  *Client
		in Inbound
	}
	roomLeave struct {
		c            *Client
		disconnected bool
	}
)

func newRoom(id string, members []*Client, l *Lobby) *Room {
	r := &Room{ID: id, events: make(chan any, 256), closed: make(chan struct{}), lobby: l, members: map[string]*Client{}}
	for _, c := range members {
		r.members[c.Name] = c
		r.names = append(r.names, c.Name)
	}
	sort.Strings(r.names)
	return r
}

// Info is safe to call from the lobby goroutine: names never change.
func (r *Room) Info() RoomInfo { return RoomInfo{ID: r.ID, Players: r.names} }

// Deliver and Leave never block on a room that has already closed.
func (r *Room) Deliver(c *Client, in Inbound) { r.post(roomMsg{c, in}) }

// Leave removes a member. disconnected is true when the connection dropped, in
// which case the lobby removes the player itself.
func (r *Room) Leave(c *Client, disconnected bool) { r.post(roomLeave{c, disconnected}) }

func (r *Room) post(ev any) {
	select {
	case r.events <- ev:
	case <-r.closed:
	}
}

func (r *Room) from() string { return "room:" + r.ID }

func (r *Room) Run() {
	joined := encode(r.from(), "room_joined", RoomJoinedOut{RoomID: r.ID, Players: r.names})
	for _, c := range r.members {
		c.Send(joined)
	}
	for len(r.members) > 0 {
		switch e := (<-r.events).(type) {
		case roomMsg:
			r.handle(e.c, e.in)
		case roomLeave:
			r.leave(e.c, e.disconnected)
		}
	}
	close(r.closed)
	r.lobby.events <- roomClosed{r.ID}
}

func (r *Room) handle(c *Client, in Inbound) {
	if r.members[c.Name] != c {
		c.Send(errorMsg(r.from(), "not_a_member", r.ID))
		return
	}
	switch in.Type {
	case "chat":
		var m ChatIn
		if !decode(c, in, &m) || !checkText(c, m.Text) {
			return
		}
		msg := encode(r.from(), "chat", ChatOut{From: c.Name, Text: m.Text, ID: m.ID, SentAt: m.SentAt})
		for _, m := range r.members {
			m.Send(msg)
		}
	case "leave":
		r.leave(c, false)
	default:
		c.Send(errorMsg(r.from(), "unknown_type", in.Type))
	}
}

func (r *Room) leave(c *Client, disconnected bool) {
	if r.members[c.Name] != c {
		return
	}
	delete(r.members, c.Name)
	left := encode(r.from(), "member_left", map[string]string{"name": c.Name})
	for _, m := range r.members {
		m.Send(left)
	}
	if !disconnected {
		r.lobby.events <- backToLobby{r.ID, c}
	}
}
