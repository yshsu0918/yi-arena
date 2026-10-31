package main

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/heroiclabs/nakama-common/api"
	"github.com/heroiclabs/nakama-common/runtime"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/roompb"
)

// Notification codes sent by the lobby (Nakama reserves codes <= 0).
const (
	codeInviteReceived = 101
	codeInviteDeclined = 102
	codeInviteExpired  = 103
	codeGameReady      = 104
	codeRoomCancelled  = 105
	codeNote           = 200
)

// Player status shown to followers through Nakama's status stream.
const (
	statusIdle    = "idle"
	statusPlaying = "playing"
)

// Nakama stream modes 0..7 are built in; the room list uses its own mode.
// Every session joins it (hidden, so joining does not broadcast presences).
const (
	streamModeStatus = 1
	streamModeRooms  = 100
	streamRoomsLabel = "rooms"
)

const (
	roomCreating = "creating" // CreateRoom call in flight, not listed yet
	roomWaiting  = "waiting"
	roomPlaying  = "playing"
)

type member struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

type roomEntry struct {
	ID        string
	Members   []member
	Status    string
	Connected []string
	WSURL     string
	createdAt time.Time
}

type roomView struct {
	RoomID    string   `json:"room_id"`
	Members   []member `json:"members"`
	Status    string   `json:"status"`
	Connected []string `json:"connected"`
}

func (r *roomEntry) view() roomView {
	return roomView{RoomID: r.ID, Members: r.Members, Status: r.Status, Connected: append([]string{}, r.Connected...)}
}

func (r *roomEntry) hasMember(userID string) bool {
	for _, m := range r.Members {
		if m.UserID == userID {
			return true
		}
	}
	return false
}

type invite struct {
	ID        string
	From, To  member
	ExpiresAt time.Time
	timer     *time.Timer
}

// seqMark is the last applied event seq of a room. It is kept for a while
// after the room is removed so that late or repeated events are ignored.
type seqMark struct {
	seq       uint64
	removedAt time.Time
}

type stats struct {
	EventsApplied   int64  `json:"events_applied"`
	EventsIgnored   int64  `json:"events_ignored"`
	EventsAdopted   int64  `json:"events_adopted"` // event for a room the lobby did not know
	ReconcileRuns   int64  `json:"reconcile_runs"`
	ReconcileErrors int64  `json:"reconcile_errors"`
	ReconcileAdded  int64  `json:"reconcile_added"`
	ReconcileFixed  int64  `json:"reconcile_fixed"`
	ReconcileGhosts int64  `json:"reconcile_ghosts_removed"`
	LastReconcileOK string `json:"last_reconcile_ok"`
	LastReconcileEr string `json:"last_reconcile_error"`
	RoomInstanceID  string `json:"room_instance_id"`
	InvitesSent     int64  `json:"invites_sent"`
	InvitesExpired  int64  `json:"invites_expired"`
	RoomsCreated    int64  `json:"rooms_created"`
	eventLatencyMS  []float64
}

type lobby struct {
	cfg   config
	log   runtime.Logger
	nk    runtime.NakamaModule
	rooms roompb.RoomServiceClient

	mu          sync.Mutex
	table       map[string]*roomEntry // room ID -> entry
	userRoom    map[string]string     // user ID -> room ID
	invites     map[string]*invite    // invite ID -> invite
	userInvite  map[string]string     // user ID (inviter or invitee) -> invite ID
	seqs        map[string]seqMark
	listVersion uint64
	st          stats
}

func newLobby(cfg config, log runtime.Logger, nk runtime.NakamaModule, rooms roompb.RoomServiceClient) *lobby {
	return &lobby{
		cfg: cfg, log: log, nk: nk, rooms: rooms,
		table: map[string]*roomEntry{}, userRoom: map[string]string{},
		invites: map[string]*invite{}, userInvite: map[string]string{},
		seqs: map[string]seqMark{},
	}
}

// --- side effects (called with l.mu held, so pushes keep their order) ---

func (l *lobby) notify(userID string, code int, subject string, content map[string]any, persistent bool) {
	if err := l.nk.NotificationSend(context.Background(), userID, subject, content, code, "", persistent); err != nil {
		l.log.Warn("notification %d to %s failed: %v", code, userID, err)
	}
}

// setStatus updates the status of every live session of the user, which
// Nakama pushes to everyone who follows that user.
func (l *lobby) setStatus(userID, status string) {
	ps, err := l.nk.StreamUserList(streamModeStatus, userID, "", "", true, true)
	if err != nil {
		return
	}
	for _, p := range ps {
		if err := l.nk.StreamUserUpdate(streamModeStatus, userID, "", "", userID, p.GetSessionId(), false, false, status); err != nil {
			l.log.Debug("status update %s/%s: %v", userID, p.GetSessionId(), err)
		}
	}
}

func (l *lobby) statusOf(userID string) string {
	if _, ok := l.userRoom[userID]; ok {
		return statusPlaying
	}
	return statusIdle
}

type listPush struct {
	Op      string    `json:"op"` // "upsert" or "remove"
	Version uint64    `json:"version"`
	Room    *roomView `json:"room,omitempty"`
	RoomID  string    `json:"room_id"`
	Reason  string    `json:"reason,omitempty"`
}

// pushList sends a room list change to every connected session.
func (l *lobby) pushList(op string, r *roomEntry, reason string) {
	l.listVersion++
	p := listPush{Op: op, Version: l.listVersion, RoomID: r.ID, Reason: reason}
	if op == "upsert" {
		v := r.view()
		p.Room = &v
	}
	b, _ := json.Marshal(p)
	if err := l.nk.StreamSend(streamModeRooms, "", "", streamRoomsLabel, string(b), nil, true); err != nil {
		l.log.Warn("room list push failed: %v", err)
	}
}

// --- room table (l.mu held) ---

func (l *lobby) addRoom(r *roomEntry) {
	l.table[r.ID] = r
	for _, m := range r.Members {
		l.userRoom[m.UserID] = r.ID
	}
}

// removeRoom drops the room, sets its members back to idle and pushes the
// removal (unless the room was never listed).
func (l *lobby) removeRoom(r *roomEntry, reason string) {
	delete(l.table, r.ID)
	mark := l.seqs[r.ID]
	mark.removedAt = time.Now()
	l.seqs[r.ID] = mark
	for _, m := range r.Members {
		if l.userRoom[m.UserID] == r.ID {
			delete(l.userRoom, m.UserID)
			l.setStatus(m.UserID, statusIdle)
		}
	}
	if r.Status != roomCreating {
		l.pushList("remove", r, reason)
	}
}

// applyInfo copies room state reported by the room service.
func (r *roomEntry) applyInfo(info *roompb.RoomInfo) {
	switch info.GetStatus() {
	case roompb.RoomStatus_ROOM_STATUS_PLAYING:
		r.Status = roomPlaying
	default:
		r.Status = roomWaiting
	}
	r.Connected = append([]string{}, info.GetConnected()...)
	sort.Strings(r.Connected)
}

func entryFromInfo(info *roompb.RoomInfo) *roomEntry {
	r := &roomEntry{ID: info.GetRoomId(), createdAt: time.Now()}
	for _, m := range info.GetMembers() {
		r.Members = append(r.Members, member{UserID: m.GetUserId(), Name: m.GetName()})
	}
	r.applyInfo(info)
	return r
}

// pruneSeqs forgets tombstones of rooms removed long ago.
func (l *lobby) pruneSeqs() {
	for id, m := range l.seqs {
		if !m.removedAt.IsZero() && time.Since(m.removedAt) > 10*time.Minute {
			delete(l.seqs, id)
		}
	}
}

// --- invites (l.mu held) ---

func (l *lobby) dropInvite(inv *invite) {
	inv.timer.Stop()
	delete(l.invites, inv.ID)
	if l.userInvite[inv.From.UserID] == inv.ID {
		delete(l.userInvite, inv.From.UserID)
	}
	if l.userInvite[inv.To.UserID] == inv.ID {
		delete(l.userInvite, inv.To.UserID)
	}
}

func (l *lobby) expireInvite(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	inv, ok := l.invites[id]
	if !ok {
		return
	}
	l.dropInvite(inv)
	l.st.InvitesExpired++
	content := map[string]any{"invite_id": inv.ID, "from": inv.From, "to": inv.To}
	l.notify(inv.From.UserID, codeInviteExpired, "invite expired", content, false)
	l.notify(inv.To.UserID, codeInviteExpired, "invite expired", content, false)
}

// --- sessions ---

// onSessionStart subscribes every new session to the room list and sets its
// status from the room table.
func (l *lobby) onSessionStart(ctx context.Context, logger runtime.Logger, _ *api.Event) {
	userID, _ := ctx.Value(runtime.RUNTIME_CTX_USER_ID).(string)
	sessionID, _ := ctx.Value(runtime.RUNTIME_CTX_SESSION_ID).(string)
	if userID == "" || sessionID == "" {
		return
	}
	if _, err := l.nk.StreamUserJoin(streamModeRooms, "", "", streamRoomsLabel, userID, sessionID, true, false, ""); err != nil {
		logger.Warn("join room list stream: %v", err)
	}
	l.mu.Lock()
	status := l.statusOf(userID)
	l.mu.Unlock()
	if err := l.nk.StreamUserUpdate(streamModeStatus, userID, "", "", userID, sessionID, false, false, status); err != nil {
		logger.Debug("initial status: %v", err)
	}
}
