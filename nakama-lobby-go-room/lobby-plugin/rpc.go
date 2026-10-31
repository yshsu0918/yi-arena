package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/heroiclabs/nakama-common/runtime"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/roompb"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/ticket"
)

// gRPC status codes used for runtime errors.
const (
	codeInvalidArgument    = 3
	codeNotFound           = 5
	codePermissionDenied   = 7
	codeFailedPrecondition = 9
	codeUnavailable        = 14
)

var (
	errBadPayload   = runtime.NewError("bad_payload", codeInvalidArgument)
	errNoUser       = runtime.NewError("user_required", codePermissionDenied)
	errServerOnly   = runtime.NewError("server_only", codePermissionDenied)
	errNotFound     = runtime.NewError("user_not_found", codeNotFound)
	errSelf         = runtime.NewError("cannot_invite_self", codeInvalidArgument)
	errOffline      = runtime.NewError("offline", codeFailedPrecondition)
	errBusy         = runtime.NewError("busy", codeFailedPrecondition)
	errPending      = runtime.NewError("pending_invite", codeFailedPrecondition)
	errNoInvite     = runtime.NewError("invite_not_found", codeNotFound)
	errNotInRoom    = runtime.NewError("not_in_room", codeFailedPrecondition)
	errRoomService  = runtime.NewError("room_service_unavailable", codeUnavailable)
	errTicketSigner = runtime.NewError("ticket_error", codeUnavailable)
)

func caller(ctx context.Context) (userID, username string) {
	userID, _ = ctx.Value(runtime.RUNTIME_CTX_USER_ID).(string)
	username, _ = ctx.Value(runtime.RUNTIME_CTX_USERNAME).(string)
	return
}

func reply(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func (l *lobby) isOnline(userID string) bool {
	// Every realtime session of a user is on its notification stream (mode 0).
	ps, err := l.nk.StreamUserList(0, userID, "", "", true, true)
	return err == nil && len(ps) > 0
}

func (l *lobby) lookupUser(ctx context.Context, name string) (member, error) {
	users, err := l.nk.UsersGetUsername(ctx, []string{name})
	if err != nil || len(users) == 0 {
		return member{}, errNotFound
	}
	return member{UserID: users[0].GetId(), Name: users[0].GetUsername()}, nil
}

// invite {"to": "<username>"} -> {"invite_id", "expires_at"}
func (l *lobby) rpcInvite(ctx context.Context, _ runtime.Logger, _ *sql.DB, _ runtime.NakamaModule, payload string) (string, error) {
	userID, username := caller(ctx)
	if userID == "" {
		return "", errNoUser
	}
	var in struct {
		To string `json:"to"`
	}
	if json.Unmarshal([]byte(payload), &in) != nil || in.To == "" {
		return "", errBadPayload
	}
	to, err := l.lookupUser(ctx, in.To)
	if err != nil {
		return "", err
	}
	if to.UserID == userID {
		return "", errSelf
	}
	// First version: only online players can be invited.
	if !l.isOnline(to.UserID) {
		return "", errOffline
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	for _, u := range []string{userID, to.UserID} {
		if _, ok := l.userRoom[u]; ok {
			return "", errBusy
		}
		if _, ok := l.userInvite[u]; ok {
			return "", errPending
		}
	}
	inv := &invite{
		ID:        ticket.NewID(),
		From:      member{UserID: userID, Name: username},
		To:        to,
		ExpiresAt: time.Now().Add(l.cfg.inviteTimeout),
	}
	id := inv.ID
	inv.timer = time.AfterFunc(l.cfg.inviteTimeout, func() { l.expireInvite(id) })
	l.invites[id] = inv
	l.userInvite[userID] = id
	l.userInvite[to.UserID] = id
	l.st.InvitesSent++
	l.notify(to.UserID, codeInviteReceived, username+" invites you", map[string]any{
		"invite_id": id, "from": inv.From, "expires_at": inv.ExpiresAt.UnixMilli(),
	}, false)
	return reply(map[string]any{"invite_id": id, "expires_at": inv.ExpiresAt.UnixMilli()})
}

type ticketOut struct {
	RoomID    string `json:"room_id"`
	WSURL     string `json:"ws_url"`
	Ticket    string `json:"ticket"`
	ExpiresAt int64  `json:"expires_at"`
}

func (l *lobby) issue(r *roomEntry, m member) (ticketOut, error) {
	exp := time.Now().Add(l.cfg.ticketTTL)
	tok, _, err := ticket.Sign(l.cfg.ticketSecret, ticket.Claims{
		RoomID: r.ID, UserID: m.UserID, Name: m.Name, Role: ticket.RolePlayer, ExpiresAt: exp.Unix(),
	})
	if err != nil {
		return ticketOut{}, errTicketSigner
	}
	return ticketOut{RoomID: r.ID, WSURL: r.WSURL, Ticket: tok, ExpiresAt: exp.UnixMilli()}, nil
}

// invite_reply {"invite_id", "accept"} -> {"accepted"} plus the caller's
// ticket when accepted. Both players also get a game_ready notification.
func (l *lobby) rpcInviteReply(ctx context.Context, _ runtime.Logger, _ *sql.DB, _ runtime.NakamaModule, payload string) (string, error) {
	userID, _ := caller(ctx)
	if userID == "" {
		return "", errNoUser
	}
	var in struct {
		InviteID string `json:"invite_id"`
		Accept   bool   `json:"accept"`
	}
	if json.Unmarshal([]byte(payload), &in) != nil || in.InviteID == "" {
		return "", errBadPayload
	}

	l.mu.Lock()
	inv, ok := l.invites[in.InviteID]
	if !ok || inv.To.UserID != userID {
		l.mu.Unlock()
		return "", errNoInvite
	}
	l.dropInvite(inv)
	if !in.Accept {
		l.notify(inv.From.UserID, codeInviteDeclined, inv.To.Name+" declined", map[string]any{
			"invite_id": inv.ID, "to": inv.To,
		}, false)
		l.mu.Unlock()
		return reply(map[string]any{"accepted": false})
	}
	// Reserve both players while the room service creates the room: the
	// "creating" entry makes them busy but is not listed yet.
	r := &roomEntry{ID: ticket.NewID(), Members: []member{inv.From, inv.To}, Status: roomCreating, createdAt: time.Now()}
	l.addRoom(r)
	l.mu.Unlock()

	req := &roompb.CreateRoomRequest{RoomId: r.ID}
	for _, m := range r.Members {
		req.Members = append(req.Members, &roompb.Member{UserId: m.UserID, Name: m.Name})
	}
	cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	resp, err := l.rooms.CreateRoom(cctx, req)
	cancel()

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.table[r.ID] != r {
		return "", errRoomService // removed meanwhile (should not happen)
	}
	if err != nil {
		l.log.Error("CreateRoom %s: %v", r.ID, err)
		l.removeRoom(r, "create_failed")
		return "", errRoomService
	}
	r.WSURL = resp.GetWsUrl()
	if r.Status == roomCreating {
		r.Status = roomWaiting
	}
	l.st.RoomsCreated++
	l.pushList("upsert", r, "")

	var mine ticketOut
	for _, m := range r.Members {
		l.setStatus(m.UserID, statusPlaying)
		t, err := l.issue(r, m)
		if err != nil {
			return "", err
		}
		if m.UserID == userID {
			mine = t
		}
		l.notify(m.UserID, codeGameReady, "game ready", map[string]any{
			"room_id": t.RoomID, "ws_url": t.WSURL, "ticket": t.Ticket, "expires_at": t.ExpiresAt, "members": r.Members,
		}, false)
	}
	return reply(map[string]any{"accepted": true, "room_id": mine.RoomID, "ws_url": mine.WSURL, "ticket": mine.Ticket, "expires_at": mine.ExpiresAt})
}

type listOut struct {
	Version uint64     `json:"version"`
	Rooms   []roomView `json:"rooms"`
}

func (l *lobby) listRooms() listOut {
	out := listOut{Version: l.listVersion, Rooms: []roomView{}}
	for _, r := range l.table {
		if r.Status != roomCreating {
			out.Rooms = append(out.Rooms, r.view())
		}
	}
	sort.Slice(out.Rooms, func(i, j int) bool { return out.Rooms[i].RoomID < out.Rooms[j].RoomID })
	return out
}

// list_rooms -> {"version", "rooms": [...]}. Later changes arrive on the
// room list stream with higher versions.
func (l *lobby) rpcListRooms(ctx context.Context, _ runtime.Logger, _ *sql.DB, _ runtime.NakamaModule, _ string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return reply(l.listRooms())
}

// reissue_ticket -> a fresh ticket for the caller's current room (reconnect).
func (l *lobby) rpcReissueTicket(ctx context.Context, _ runtime.Logger, _ *sql.DB, _ runtime.NakamaModule, _ string) (string, error) {
	userID, username := caller(ctx)
	if userID == "" {
		return "", errNoUser
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.table[l.userRoom[userID]]
	if r == nil || r.Status == roomCreating || r.WSURL == "" {
		return "", errNotInRoom
	}
	t, err := l.issue(r, member{UserID: userID, Name: username})
	if err != nil {
		return "", err
	}
	return reply(t)
}

// send_note {"to": "<username>", "text"}: a persistent notification, which
// the receiver gets later if offline.
func (l *lobby) rpcSendNote(ctx context.Context, _ runtime.Logger, _ *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	userID, username := caller(ctx)
	if userID == "" {
		return "", errNoUser
	}
	var in struct {
		To   string `json:"to"`
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(payload), &in) != nil || in.To == "" || len(in.Text) > 500 {
		return "", errBadPayload
	}
	to, err := l.lookupUser(ctx, in.To)
	if err != nil {
		return "", err
	}
	if err := nk.NotificationSend(ctx, to.UserID, "note from "+username, map[string]any{"text": in.Text}, codeNote, userID, true); err != nil {
		return "", err
	}
	return reply(map[string]any{"sent": true})
}

// room_event: called by the room service through Nakama's gRPC API with the
// HTTP key. Payload is a protojson RoomEvent.
func (l *lobby) rpcRoomEvent(ctx context.Context, _ runtime.Logger, _ *sql.DB, _ runtime.NakamaModule, payload string) (string, error) {
	if u, _ := caller(ctx); u != "" {
		return "", errServerOnly
	}
	var ev roompb.RoomEvent
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(payload), &ev); err != nil || ev.GetRoomId() == "" {
		return "", errBadPayload
	}
	applied := l.applyEvent(&ev)
	return reply(map[string]any{"applied": applied})
}

func (l *lobby) applyEvent(ev *roompb.RoomEvent) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	mark := l.seqs[ev.RoomId]
	if ev.Seq <= mark.seq {
		l.st.EventsIgnored++
		return false
	}
	mark.seq = ev.Seq
	l.seqs[ev.RoomId] = mark
	l.st.EventsApplied++
	if ev.SentAtMs > 0 && len(l.st.eventLatencyMS) < 100_000 {
		l.st.eventLatencyMS = append(l.st.eventLatencyMS, float64(time.Now().UnixMicro()-ev.SentAtMs*1000)/1000)
	}

	terminal := ev.Type == "room.closed" || ev.Type == "room.cancelled"
	r := l.table[ev.RoomId]
	if r == nil {
		if terminal || ev.Room == nil {
			mark.removedAt = time.Now()
			l.seqs[ev.RoomId] = mark
			return true
		}
		// A room the lobby does not know (e.g. Nakama restarted): adopt it.
		r = entryFromInfo(ev.Room)
		l.addRoom(r)
		for _, m := range r.Members {
			l.setStatus(m.UserID, statusPlaying)
		}
		l.st.EventsAdopted++
		l.pushList("upsert", r, "")
		return true
	}
	if terminal {
		if ev.Type == "room.cancelled" {
			for _, m := range r.Members {
				l.notify(m.UserID, codeRoomCancelled, "room cancelled", map[string]any{"room_id": r.ID, "reason": ev.Reason}, false)
			}
		}
		l.removeRoom(r, strings.TrimPrefix(ev.Type, "room."))
		return true
	}
	if ev.Room != nil {
		creating := r.Status == roomCreating
		r.applyInfo(ev.Room)
		if creating {
			r.Status = roomCreating
			return true
		}
	}
	l.pushList("upsert", r, "")
	return true
}

// lobby_stats {"reset_latency": bool}: server to server, for tests.
func (l *lobby) rpcLobbyStats(ctx context.Context, _ runtime.Logger, _ *sql.DB, _ runtime.NakamaModule, payload string) (string, error) {
	if u, _ := caller(ctx); u != "" {
		return "", errServerOnly
	}
	var in struct {
		ResetLatency bool `json:"reset_latency"`
	}
	_ = json.Unmarshal([]byte(payload), &in)
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]any{
		"stats":           l.st,
		"rooms":           l.listRooms().Rooms,
		"rooms_total":     len(l.table),
		"pending_invites": len(l.invites),
		"busy_users":      len(l.userRoom),
		"event_latency":   summarize(l.st.eventLatencyMS),
	}
	if in.ResetLatency {
		l.st.eventLatencyMS = nil
	}
	return reply(out)
}

func summarize(xs []float64) map[string]float64 {
	if len(xs) == 0 {
		return map[string]float64{"n": 0}
	}
	s := append([]float64{}, xs...)
	sort.Float64s(s)
	at := func(p float64) float64 { return s[int(p*float64(len(s)-1))] }
	return map[string]float64{"n": float64(len(s)), "p50": at(0.50), "p95": at(0.95), "p99": at(0.99), "max": s[len(s)-1]}
}
