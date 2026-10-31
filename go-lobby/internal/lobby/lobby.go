package lobby

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

var ErrNameTaken = errors.New("name already in use")

// InviteTTL is how long an invite waits for an answer.
//
// There is no presence broadcast: joining, leaving or entering a room is not
// pushed to other players (that is O(N^2) when many players connect at once).
// Clients get a snapshot in "welcome" and ask for the list with "who".
const InviteTTL = 30 * time.Second

// Lobby is a single goroutine that owns the player list, invites and the room
// list. Everything else talks to it through its event channel, so its state
// needs no locks and events are handled strictly one at a time.
type Lobby struct {
	events chan any
	stats  *Stats

	players   map[string]*player
	invites   map[string]*invite
	rooms     map[string]*Room
	roomSeq   int
	inviteSeq int
}

type player struct {
	c      *Client
	status string
	roomID string
	invite string // pending invite this player sent or received
}

type invite struct {
	id, from, to string
	timer        *time.Timer
}

// Events handled by the lobby goroutine.
type (
	joinEvent struct {
		c     *Client
		reply chan error
	}
	leaveEvent  struct{ c *Client }
	clientEvent struct {
		c  *Client
		in Inbound
	}
	inviteExpired struct{ id string }
	backToLobby   struct {
		roomID string
		c      *Client
	}
	roomClosed    struct{ roomID string }
	snapshotEvent struct{ reply chan Snapshot }
)

// Snapshot is a summary for the /stats endpoint and tests.
type Snapshot struct {
	Players int `json:"players"`
	InLobby int `json:"in_lobby"`
	InRooms int `json:"in_rooms"`
	Rooms   int `json:"rooms"`
	Invites int `json:"invites"`
}

func New(stats *Stats) *Lobby {
	return &Lobby{
		events:  make(chan any, 4096),
		stats:   stats,
		players: map[string]*player{},
		invites: map[string]*invite{},
		rooms:   map[string]*Room{},
	}
}

// Public API: safe to call from any goroutine.

func (l *Lobby) Join(c *Client) error {
	reply := make(chan error, 1)
	l.events <- joinEvent{c, reply}
	return <-reply
}
func (l *Lobby) Leave(c *Client)               { l.events <- leaveEvent{c} }
func (l *Lobby) Deliver(c *Client, in Inbound) { l.events <- clientEvent{c, in} }
func (l *Lobby) Snapshot() Snapshot {
	reply := make(chan Snapshot, 1)
	l.events <- snapshotEvent{reply}
	return <-reply
}

func (l *Lobby) Run() {
	for ev := range l.events {
		switch e := ev.(type) {
		case joinEvent:
			e.reply <- l.join(e.c)
		case leaveEvent:
			l.leave(e.c)
		case clientEvent:
			l.handle(e.c, e.in)
		case inviteExpired:
			l.endInvite(e.id, "expired")
		case backToLobby:
			l.backToLobby(e.roomID, e.c)
		case roomClosed:
			l.roomClosed(e.roomID)
		case snapshotEvent:
			e.reply <- l.snapshot()
		}
	}
}

func (l *Lobby) join(c *Client) error {
	if _, taken := l.players[c.Name]; taken {
		return ErrNameTaken
	}
	p := &player{c: c, status: StatusLobby}
	l.players[c.Name] = p
	c.Send(l.welcome(c.Name))
	return nil
}

func (l *Lobby) leave(c *Client) {
	p, ok := l.players[c.Name]
	if !ok || p.c != c {
		return
	}
	if p.invite != "" {
		l.endInvite(p.invite, "cancelled")
	}
	delete(l.players, c.Name)
}

func (l *Lobby) handle(c *Client, in Inbound) {
	p, ok := l.players[c.Name]
	if !ok || p.c != c {
		return
	}
	switch in.Type {
	case "chat":
		var m ChatIn
		if !decode(c, in, &m) || !checkText(c, m.Text) {
			return
		}
		if p.status != StatusLobby {
			c.Send(errorMsg("lobby", "not_in_lobby", "lobby chat is only available in the lobby"))
			return
		}
		l.broadcastLobby(encode("lobby", "chat", ChatOut{From: c.Name, Text: m.Text, ID: m.ID, SentAt: m.SentAt}), "")
	case "dm":
		var m DMIn
		if !decode(c, in, &m) || !checkText(c, m.Text) {
			return
		}
		to, ok := l.players[m.To]
		if !ok {
			c.Send(errorMsg("lobby", "no_such_player", m.To+" is not online"))
			return
		}
		// Direct messages reach a player wherever they are, lobby or room.
		to.c.Send(encode("lobby", "dm", DMOut{From: c.Name, Text: m.Text, ID: m.ID, SentAt: m.SentAt}))
	case "who":
		c.Send(encode("lobby", "players", PlayersOut{Players: l.playerList()}))
	case "invite":
		var m InviteIn
		if decode(c, in, &m) {
			l.invite(p, m.To)
		}
	case "invite_reply":
		var m InviteReplyIn
		if decode(c, in, &m) {
			l.inviteReply(p, m)
		}
	default:
		c.Send(errorMsg("lobby", "unknown_type", in.Type))
	}
}

func (l *Lobby) invite(from *player, toName string) {
	c := from.c
	to, ok := l.players[toName]
	switch {
	case !ok:
		c.Send(errorMsg("lobby", "no_such_player", toName+" is not online"))
	case to == from:
		c.Send(errorMsg("lobby", "invalid", "cannot invite yourself"))
	case from.status != StatusLobby || to.status != StatusLobby:
		c.Send(errorMsg("lobby", "busy", "both players must be in the lobby"))
	case from.invite != "" || to.invite != "":
		c.Send(errorMsg("lobby", "busy", "a pending invite already exists"))
	default:
		l.inviteSeq++
		id := fmt.Sprintf("inv%d", l.inviteSeq)
		inv := &invite{id: id, from: from.c.Name, to: toName}
		inv.timer = time.AfterFunc(InviteTTL, func() { l.events <- inviteExpired{id} })
		l.invites[id] = inv
		from.invite, to.invite = id, id
		msg := encode("lobby", "invite", InviteOut{InviteID: id, From: inv.from, To: inv.to})
		to.c.Send(msg)
		from.c.Send(msg)
	}
}

func (l *Lobby) inviteReply(p *player, m InviteReplyIn) {
	inv, ok := l.invites[m.InviteID]
	if !ok || inv.to != p.c.Name {
		p.c.Send(errorMsg("lobby", "no_such_invite", m.InviteID))
		return
	}
	if !m.Accept {
		l.endInvite(inv.id, "declined")
		return
	}
	from := l.players[inv.from] // still online: leaving cancels the invite
	l.endInvite(inv.id, "accepted")
	l.openRoom([]*player{from, p})
}

// endInvite removes an invite and tells both players how it ended.
func (l *Lobby) endInvite(id, result string) {
	inv, ok := l.invites[id]
	if !ok {
		return // already handled (e.g. accepted just before the timer fired)
	}
	inv.timer.Stop()
	delete(l.invites, id)
	msg := encode("lobby", "invite_result", InviteResultOut{InviteID: id, Result: result})
	for _, name := range []string{inv.from, inv.to} {
		if p, ok := l.players[name]; ok {
			p.invite = ""
			p.c.Send(msg)
		}
	}
}

func (l *Lobby) openRoom(members []*player) {
	l.roomSeq++
	id := fmt.Sprintf("r%d", l.roomSeq)
	clients := make([]*Client, len(members))
	for i, p := range members {
		clients[i] = p.c
	}
	r := newRoom(id, clients, l)
	l.rooms[id] = r
	l.stats.RoomsOpened.Add(1)
	for _, p := range members {
		p.status, p.roomID = StatusRoom, id
		// Set before the room starts so the gateway routes the member's next
		// room message correctly.
		p.c.room.Store(r)
	}
	go r.Run()
	l.broadcastLobby(encode("lobby", "rooms", RoomListOut{Op: "add", Room: r.Info()}), "")
}

func (l *Lobby) backToLobby(roomID string, c *Client) {
	p, ok := l.players[c.Name]
	if !ok || p.c != c || p.roomID != roomID {
		return
	}
	p.status, p.roomID = StatusLobby, ""
	c.room.Store(nil)
	// While in the room the player received no lobby updates: resync.
	c.Send(l.welcome(c.Name))
}

func (l *Lobby) roomClosed(id string) {
	r, ok := l.rooms[id]
	if !ok {
		return
	}
	delete(l.rooms, id)
	l.broadcastLobby(encode("lobby", "rooms", RoomListOut{Op: "remove", Room: RoomInfo{ID: r.ID}}), "")
}

// broadcastLobby sends to every player whose focus is the lobby. Players in a
// room do not get lobby chat or list updates; they resync when they return.
func (l *Lobby) broadcastLobby(msg []byte, except string) {
	for name, p := range l.players {
		if p.status == StatusLobby && name != except {
			p.c.Send(msg)
		}
	}
}

func (l *Lobby) welcome(you string) []byte {
	w := WelcomeOut{You: you, Players: l.playerList(), Rooms: make([]RoomInfo, 0, len(l.rooms))}
	for _, r := range l.rooms {
		w.Rooms = append(w.Rooms, r.Info())
	}
	sort.Slice(w.Rooms, func(i, j int) bool { return w.Rooms[i].ID < w.Rooms[j].ID })
	return encode("lobby", "welcome", w)
}

func (l *Lobby) playerList() []PlayerInfo {
	list := make([]PlayerInfo, 0, len(l.players))
	for _, p := range l.players {
		list = append(list, info(p))
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

func (l *Lobby) snapshot() Snapshot {
	s := Snapshot{Players: len(l.players), Rooms: len(l.rooms), Invites: len(l.invites)}
	for _, p := range l.players {
		if p.status == StatusLobby {
			s.InLobby++
		} else {
			s.InRooms++
		}
	}
	return s
}

func info(p *player) PlayerInfo {
	return PlayerInfo{Name: p.c.Name, Status: p.status, RoomID: p.roomID}
}

func decode(c *Client, in Inbound, v any) bool {
	if err := json.Unmarshal(in.Data, v); err != nil {
		c.Send(errorMsg(in.To, "bad_request", "invalid data for "+in.Type))
		return false
	}
	return true
}

func checkText(c *Client, text string) bool {
	if text == "" || len(text) > MaxTextLen {
		c.Send(errorMsg("lobby", "bad_request", fmt.Sprintf("text must be 1..%d bytes", MaxTextLen)))
		return false
	}
	return true
}
