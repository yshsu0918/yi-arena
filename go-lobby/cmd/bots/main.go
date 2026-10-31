// Command bots simulates many users against the lobby server and reports
// latency, delivery and isolation results.
//
// Timeline (each phase lasts duration/3):
//
//	phase 1: everyone is in the lobby; bots chat and send direct messages.
//	         Every phase-1 lobby chat must reach all N bots exactly once.
//	phase 2: the first `pairs` pairs invite each other and move into rooms,
//	         chat in the room; the rest keep chatting in the lobby.
//	         Room bots must not see lobby chat, nobody may see another room's chat.
//	phase 3: room bots leave their rooms and everyone chats in the lobby again.
//	         At the end every bot asks "who"; the list must show all N bots in the lobby.
//
// There is no presence broadcast, so the only player lists are the snapshot in
// "welcome" and the answer to "who".
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

type outbound struct {
	From string          `json:"from"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
	TS   int64           `json:"ts"`
}

type chatData struct {
	From   string `json:"from"`
	Text   string `json:"text"`
	ID     string `json:"id"`
	SentAt int64  `json:"sent_at"`
}

type playerInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

var (
	url       = flag.String("url", "ws://127.0.0.1:8910/ws", "lobby WebSocket URL")
	statsURL  = flag.String("stats", "http://127.0.0.1:8910/stats", "server stats URL")
	n         = flag.Int("n", 100, "number of bots")
	duration  = flag.Duration("duration", 30*time.Second, "total run time (three equal phases)")
	chatEvery = flag.Duration("chat-every", 2*time.Second, "mean interval between lobby chats per bot")
	pairs     = flag.Int("pairs", 25, "number of bot pairs that open rooms in phase 2")
	seed      = flag.Int64("seed", 1, "random seed")
	outFile   = flag.String("out", "", "write JSON results to this file")
)

// Shared bookkeeping across bots.
type tally struct {
	mu            sync.Mutex
	lobbyLatency  []time.Duration
	roomLatency   []time.Duration
	dmLatency     []time.Duration
	connect       []time.Duration
	phase1Sent    map[string]bool
	phase1Got     map[string]int // message id -> receivers
	dmSent        map[string]string
	dmGot         map[string]int
	violations    []string
	finalLists    map[string]int // bot -> players shown in lobby at the end
	errors        int
	lobbyChatSent atomic.Int64
	roomChatSent  atomic.Int64
	dmSentCount   atomic.Int64
	invitesOK     atomic.Int64
	roomsJoined   atomic.Int64
	received      atomic.Int64
}

func (t *tally) violation(format string, a ...any) {
	t.mu.Lock()
	if len(t.violations) < 20 {
		t.violations = append(t.violations, fmt.Sprintf(format, a...))
	} else if len(t.violations) == 20 {
		t.violations = append(t.violations, "...")
	}
	t.mu.Unlock()
}

type bot struct {
	id      int
	name    string
	conn    *websocket.Conn
	t       *tally
	rng     *rand.Rand
	partner string // bot it pairs with in phase 2 ("" if none)
	inviter bool

	mu      sync.Mutex
	room    string // current room id, "" when in the lobby
	who     chan []playerInfo
	joined  chan string // room ids, from the reader
	back    chan struct{}
	invites chan string // invite ids received
}

func main() {
	flag.Parse()
	if *pairs*2 > *n {
		*pairs = *n / 2
	}
	t := &tally{phase1Sent: map[string]bool{}, phase1Got: map[string]int{}, dmSent: map[string]string{}, dmGot: map[string]int{}, finalLists: map[string]int{}}
	before := fetchStats()

	bots := make([]*bot, *n)
	for i := range bots {
		b := &bot{id: i, name: fmt.Sprintf("bot%03d", i), t: t, rng: rand.New(rand.NewSource(*seed + int64(i))),
			who: make(chan []playerInfo, 1), joined: make(chan string, 4), back: make(chan struct{}, 4), invites: make(chan string, 4)}
		if i < *pairs*2 {
			if i%2 == 0 {
				b.partner, b.inviter = fmt.Sprintf("bot%03d", i+1), true
			} else {
				b.partner = fmt.Sprintf("bot%03d", i-1)
			}
		}
		bots[i] = b
	}

	// Connect everyone before starting the phases.
	start := time.Now()
	var wg sync.WaitGroup
	for _, b := range bots {
		wg.Add(1)
		go func(b *bot) {
			defer wg.Done()
			if err := b.connect(); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", b.name, err)
				t.mu.Lock()
				t.errors++
				t.mu.Unlock()
			}
		}(b)
	}
	wg.Wait()
	connectAll := time.Since(start)
	peak := fetchStats()

	phase := *duration / 3
	t0 := time.Now()
	for _, b := range bots {
		if b.conn == nil {
			continue
		}
		wg.Add(1)
		go func(b *bot) {
			defer wg.Done()
			b.run(t0, phase)
		}(b)
	}
	wg.Wait()
	time.Sleep(time.Second) // let the last messages arrive

	// Everyone asks for the player list; all N must be shown in the lobby.
	for _, b := range bots {
		if b.conn == nil {
			continue
		}
		wg.Add(1)
		go func(b *bot) {
			defer wg.Done()
			b.send("lobby", "who", nil)
			select {
			case list := <-b.who:
				inLobby := 0
				for _, p := range list {
					if p.Status == "lobby" {
						inLobby++
					}
				}
				t.mu.Lock()
				t.finalLists[b.name] = inLobby
				t.mu.Unlock()
			case <-time.After(5 * time.Second):
				t.violation("%s got no answer to who", b.name)
			}
		}(b)
	}
	wg.Wait()
	end := fetchStats()
	for _, b := range bots {
		if b.conn != nil {
			b.conn.Close(websocket.StatusNormalClosure, "")
		}
	}
	time.Sleep(time.Second)
	after := fetchStats()

	report(t, connectAll, before, peak, end, after)
}

func (b *bot) connect() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t0 := time.Now()
	conn, _, err := websocket.Dial(ctx, *url, nil)
	if err != nil {
		return err
	}
	conn.SetReadLimit(1 << 20)
	b.conn = conn
	welcome := make(chan struct{})
	go b.read(welcome)
	if err := b.send("lobby", "hello", map[string]string{"name": b.name}); err != nil {
		return err
	}
	select {
	case <-welcome:
	case <-ctx.Done():
		return ctx.Err()
	}
	b.t.mu.Lock()
	b.t.connect = append(b.t.connect, time.Since(t0))
	b.t.mu.Unlock()
	return nil
}

func (b *bot) send(to, typ string, data any) error {
	raw, _ := json.Marshal(data)
	msg, _ := json.Marshal(map[string]any{"to": to, "type": typ, "data": json.RawMessage(raw)})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return b.conn.Write(ctx, websocket.MessageText, msg)
}

func (b *bot) read(welcome chan struct{}) {
	var once sync.Once
	for {
		_, data, err := b.conn.Read(context.Background())
		if err != nil {
			return
		}
		now := time.Now()
		b.t.received.Add(1)
		var o outbound
		if json.Unmarshal(data, &o) != nil {
			continue
		}
		b.mu.Lock()
		room := b.room
		b.mu.Unlock()
		switch {
		case o.From == "lobby" && o.Type == "welcome":
			b.mu.Lock()
			wasInRoom := b.room != ""
			b.room = ""
			b.mu.Unlock()
			once.Do(func() { close(welcome) })
			if wasInRoom {
				b.back <- struct{}{}
			}
		case o.From == "lobby" && o.Type == "players":
			var p struct {
				Players []playerInfo `json:"players"`
			}
			json.Unmarshal(o.Data, &p)
			b.who <- p.Players
		case o.From == "lobby" && o.Type == "chat":
			var c chatData
			json.Unmarshal(o.Data, &c)
			if room != "" {
				b.t.violation("%s got lobby chat %s while in room %s", b.name, c.ID, room)
			}
			b.t.mu.Lock()
			b.t.lobbyLatency = append(b.t.lobbyLatency, now.Sub(time.Unix(0, c.SentAt)))
			if b.t.phase1Sent[c.ID] {
				b.t.phase1Got[c.ID]++
			}
			b.t.mu.Unlock()
		case o.From == "lobby" && o.Type == "dm":
			var c chatData
			json.Unmarshal(o.Data, &c)
			b.t.mu.Lock()
			b.t.dmLatency = append(b.t.dmLatency, now.Sub(time.Unix(0, c.SentAt)))
			if b.t.dmSent[c.ID] != b.name {
				b.t.violations = append(b.t.violations, fmt.Sprintf("%s got dm %s meant for %q", b.name, c.ID, b.t.dmSent[c.ID]))
			}
			b.t.dmGot[c.ID]++
			b.t.mu.Unlock()
		case o.From == "lobby" && o.Type == "invite":
			var inv struct {
				InviteID string `json:"invite_id"`
				To       string `json:"to"`
			}
			json.Unmarshal(o.Data, &inv)
			if inv.To == b.name {
				b.invites <- inv.InviteID
			}
		case o.Type == "room_joined":
			var j struct {
				RoomID string `json:"room_id"`
			}
			json.Unmarshal(o.Data, &j)
			b.mu.Lock()
			b.room = j.RoomID
			b.mu.Unlock()
			b.t.roomsJoined.Add(1)
			b.joined <- j.RoomID
		case len(o.From) > 5 && o.From[:5] == "room:" && o.Type == "chat":
			var c chatData
			json.Unmarshal(o.Data, &c)
			if o.From != "room:"+room {
				b.t.violation("%s (room %q) got chat from %s", b.name, room, o.From)
			}
			b.t.mu.Lock()
			b.t.roomLatency = append(b.t.roomLatency, now.Sub(time.Unix(0, c.SentAt)))
			b.t.mu.Unlock()
		case o.Type == "error":
			b.t.mu.Lock()
			b.t.errors++
			b.t.mu.Unlock()
			fmt.Fprintf(os.Stderr, "%s: error %s\n", b.name, o.Data)
		}
	}
}

func (b *bot) chat(phase int, seq *int) {
	*seq++
	id := fmt.Sprintf("%s-%d", b.name, *seq)
	if phase == 1 {
		b.t.mu.Lock()
		b.t.phase1Sent[id] = true
		b.t.mu.Unlock()
	}
	b.t.lobbyChatSent.Add(1)
	b.send("lobby", "chat", map[string]any{"text": "hello from " + b.name, "id": id, "sent_at": time.Now().UnixNano()})
}

func (b *bot) dm(seq *int) {
	*seq++
	id := fmt.Sprintf("%s-dm%d", b.name, *seq)
	to := fmt.Sprintf("bot%03d", b.rng.Intn(*n))
	b.t.mu.Lock()
	b.t.dmSent[id] = to
	b.t.mu.Unlock()
	b.t.dmSentCount.Add(1)
	b.send("lobby", "dm", map[string]any{"to": to, "text": "hi " + to, "id": id, "sent_at": time.Now().UnixNano()})
}

// lobbyChatter chats (and sometimes DMs) until the deadline.
func (b *bot) lobbyChatter(until time.Time, phase int, seq *int) {
	for {
		wait := time.Duration(b.rng.ExpFloat64() * float64(*chatEvery))
		if time.Now().Add(wait).After(until) {
			time.Sleep(time.Until(until))
			return
		}
		time.Sleep(wait)
		if b.rng.Intn(10) == 0 {
			b.dm(seq)
		} else {
			b.chat(phase, seq)
		}
	}
}

func (b *bot) run(t0 time.Time, phase time.Duration) {
	seq := 0
	// Phase 1: everyone chats in the lobby. Stop a little early so the last
	// phase-1 messages are delivered before anyone leaves the lobby.
	b.lobbyChatter(t0.Add(phase-time.Second), 1, &seq)
	time.Sleep(time.Until(t0.Add(phase)))

	// Phase 2.
	if b.partner == "" {
		b.lobbyChatter(t0.Add(2*phase), 2, &seq)
	} else {
		b.playRoom(t0.Add(2 * phase))
	}
	time.Sleep(time.Until(t0.Add(2 * phase)))

	// Phase 3: everyone back in the lobby.
	b.lobbyChatter(t0.Add(3*phase), 3, &seq)
}

func (b *bot) playRoom(until time.Time) {
	if b.inviter {
		time.Sleep(time.Duration(b.rng.Intn(500)) * time.Millisecond)
		b.send("lobby", "invite", map[string]string{"to": b.partner})
	} else {
		select {
		case id := <-b.invites:
			b.send("lobby", "invite_reply", map[string]any{"invite_id": id, "accept": true})
			b.t.invitesOK.Add(1)
		case <-time.After(time.Until(until)):
			b.t.violation("%s never got an invite", b.name)
			return
		}
	}
	select {
	case <-b.joined:
	case <-time.After(5 * time.Second):
		b.t.violation("%s never joined a room", b.name)
		return
	}
	for i := 0; i < 5; i++ {
		time.Sleep(time.Duration(200+b.rng.Intn(400)) * time.Millisecond)
		b.t.roomChatSent.Add(1)
		b.send("room", "chat", map[string]any{"text": "gg", "id": fmt.Sprintf("%s-r%d", b.name, i), "sent_at": time.Now().UnixNano()})
	}
	time.Sleep(time.Until(until.Add(-2 * time.Second)))
	b.send("room", "leave", nil)
	select {
	case <-b.back:
	case <-time.After(5 * time.Second):
		b.t.violation("%s did not get back to the lobby", b.name)
	}
}

func fetchStats() map[string]any {
	resp, err := http.Get(*statsURL)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	return m
}

func pct(ds []time.Duration) map[string]string {
	if len(ds) == 0 {
		return nil
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	at := func(p float64) string { return s[int(p*float64(len(s)-1))].Round(10 * time.Microsecond).String() }
	return map[string]string{"count": fmt.Sprint(len(s)), "p50": at(0.5), "p95": at(0.95), "p99": at(0.99), "max": s[len(s)-1].Round(10 * time.Microsecond).String()}
}

func report(t *tally, connectAll time.Duration, before, peak, end, after map[string]any) {
	t.mu.Lock()
	defer t.mu.Unlock()

	phase1Complete, phase1Short := 0, 0
	for id := range t.phase1Sent {
		if t.phase1Got[id] == *n {
			phase1Complete++
		} else {
			phase1Short++
			if phase1Short <= 5 {
				t.violations = append(t.violations, fmt.Sprintf("phase-1 chat %s reached %d of %d", id, t.phase1Got[id], *n))
			}
		}
	}
	dmOK := 0
	for id := range t.dmSent {
		if t.dmGot[id] == 1 {
			dmOK++
		}
	}
	listOK := 0
	for _, v := range t.finalLists {
		if v == *n {
			listOK++
		}
	}

	res := map[string]any{
		"bots":                 *n,
		"duration":             duration.String(),
		"chat_every":           chatEvery.String(),
		"pairs":                *pairs,
		"connect_all":          connectAll.Round(time.Millisecond).String(),
		"connect_latency":      pct(t.connect),
		"lobby_chat_sent":      t.lobbyChatSent.Load(),
		"lobby_chat_latency":   pct(t.lobbyLatency),
		"phase1_complete":      fmt.Sprintf("%d/%d", phase1Complete, len(t.phase1Sent)),
		"dm_sent":              t.dmSentCount.Load(),
		"dm_delivered_once":    fmt.Sprintf("%d/%d", dmOK, len(t.dmSent)),
		"dm_latency":           pct(t.dmLatency),
		"invites_accepted":     t.invitesOK.Load(),
		"room_joins":           t.roomsJoined.Load(),
		"room_chat_sent":       t.roomChatSent.Load(),
		"room_chat_latency":    pct(t.roomLatency),
		"messages_received":    t.received.Load(),
		"final_player_lists":   fmt.Sprintf("%d/%d bots see all %d players in the lobby", listOK, len(t.finalLists), *n),
		"errors":               t.errors,
		"violations":           t.violations,
		"server_before":        before,
		"server_all_connected": peak,
		"server_end":           end,
		"server_after_close":   after,
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))
	if *outFile != "" {
		os.WriteFile(*outFile, out, 0o644)
	}
}
