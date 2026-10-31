package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/heroiclabs/nakama-common/api"
	"github.com/heroiclabs/nakama-common/rtapi"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/bots/nk"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/bots/roomc"
)

// Notification codes from the lobby plugin, and Nakama's DM request.
const (
	codeDMRequest      = -1
	codeFriendRequest  = -2
	codeInviteReceived = 101
	codeInviteDeclined = 102
	codeInviteExpired  = 103
	codeGameReady      = 104
	codeRoomCancelled  = 105
	codeNote           = 200
)

type Env struct {
	NK        *nk.Client
	HTTPKey   string
	RoomStats string   // room service /stats URL
	Compose   []string // command prefix, e.g. docker compose -f ../docker-compose.yml
}

// --- report ---

type Check struct {
	Step   string `json:"step"`
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type Report struct {
	Scenario    string         `json:"scenario"`
	Title       string         `json:"title"`
	StartedAt   time.Time      `json:"started_at"`
	DurationSec float64        `json:"duration_sec"`
	Passed      bool           `json:"passed"`
	Checks      []Check        `json:"checks"`
	Metrics     map[string]any `json:"metrics,omitempty"`
	mu          sync.Mutex
}

// Check records one automatic check and prints it.
func (r *Report) Check(step, name string, ok bool, detail string, args ...any) bool {
	if len(args) > 0 {
		detail = fmt.Sprintf(detail, args...)
	}
	r.mu.Lock()
	r.Checks = append(r.Checks, Check{step, name, ok, detail})
	r.mu.Unlock()
	mark := "PASS"
	if !ok {
		mark = "FAIL"
	}
	fmt.Printf("  [%s] %s %s", mark, step, name)
	if detail != "" {
		fmt.Printf(" — %s", detail)
	}
	fmt.Println()
	return ok
}

// NoErr checks that err is nil.
func (r *Report) NoErr(step, name string, err error) bool {
	if err != nil {
		return r.Check(step, name, false, "%v", err)
	}
	return r.Check(step, name, true, "")
}

func (r *Report) Metric(key string, v any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Metrics == nil {
		r.Metrics = map[string]any{}
	}
	r.Metrics[key] = v
}

func (r *Report) finish() {
	r.Passed = len(r.Checks) > 0
	for _, c := range r.Checks {
		r.Passed = r.Passed && c.OK
	}
	r.DurationSec = time.Since(r.StartedAt).Seconds()
}

// errAbort stops a scenario after a failed check that later steps depend on.
type errAbort struct{ step string }

func (e errAbort) Error() string { return "aborted at " + e.step }

func must(ok bool, step string) error {
	if !ok {
		return errAbort{step}
	}
	return nil
}

// --- users ---

type User struct {
	Name string
	S    *nk.Session
	K    *nk.Socket
	env  *Env
}

func (e *Env) Login(ctx context.Context, name string) (*User, error) {
	s, err := e.NK.AuthenticateCustom(ctx, "bot-"+strings.ToLower(name), name)
	if err != nil {
		return nil, fmt.Errorf("login %s: %w", name, err)
	}
	u := &User{Name: name, S: s, env: e}
	return u, u.Connect(ctx, nil)
}

// Connect opens a new realtime socket (session token is reused).
func (u *User) Connect(ctx context.Context, onEvent func(*rtapi.Envelope, time.Time)) error {
	k, err := u.S.Connect(ctx, onEvent)
	if err != nil {
		return fmt.Errorf("socket %s: %w", u.Name, err)
	}
	u.K = k
	return nil
}

func (u *User) ID() string { return u.S.UserID }

func (u *User) Invite(ctx context.Context, to string) (string, error) {
	var out struct {
		InviteID string `json:"invite_id"`
	}
	err := u.S.RPC(ctx, "invite", map[string]string{"to": to}, &out)
	return out.InviteID, err
}

type TicketOut struct {
	Accepted  bool   `json:"accepted"`
	RoomID    string `json:"room_id"`
	WSURL     string `json:"ws_url"`
	Ticket    string `json:"ticket"`
	ExpiresAt int64  `json:"expires_at"`
}

func (u *User) Reply(ctx context.Context, inviteID string, accept bool) (TicketOut, error) {
	var out TicketOut
	err := u.S.RPC(ctx, "invite_reply", map[string]any{"invite_id": inviteID, "accept": accept}, &out)
	return out, err
}

func (u *User) Reissue(ctx context.Context) (TicketOut, error) {
	var out TicketOut
	err := u.S.RPC(ctx, "reissue_ticket", map[string]any{}, &out)
	return out, err
}

// WaitNote waits for a pushed notification and decodes its content.
func (u *User) WaitNote(from int, code int32, timeout time.Duration, content any) (*api.Notification, error) {
	ev, err := u.K.Wait(from, timeout, nk.Notification(code))
	if err != nil {
		return nil, fmt.Errorf("%s: no notification %d: %w", u.Name, code, err)
	}
	n := nk.FindNotification(ev, code)
	if content != nil {
		if err := nk.NotificationContent(n, content); err != nil {
			return n, err
		}
	}
	return n, nil
}

type RoomView struct {
	RoomID    string   `json:"room_id"`
	Status    string   `json:"status"`
	Connected []string `json:"connected"`
}

func (u *User) ListRooms(ctx context.Context) ([]RoomView, error) {
	var out struct {
		Rooms []RoomView `json:"rooms"`
	}
	err := u.S.RPC(ctx, "list_rooms", map[string]any{}, &out)
	return out.Rooms, err
}

// --- games ---

type Game struct {
	RoomID, WSURL string
	A, B          *User
	CA, CB        *roomc.Conn
	TicketA       string // already used
}

// StartGame runs invite -> accept -> both connect with their tickets.
func (e *Env) StartGame(ctx context.Context, a, b *User) (*Game, error) {
	ma, mb := a.K.Mark(), b.K.Mark()
	id, err := a.Invite(ctx, b.Name)
	if err != nil {
		return nil, fmt.Errorf("invite: %w", err)
	}
	if _, err := b.WaitNote(mb, codeInviteReceived, 5*time.Second, nil); err != nil {
		return nil, err
	}
	if _, err := b.Reply(ctx, id, true); err != nil {
		return nil, fmt.Errorf("accept: %w", err)
	}
	var ra, rb TicketOut
	if _, err := a.WaitNote(ma, codeGameReady, 5*time.Second, &ra); err != nil {
		return nil, err
	}
	if _, err := b.WaitNote(mb, codeGameReady, 5*time.Second, &rb); err != nil {
		return nil, err
	}
	g := &Game{RoomID: ra.RoomID, WSURL: ra.WSURL, A: a, B: b, TicketA: ra.Ticket}
	var first roomc.Msg
	if g.CA, first, err = roomc.Dial(ctx, ra.WSURL, ra.Ticket, nil); err != nil || first.Type != "welcome" {
		return nil, fmt.Errorf("%s join room: %v %+v", a.Name, err, first)
	}
	if g.CB, first, err = roomc.Dial(ctx, rb.WSURL, rb.Ticket, nil); err != nil || first.Type != "welcome" {
		return nil, fmt.Errorf("%s join room: %v %+v", b.Name, err, first)
	}
	return g, nil
}

// Leave has both players leave the room.
func (g *Game) Leave(ctx context.Context) {
	g.CA.Leave(ctx)
	g.CB.Leave(ctx)
}

// --- server-side views ---

type LobbyStats struct {
	Stats struct {
		EventsApplied   int64  `json:"events_applied"`
		EventsIgnored   int64  `json:"events_ignored"`
		EventsAdopted   int64  `json:"events_adopted"`
		ReconcileRuns   int64  `json:"reconcile_runs"`
		ReconcileAdded  int64  `json:"reconcile_added"`
		ReconcileGhosts int64  `json:"reconcile_ghosts_removed"`
		RoomInstanceID  string `json:"room_instance_id"`
		InvitesExpired  int64  `json:"invites_expired"`
		RoomsCreated    int64  `json:"rooms_created"`
	} `json:"stats"`
	Rooms          []RoomView         `json:"rooms"`
	RoomsTotal     int                `json:"rooms_total"`
	PendingInvites int                `json:"pending_invites"`
	BusyUsers      int                `json:"busy_users"`
	EventLatency   map[string]float64 `json:"event_latency"`
}

func (e *Env) LobbyStats(ctx context.Context, resetLatency bool) (LobbyStats, error) {
	var out LobbyStats
	err := e.NK.ServerRPC(ctx, e.HTTPKey, "lobby_stats", map[string]bool{"reset_latency": resetLatency}, &out)
	return out, err
}

type RoomStats struct {
	InstanceID    string  `json:"instance_id"`
	Goroutines    int     `json:"goroutines"`
	HeapAllocMB   float64 `json:"heap_alloc_mb"`
	SysMB         float64 `json:"sys_mb"`
	Rooms         int     `json:"rooms"`
	Connections   int64   `json:"connections"`
	SlowKicks     int64   `json:"slow_kicks"`
	EventsSent    int64   `json:"events_sent"`
	EventsRetries int64   `json:"events_retries"`
	EventsPending int     `json:"events_pending"`
}

func (e *Env) RoomServiceStats(ctx context.Context) (RoomStats, error) {
	var out RoomStats
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.RoomStats, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

// ComposeRun runs a docker compose command (restart, pause, ...).
func (e *Env) ComposeRun(args ...string) error {
	cmd := exec.Command(e.Compose[0], append(e.Compose[1:], args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

// WaitHealthy waits until Nakama answers its health check.
func (e *Env) WaitHealthy(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if e.NK.Healthy(ctx) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("nakama not healthy after %s", timeout)
}

// Eventually polls cond until it returns true or the timeout passes.
func Eventually(timeout time.Duration, cond func() bool) (time.Duration, bool) {
	start := time.Now()
	for {
		if cond() {
			return time.Since(start), true
		}
		if time.Since(start) > timeout {
			return time.Since(start), false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func ms(d time.Duration) string { return fmt.Sprintf("%.0f ms", float64(d.Microseconds())/1000) }
