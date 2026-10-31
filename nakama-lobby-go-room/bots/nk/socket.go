package nk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/heroiclabs/nakama-common/api"
	"github.com/heroiclabs/nakama-common/rtapi"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Event is a message pushed by Nakama (not a reply to a request).
type Event struct {
	Env *rtapi.Envelope
	At  time.Time
}

// Socket is a realtime connection. Pushed events are kept in a history so a
// test can wait for "the next event matching X after mark M".
type Socket struct {
	conn    *websocket.Conn
	onEvent func(*rtapi.Envelope, time.Time)

	cid     atomic.Int64
	mu      sync.Mutex
	pending map[string]chan *rtapi.Envelope
	history []Event
	changed chan struct{}
	frozen  chan struct{}
	freeze  sync.Once
	done    chan struct{}
	closeMu sync.Once
}

// Connect opens the realtime socket with status=true, so the user appears
// online to followers. onEvent (optional) is called for every pushed event
// on the reader goroutine.
func (s *Session) Connect(ctx context.Context, onEvent func(*rtapi.Envelope, time.Time)) (*Socket, error) {
	u, err := url.Parse(s.c.BaseURL)
	if err != nil {
		return nil, err
	}
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	q := url.Values{"token": {s.Token}, "status": {"true"}, "format": {"json"}}
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("%s://%s/ws?%s", scheme, u.Host, q.Encode()), nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(1 << 20)
	k := &Socket{
		conn: conn, onEvent: onEvent, pending: map[string]chan *rtapi.Envelope{},
		changed: make(chan struct{}), frozen: make(chan struct{}), done: make(chan struct{}),
	}
	go k.read()
	return k, nil
}

func (k *Socket) read() {
	defer k.markDone()
	for {
		select {
		case <-k.frozen:
			<-k.done // stop reading (and answering pings) until closed
			return
		default:
		}
		_, data, err := k.conn.Read(context.Background())
		if err != nil {
			return
		}
		at := time.Now()
		var env rtapi.Envelope
		if decode(data, &env) != nil {
			continue
		}
		if env.Cid != "" {
			k.mu.Lock()
			ch := k.pending[env.Cid]
			delete(k.pending, env.Cid)
			k.mu.Unlock()
			if ch != nil {
				ch <- &env
			}
			continue
		}
		if k.onEvent != nil {
			k.onEvent(&env, at)
		}
		k.mu.Lock()
		k.history = append(k.history, Event{&env, at})
		close(k.changed)
		k.changed = make(chan struct{})
		k.mu.Unlock()
	}
}

func (k *Socket) markDone() { k.closeMu.Do(func() { close(k.done) }) }

// Done is closed when the socket is closed (by either side).
func (k *Socket) Done() <-chan struct{} { return k.done }

// Close closes the socket cleanly (Nakama sees the user go offline at once).
func (k *Socket) Close() {
	k.conn.Close(websocket.StatusNormalClosure, "")
	k.markDone()
}

// Freeze stops reading: the client no longer answers Nakama's pings, like a
// hung client or a dead network path. Nakama notices only via ping timeout.
func (k *Socket) Freeze() { k.freeze.Do(func() { close(k.frozen) }) }

func (k *Socket) request(ctx context.Context, env *rtapi.Envelope) (*rtapi.Envelope, error) {
	env.Cid = strconv.FormatInt(k.cid.Add(1), 10)
	ch := make(chan *rtapi.Envelope, 1)
	k.mu.Lock()
	k.pending[env.Cid] = ch
	k.mu.Unlock()
	data, err := marshal.Marshal(env)
	if err != nil {
		return nil, err
	}
	if err := k.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return nil, err
	}
	select {
	case reply := <-ch:
		if e := reply.GetError(); e != nil {
			return nil, fmt.Errorf("nakama socket error %d: %s", e.Code, e.Message)
		}
		return reply, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-k.done:
		return nil, errors.New("socket closed")
	}
}

// FollowStatus follows users and returns the ones online right now.
func (k *Socket) FollowStatus(ctx context.Context, userIDs ...string) ([]*rtapi.UserPresence, error) {
	reply, err := k.request(ctx, &rtapi.Envelope{Message: &rtapi.Envelope_StatusFollow{StatusFollow: &rtapi.StatusFollow{UserIds: userIDs}}})
	if err != nil {
		return nil, err
	}
	return reply.GetStatus().GetPresences(), nil
}

// JoinDM joins the direct-message channel with another user.
func (k *Socket) JoinDM(ctx context.Context, userID string) (string, error) {
	reply, err := k.request(ctx, &rtapi.Envelope{Message: &rtapi.Envelope_ChannelJoin{ChannelJoin: &rtapi.ChannelJoin{
		Target: userID, Type: int32(rtapi.ChannelJoin_DIRECT_MESSAGE),
		Persistence: wrapperspb.Bool(true), Hidden: wrapperspb.Bool(false),
	}}})
	if err != nil {
		return "", err
	}
	return reply.GetChannel().GetId(), nil
}

// SendChannel sends a JSON object to a channel and waits for the ack.
func (k *Socket) SendChannel(ctx context.Context, channelID string, content any) error {
	b, _ := json.Marshal(content)
	_, err := k.request(ctx, &rtapi.Envelope{Message: &rtapi.Envelope_ChannelMessageSend{ChannelMessageSend: &rtapi.ChannelMessageSend{
		ChannelId: channelID, Content: string(b),
	}}})
	return err
}

// Mark is the current position in the event history.
func (k *Socket) Mark() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.history)
}

// Wait returns the first event at or after position from that matches.
func (k *Socket) Wait(from int, timeout time.Duration, match func(*rtapi.Envelope) bool) (*rtapi.Envelope, error) {
	deadline := time.After(timeout)
	for i := from; ; {
		k.mu.Lock()
		for ; i < len(k.history); i++ {
			if match(k.history[i].Env) {
				ev := k.history[i].Env
				k.mu.Unlock()
				return ev, nil
			}
		}
		changed := k.changed
		k.mu.Unlock()
		select {
		case <-changed:
		case <-deadline:
			return nil, fmt.Errorf("timed out after %s", timeout)
		}
	}
}

// Count counts events at or after position from that match.
func (k *Socket) Count(from int, match func(*rtapi.Envelope) bool) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for _, ev := range k.history[min(from, len(k.history)):] {
		if match(ev.Env) {
			n++
		}
	}
	return n
}

// --- matchers ---

// Notification matches a pushed notification with the given code.
func Notification(code int32) func(*rtapi.Envelope) bool {
	return func(e *rtapi.Envelope) bool { return FindNotification(e, code) != nil }
}

func FindNotification(e *rtapi.Envelope, code int32) *api.Notification {
	for _, n := range e.GetNotifications().GetNotifications() {
		if n.Code == code {
			return n
		}
	}
	return nil
}

// NotificationContent decodes the JSON content of a notification.
func NotificationContent(n *api.Notification, out any) error {
	return json.Unmarshal([]byte(n.Content), out)
}

// StatusJoin matches a status event where the user is (now) online with the
// given status ("" matches any status).
func StatusJoin(userID, status string) func(*rtapi.Envelope) bool {
	return func(e *rtapi.Envelope) bool {
		for _, p := range e.GetStatusPresenceEvent().GetJoins() {
			if p.UserId == userID && (status == "" || p.GetStatus().GetValue() == status) {
				return true
			}
		}
		return false
	}
}

// StatusLeave matches a status event where the user's session went away and
// no session of the user joined in the same event.
func StatusLeave(userID string) func(*rtapi.Envelope) bool {
	return func(e *rtapi.Envelope) bool {
		ev := e.GetStatusPresenceEvent()
		left := false
		for _, p := range ev.GetLeaves() {
			left = left || p.UserId == userID
		}
		for _, p := range ev.GetJoins() {
			if p.UserId == userID {
				return false
			}
		}
		return left
	}
}

// RoomListPush is a change on the lobby's room list stream.
type RoomListPush struct {
	Op      string `json:"op"`
	Version uint64 `json:"version"`
	RoomID  string `json:"room_id"`
	Reason  string `json:"reason"`
	Room    *struct {
		RoomID    string   `json:"room_id"`
		Status    string   `json:"status"`
		Connected []string `json:"connected"`
	} `json:"room"`
}

// RoomConnected matches a room list upsert where n members are connected.
func RoomConnected(roomID string, n int) func(*rtapi.Envelope) bool {
	return func(e *rtapi.Envelope) bool {
		sd := e.GetStreamData()
		if sd == nil || sd.Stream.GetLabel() != "rooms" {
			return false
		}
		var p RoomListPush
		return json.Unmarshal([]byte(sd.Data), &p) == nil && p.RoomID == roomID && p.Op == "upsert" &&
			p.Room != nil && len(p.Room.Connected) == n
	}
}

// RoomList matches a room list push for the room with the given op and
// (for upserts, if not "") status.
func RoomList(roomID, op, status string) func(*rtapi.Envelope) bool {
	return func(e *rtapi.Envelope) bool {
		sd := e.GetStreamData()
		if sd == nil || sd.Stream.GetLabel() != "rooms" {
			return false
		}
		var p RoomListPush
		if json.Unmarshal([]byte(sd.Data), &p) != nil || p.RoomID != roomID || p.Op != op {
			return false
		}
		return status == "" || (p.Room != nil && p.Room.Status == status)
	}
}
