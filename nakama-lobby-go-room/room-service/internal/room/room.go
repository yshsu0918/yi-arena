// Package room runs rooms: one goroutine per room that owns its members,
// connections and chat, and reports room events to the lobby.
package room

import (
	"errors"
	"log"
	"sort"
	"sync/atomic"
	"time"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/roompb"
)

// Event types reported to the lobby.
const (
	EventStarted      = "room.started"
	EventDisconnected = "room.player_disconnected"
	EventReconnected  = "room.player_reconnected"
	EventClosed       = "room.closed"
	EventCancelled    = "room.cancelled"
)

var (
	ErrClosed    = errors.New("room_closed")
	ErrNotMember = errors.New("not_a_member")
	ErrLeft      = errors.New("already_left")
)

// Emitter delivers room events to the lobby. Emit must not block.
type Emitter interface {
	Emit(ev *roompb.RoomEvent)
}

type Config struct {
	// Every member must connect within this time or the room is cancelled.
	ArrivalTimeout time.Duration
	// A member who disconnects and does not come back within this time is
	// treated as having left.
	DisconnectGrace time.Duration
}

type Room struct {
	ID      string
	members []*roompb.Member
	cfg     Config
	emitter Emitter
	onDone  func(*Room)

	events chan any
	closed chan struct{}
	info   atomic.Pointer[roompb.RoomInfo]

	// Owned by the room goroutine.
	conns    map[string]*Conn // user ID -> current connection
	arrived  map[string]bool  // connected at least once
	left     map[string]bool  // left for good
	graceGen map[string]uint64
	status   roompb.RoomStatus
	seq      uint64
	arrival  *time.Timer
	finished bool
}

type (
	joinReq struct {
		c     *Conn
		reply chan error
	}
	inbound struct {
		c  *Conn
		in Inbound
	}
	disconnected struct{ c *Conn }
	arrivalFired struct{}
	graceFired   struct {
		userID string
		gen    uint64
	}
	shutdown struct{ reason string }
)

func newRoom(id string, members []*roompb.Member, cfg Config, emitter Emitter, onDone func(*Room)) *Room {
	r := &Room{
		ID: id, members: members, cfg: cfg, emitter: emitter, onDone: onDone,
		events: make(chan any, 256), closed: make(chan struct{}),
		conns: map[string]*Conn{}, arrived: map[string]bool{}, left: map[string]bool{}, graceGen: map[string]uint64{},
		status: roompb.RoomStatus_ROOM_STATUS_WAITING,
	}
	r.publish()
	return r
}

// --- called from other goroutines; none of these block on a closed room ---

// Join attaches a connection whose ticket the gateway has already checked.
func (r *Room) Join(c *Conn) error {
	reply := make(chan error, 1)
	select {
	case r.events <- joinReq{c, reply}:
	case <-r.closed:
		return ErrClosed
	}
	select {
	case err := <-reply:
		return err
	case <-r.closed:
		return ErrClosed
	}
}

func (r *Room) Deliver(c *Conn, in Inbound) { r.post(inbound{c, in}) }
func (r *Room) Disconnect(c *Conn)          { r.post(disconnected{c}) }
func (r *Room) Close(reason string)         { r.post(shutdown{reason}) }

// Info is the latest snapshot, safe to read from any goroutine.
func (r *Room) Info() *roompb.RoomInfo { return r.info.Load() }
func (r *Room) Done() <-chan struct{}  { return r.closed }

func (r *Room) post(ev any) {
	select {
	case r.events <- ev:
	case <-r.closed:
	}
}

// --- room goroutine ---

func (r *Room) Run() {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("room %s panicked: %v", r.ID, p)
			r.finish(EventClosed, "panic")
		}
	}()
	r.arrival = time.AfterFunc(r.cfg.ArrivalTimeout, func() { r.post(arrivalFired{}) })
	for !r.finished {
		switch e := (<-r.events).(type) {
		case joinReq:
			r.join(e.c, e.reply)
		case inbound:
			r.handle(e.c, e.in)
		case disconnected:
			r.disconnect(e.c)
		case graceFired:
			if e.gen == r.graceGen[e.userID] && r.conns[e.userID] == nil && !r.left[e.userID] {
				r.leave(e.userID)
			}
		case arrivalFired:
			if r.status == roompb.RoomStatus_ROOM_STATUS_WAITING {
				r.finish(EventCancelled, "arrival_timeout")
			}
		case shutdown:
			r.finish(EventClosed, e.reason)
		}
	}
}

func (r *Room) member(userID string) *roompb.Member {
	for _, m := range r.members {
		if m.GetUserId() == userID {
			return m
		}
	}
	return nil
}

func (r *Room) join(c *Conn, reply chan error) {
	switch {
	case r.member(c.UserID) == nil:
		reply <- ErrNotMember
		return
	case r.left[c.UserID]:
		reply <- ErrLeft
		return
	}
	old := r.conns[c.UserID]
	if old != nil { // the same user connected again: the newer connection wins
		old.Send(ErrorMsg("replaced"))
		old.Close()
	}
	first := !r.arrived[c.UserID]
	r.conns[c.UserID] = c
	r.arrived[c.UserID] = true
	r.graceGen[c.UserID]++ // cancels a pending disconnect grace timer
	reply <- nil

	starting := r.status == roompb.RoomStatus_ROOM_STATUS_WAITING && r.allArrived()
	if starting {
		r.status = roompb.RoomStatus_ROOM_STATUS_PLAYING
		r.arrival.Stop()
	}
	info := r.snapshot()
	w := WelcomeOut{Type: "welcome", RoomID: r.ID, You: c.UserID, Connected: info.Connected, Status: statusName(r.status)}
	for _, m := range r.members {
		w.Members = append(w.Members, MemberOut{UserID: m.GetUserId(), Name: m.GetName()})
	}
	c.Send(encode(w))
	r.broadcastExcept(c.UserID, encode(MemberEventOut{Type: "member_joined", UserID: c.UserID, Name: c.Name}))

	switch {
	case starting:
		r.emit(EventStarted, "", "")
	case r.status == roompb.RoomStatus_ROOM_STATUS_PLAYING && !first && old == nil:
		r.emit(EventReconnected, c.UserID, "")
	}
	r.publish()
}

func (r *Room) allArrived() bool {
	for _, m := range r.members {
		if !r.arrived[m.GetUserId()] {
			return false
		}
	}
	return true
}

func (r *Room) handle(c *Conn, in Inbound) {
	if r.conns[c.UserID] != c {
		c.Send(ErrorMsg("not_a_member"))
		return
	}
	switch in.Type {
	case "chat":
		if in.Text == "" || len(in.Text) > maxChat {
			c.Send(ErrorMsg("bad_text"))
			return
		}
		r.broadcastExcept("", encode(ChatOut{Type: "chat", From: c.Name, UserID: c.UserID, Text: in.Text, ID: in.ID, SentAt: in.SentAt}))
	case "leave":
		c.Send(encode(MemberEventOut{Type: "left", UserID: c.UserID, Name: c.Name}))
		c.Close()
		r.leave(c.UserID)
	default:
		c.Send(ErrorMsg("unknown_type"))
	}
}

func (r *Room) disconnect(c *Conn) {
	if r.conns[c.UserID] != c {
		return // an old connection, already replaced or left
	}
	delete(r.conns, c.UserID)
	r.broadcastExcept("", encode(MemberEventOut{Type: "member_disconnected", UserID: c.UserID, Name: c.Name}))
	if r.status == roompb.RoomStatus_ROOM_STATUS_PLAYING {
		r.emit(EventDisconnected, c.UserID, "")
	}
	r.graceGen[c.UserID]++
	userID, gen := c.UserID, r.graceGen[c.UserID]
	time.AfterFunc(r.cfg.DisconnectGrace, func() { r.post(graceFired{userID, gen}) })
	r.publish()
}

// leave marks a member as gone for good; the room closes when all are gone.
func (r *Room) leave(userID string) {
	delete(r.conns, userID)
	r.left[userID] = true
	m := r.member(userID)
	r.broadcastExcept("", encode(MemberEventOut{Type: "member_left", UserID: userID, Name: m.GetName()}))
	for _, m := range r.members {
		if !r.left[m.GetUserId()] {
			r.publish()
			return
		}
	}
	r.finish(EventClosed, "empty")
}

// finish ends the room: tells connected members, reports the event and stops
// the goroutine. Messages posted afterwards are dropped.
func (r *Room) finish(eventType, reason string) {
	if r.finished {
		return
	}
	r.finished = true
	if r.arrival != nil {
		r.arrival.Stop()
	}
	bye := encode(ClosedOut{Type: "room_closed", Reason: reason})
	for id, c := range r.conns {
		c.Send(bye)
		c.Close()
		delete(r.conns, id)
	}
	r.emit(eventType, "", reason)
	r.publish()
	close(r.closed)
	if r.onDone != nil {
		r.onDone(r)
	}
}

func (r *Room) broadcastExcept(userID string, msg []byte) {
	for id, c := range r.conns {
		if id != userID {
			c.Send(msg)
		}
	}
}

func (r *Room) emit(eventType, userID, reason string) {
	r.seq++
	r.emitter.Emit(&roompb.RoomEvent{
		Type: eventType, RoomId: r.ID, Seq: r.seq, UserId: userID,
		Room: r.snapshot(), SentAtMs: time.Now().UnixMilli(), Reason: reason,
	})
}

func (r *Room) snapshot() *roompb.RoomInfo {
	info := &roompb.RoomInfo{RoomId: r.ID, Members: r.members, Status: r.status, Seq: r.seq, Connected: []string{}}
	for id := range r.conns {
		info.Connected = append(info.Connected, id)
	}
	sort.Strings(info.Connected)
	return info
}

func (r *Room) publish() { r.info.Store(r.snapshot()) }

func statusName(s roompb.RoomStatus) string {
	if s == roompb.RoomStatus_ROOM_STATUS_PLAYING {
		return "playing"
	}
	return "waiting"
}
