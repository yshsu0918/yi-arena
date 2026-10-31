package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heroiclabs/nakama-common/rtapi"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/bots/nk"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/bots/roomc"
)

type loadConfig struct {
	Users    int
	Pairs    int
	Duration time.Duration
	DMEvery  time.Duration // average gap between one user's DMs
	Seed     uint64
}

type loadUser struct {
	*User
	idx     int
	friends []int
	dm      map[int]string // friend index -> channel ID

	mu         sync.Mutex
	lastStatus map[string]string // user ID -> last status seen from follows
}

// dmLog tracks every DM: who it was for and who received it.
type dmLog struct {
	mu       sync.Mutex
	to       map[string]int   // message ID -> intended receiver index
	got      map[string][]int // message ID -> receiver indexes
	latMS    []float64
	sendErrs int
}

type pairResult struct {
	OK          bool
	Err         string
	ConnectedMS float64 // accept -> both connected to the room
}

// Scenario E: 100 users, friends, DMs and 25 games at the same time.
func scenarioE(ctx context.Context, e *Env, r *Report, cfg loadConfig) error {
	r.Title = fmt.Sprintf("%d 人負載", cfg.Users)
	rng := rand.New(rand.NewPCG(cfg.Seed, 1))
	dms := &dmLog{to: map[string]int{}, got: map[string][]int{}}
	var chatMu sync.Mutex
	var chatLat []float64

	// E1: log in (custom auth + realtime socket), 20 at a time.
	users := make([]*loadUser, cfg.Users)
	loginMS := make([]float64, cfg.Users)
	t0 := time.Now()
	errs := parallel(cfg.Users, 20, func(i int) error {
		lu := &loadUser{idx: i, dm: map[int]string{}, lastStatus: map[string]string{}}
		users[i] = lu
		start := time.Now()
		s, err := e.NK.AuthenticateCustom(ctx, fmt.Sprintf("bot-load-%03d", i), fmt.Sprintf("U%03d", i))
		if err != nil {
			return err
		}
		lu.User = &User{Name: s.Username, S: s, env: e}
		err = lu.Connect(ctx, func(env *rtapi.Envelope, at time.Time) { lu.onEvent(env, at, dms) })
		loginMS[i] = float64(time.Since(start).Microseconds()) / 1000
		return err
	})
	loginWall := time.Since(t0)
	if !r.Check("E1", fmt.Sprintf("%d 位使用者登入", cfg.Users), countNil(errs) == cfg.Users, "成功 %d，總時間 %s；%v", countNil(errs), ms(loginWall), firstErr(errs)) {
		return errAbort{"E1"}
	}
	r.Metric("login_ms", summarize(loginMS))
	r.Metric("login_wall_ms", loginWall.Milliseconds())

	// E1: random friendships, 3-5 per user.
	edges := map[[2]int]bool{}
	degree := make([]int, cfg.Users)
	for i := range users {
		want := 3 + rng.IntN(3)
		for tries := 0; degree[i] < want && tries < 1000; tries++ {
			j := rng.IntN(cfg.Users)
			k := [2]int{min(i, j), max(i, j)}
			if j == i || edges[k] || degree[j] >= 6 {
				continue
			}
			edges[k] = true
			degree[i]++
			degree[j]++
			users[i].friends = append(users[i].friends, j)
			users[j].friends = append(users[j].friends, i)
		}
	}
	edgeList := make([][2]int, 0, len(edges))
	for k := range edges {
		edgeList = append(edgeList, k)
	}
	sort.Slice(edgeList, func(a, b int) bool { return edgeList[a][0]*1000+edgeList[a][1] < edgeList[b][0]*1000+edgeList[b][1] })
	// Nakama occasionally answers 500 when many friend requests touching the
	// same user run at once (user_edge_pkey includes a nanosecond timestamp).
	// A client would retry; the retries are counted and reported.
	var retryMu sync.Mutex
	friend500 := 0
	addFriend := func(from, to *loadUser) error {
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			if err = from.S.AddFriends(ctx, to.Name); err == nil {
				return nil
			}
			if e, ok := err.(*nk.Error); !ok || e.Status != 500 {
				return err
			}
			retryMu.Lock()
			friend500++
			retryMu.Unlock()
			time.Sleep(50 * time.Millisecond)
		}
		return err
	}
	t0 = time.Now()
	errs = parallel(len(edgeList), 20, func(n int) error {
		a, b := users[edgeList[n][0]], users[edgeList[n][1]]
		if err := addFriend(a, b); err != nil {
			return err
		}
		return addFriend(b, a)
	})
	r.Check("E1", "建立好友關係（雙方確認）", countNil(errs) == len(edgeList), "%d 對，%s；Nakama 回 500 後重試 %d 次；%v", len(edgeList), ms(time.Since(t0)), friend500, firstErr(errs))
	r.Metric("friend_add_http_500_retried", friend500)
	mutualOK := countNil(parallel(cfg.Users, 20, func(i int) error {
		fs, err := users[i].S.ListFriends(ctx)
		if err != nil {
			return err
		}
		n := 0
		for _, f := range fs {
			if f.GetState().GetValue() == 0 {
				n++
			}
		}
		if n != len(users[i].friends) || n < 3 {
			return fmt.Errorf("%s has %d mutual friends, want %d", users[i].Name, n, len(users[i].friends))
		}
		return nil
	}))
	r.Check("E1", "每個人的好友數都是 3 個以上且與預期一致", mutualOK == cfg.Users, "%d/%d", mutualOK, cfg.Users)

	// Follow friends and open the DM channels before the measured window.
	errs = parallel(cfg.Users, 20, func(i int) error {
		ids := []string{}
		for _, j := range users[i].friends {
			ids = append(ids, users[j].ID())
		}
		_, err := users[i].K.FollowStatus(ctx, ids...)
		return err
	})
	r.Check("E1", "每個人追蹤好友狀態", countNil(errs) == cfg.Users, "%v", firstErr(errs))
	var dmMu sync.Mutex
	errs = parallel(len(edgeList), 20, func(n int) error {
		a, b := users[edgeList[n][0]], users[edgeList[n][1]]
		ca, err := a.K.JoinDM(ctx, b.ID())
		if err != nil {
			return err
		}
		cb, err := b.K.JoinDM(ctx, a.ID())
		if err != nil {
			return err
		}
		if ca != cb {
			return fmt.Errorf("channel mismatch %s %s", ca, cb)
		}
		dmMu.Lock()
		a.dm[b.idx], b.dm[a.idx] = ca, cb
		dmMu.Unlock()
		return nil
	})
	r.Check("E1", "好友之間加入私訊頻道", countNil(errs) == len(edgeList), "%v", firstErr(errs))

	// E2: the measured window.
	if _, err := e.LobbyStats(ctx, true); err != nil {
		r.Check("E2", "重設大廳的事件延遲統計", false, "%v", err)
	}
	samples := startSampler(e, 2*time.Second)
	windowStart := time.Now()
	end := windowStart.Add(cfg.Duration)
	var wg sync.WaitGroup
	for i := range users {
		wg.Add(1)
		go func(lu *loadUser, seed uint64) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(seed, 2))
			seq := 0
			for {
				gap := time.Duration(float64(cfg.DMEvery) * (0.5 + rng.Float64()))
				if time.Now().Add(gap).After(end) {
					return
				}
				time.Sleep(gap)
				j := lu.friends[rng.IntN(len(lu.friends))]
				seq++
				id := fmt.Sprintf("dm-%d-%d", lu.idx, seq)
				dms.mu.Lock()
				dms.to[id] = j
				dms.mu.Unlock()
				err := lu.K.SendChannel(ctx, lu.dm[j], map[string]any{"id": id, "sent_at": time.Now().UnixMicro(), "text": "hello"})
				if err != nil {
					dms.mu.Lock()
					dms.sendErrs++
					delete(dms.to, id)
					dms.mu.Unlock()
				}
			}
		}(users[i], uint64(i)+cfg.Seed*1000)
	}
	results := make([]pairResult, cfg.Pairs)
	var pwg sync.WaitGroup
	for p := range cfg.Pairs {
		pwg.Add(1)
		go func(p int) {
			defer pwg.Done()
			time.Sleep(time.Duration(p) * cfg.Duration / 3 / time.Duration(cfg.Pairs)) // spread over the first third
			onChat := func(m roomc.Msg) {
				if m.Type == "chat" && m.SentAt > 0 {
					chatMu.Lock()
					chatLat = append(chatLat, float64(m.At.UnixMicro()-m.SentAt)/1000)
					chatMu.Unlock()
				}
			}
			results[p] = runPair(ctx, users[2*p].User, users[2*p+1].User, p, onChat)
		}(p)
	}
	pwg.Wait()
	wg.Wait()
	windowTook := time.Since(windowStart)
	time.Sleep(3 * time.Second) // let the last DMs and room events arrive
	resources := samples.stop()

	// E3: measurements.
	dms.mu.Lock()
	exactlyOnce, missing, dup, wrong := 0, 0, 0, 0
	for id, to := range dms.to {
		got := dms.got[id]
		switch {
		case len(got) == 1 && got[0] == to:
			exactlyOnce++
		case len(got) == 0:
			missing++
		default:
			for _, g := range got {
				if g != to {
					wrong++
				}
			}
			if len(got) > 1 {
				dup++
			}
		}
	}
	dmSent, dmErrs := len(dms.to), dms.sendErrs
	r.Metric("dm_latency_ms", summarize(dms.latMS))
	dms.mu.Unlock()
	r.Metric("dm_sent", dmSent)

	okPairs := 0
	var connected []float64
	for _, pr := range results {
		if pr.OK {
			okPairs++
			connected = append(connected, pr.ConnectedMS)
		}
	}
	r.Metric("accept_to_connected_ms", summarize(connected))
	chatMu.Lock()
	r.Metric("room_chat_latency_ms", summarize(chatLat))
	chatMu.Unlock()
	st, err := e.LobbyStats(ctx, false)
	if err == nil {
		r.Metric("room_event_latency_ms", st.EventLatency)
	}
	r.Metric("resources", resources)
	r.Metric("window_s", windowTook.Seconds())

	// E4: correctness.
	r.Check("E4", "每則私訊剛好送達一次", dmSent > 0 && exactlyOnce == dmSent && dmErrs == 0,
		"送出 %d、剛好一次 %d、遺失 %d、重複 %d、送錯 %d、送出失敗 %d", dmSent, exactlyOnce, missing, dup, wrong, dmErrs)
	var pairErr string
	for p, pr := range results {
		if !pr.OK && pairErr == "" {
			pairErr = fmt.Sprintf("pair %d: %s", p, pr.Err)
		}
	}
	r.Check("E4", "所有邀局都成功開房、聊天、關房", okPairs == cfg.Pairs, "%d/%d %s", okPairs, cfg.Pairs, pairErr)
	var final LobbyStats
	_, clean := Eventually(10*time.Second, func() bool {
		final, err = e.LobbyStats(ctx, false)
		return err == nil && final.RoomsTotal == 0 && final.PendingInvites == 0 && final.BusyUsers == 0
	})
	r.Check("E4", "大廳沒有殘留的房間、邀請、對局中玩家", clean, "rooms=%d invites=%d busy=%d", final.RoomsTotal, final.PendingInvites, final.BusyUsers)
	var rs RoomStats
	_, clean = Eventually(10*time.Second, func() bool {
		rs, err = e.RoomServiceStats(ctx)
		return err == nil && rs.Rooms == 0 && rs.Connections == 0 && rs.EventsPending == 0
	})
	r.Check("E4", "房間服務沒有殘留的房間、連線、待送事件", clean, "rooms=%d conns=%d pending=%d goroutines=%d slow_kicks=%d", rs.Rooms, rs.Connections, rs.EventsPending, rs.Goroutines, rs.SlowKicks)
	r.Metric("room_service_final", rs)
	stale := 0
	for p := range cfg.Pairs {
		for _, u := range []*loadUser{users[2*p], users[2*p+1]} {
			for _, j := range u.friends {
				f := users[j]
				f.mu.Lock()
				if f.lastStatus[u.ID()] != "idle" {
					stale++
				}
				f.mu.Unlock()
			}
		}
	}
	r.Check("E4", "好友看到的最後狀態都不是「對局中」", stale == 0, "%d 個好友看到的狀態不是 idle", stale)
	for _, u := range users {
		u.K.Close()
	}
	return nil
}

func (lu *loadUser) onEvent(env *rtapi.Envelope, at time.Time, dms *dmLog) {
	if m := env.GetChannelMessage(); m != nil && m.SenderId != lu.ID() {
		var c struct {
			ID     string `json:"id"`
			SentAt int64  `json:"sent_at"`
		}
		if json.Unmarshal([]byte(m.Content), &c) == nil && c.ID != "" {
			dms.mu.Lock()
			dms.got[c.ID] = append(dms.got[c.ID], lu.idx)
			dms.latMS = append(dms.latMS, float64(at.UnixMicro()-c.SentAt)/1000)
			dms.mu.Unlock()
		}
	}
	if ev := env.GetStatusPresenceEvent(); ev != nil {
		lu.mu.Lock()
		for _, p := range ev.GetJoins() {
			lu.lastStatus[p.UserId] = p.GetStatus().GetValue()
		}
		lu.mu.Unlock()
	}
}

// runPair: a invites b, b accepts, both connect, chat 5 lines each, leave.
func runPair(ctx context.Context, a, b *User, p int, onChat func(roomc.Msg)) pairResult {
	fail := func(f string, args ...any) pairResult { return pairResult{Err: fmt.Sprintf(f, args...)} }
	ma, mb := a.K.Mark(), b.K.Mark()
	id, err := a.Invite(ctx, b.Name)
	if err != nil {
		return fail("invite: %v", err)
	}
	if _, err := b.WaitNote(mb, codeInviteReceived, 10*time.Second, nil); err != nil {
		return fail("%v", err)
	}
	acceptAt := time.Now()
	if _, err := b.Reply(ctx, id, true); err != nil {
		return fail("accept: %v", err)
	}
	var ta, tb TicketOut
	if _, err := a.WaitNote(ma, codeGameReady, 10*time.Second, &ta); err != nil {
		return fail("%v", err)
	}
	if _, err := b.WaitNote(mb, codeGameReady, 10*time.Second, &tb); err != nil {
		return fail("%v", err)
	}
	type dialed struct {
		c   *roomc.Conn
		err error
	}
	ch := make(chan dialed, 2)
	for _, t := range []TicketOut{ta, tb} {
		go func(t TicketOut) {
			c, first, err := roomc.Dial(ctx, t.WSURL, t.Ticket, onChat)
			if err == nil && first.Type != "welcome" {
				err = fmt.Errorf("refused: %s", first.Code)
			}
			ch <- dialed{c, err}
		}(t)
	}
	var conns []*roomc.Conn
	for range 2 {
		d := <-ch
		if d.err != nil {
			return fail("join room: %v", d.err)
		}
		conns = append(conns, d.c)
	}
	res := pairResult{ConnectedMS: float64(time.Since(acceptAt).Microseconds()) / 1000}
	for i := range 5 {
		for k, c := range conns {
			c.Chat(ctx, fmt.Sprintf("p%d-%d-%d", p, k, i), "line "+strconv.Itoa(i))
		}
		time.Sleep(300 * time.Millisecond)
	}
	for k, c := range conns {
		other := 1 - k
		if _, err := c.Wait(5*time.Second, roomc.ChatID(fmt.Sprintf("p%d-%d-4", p, other))); err != nil {
			return fail("chat not received: %v", err)
		}
	}
	for _, c := range conns {
		c.Leave(ctx)
	}
	for _, c := range conns {
		if _, err := c.Wait(5*time.Second, roomc.Type("left")); err != nil {
			return fail("leave: %v", err)
		}
	}
	res.OK = true
	return res
}

// --- resource sampling ---

type sampler struct {
	stopCh chan struct{}
	done   chan map[string]any
}

func startSampler(e *Env, every time.Duration) *sampler {
	s := &sampler{stopCh: make(chan struct{}), done: make(chan map[string]any, 1)}
	go func() {
		type peak struct{ CPU, MemMB float64 }
		peaks := map[string]*peak{}
		maxGor, maxHeap := 0, 0.0
		var cpuSamples = map[string][]float64{}
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			for name, cs := range dockerStats() {
				p := peaks[name]
				if p == nil {
					p = &peak{}
					peaks[name] = p
				}
				p.CPU, p.MemMB = max(p.CPU, cs[0]), max(p.MemMB, cs[1])
				cpuSamples[name] = append(cpuSamples[name], cs[0])
			}
			if rs, err := e.RoomServiceStats(context.Background()); err == nil {
				maxGor, maxHeap = max(maxGor, rs.Goroutines), max(maxHeap, rs.HeapAllocMB)
			}
			select {
			case <-s.stopCh:
				out := map[string]any{"room_service_goroutines_max": maxGor, "room_service_heap_mb_max": maxHeap}
				for name, p := range peaks {
					out[name] = map[string]any{"cpu_pct_max": p.CPU, "cpu_pct_avg": avg(cpuSamples[name]), "mem_mb_max": p.MemMB, "samples": len(cpuSamples[name])}
				}
				s.done <- out
				return
			case <-tick.C:
			}
		}
	}()
	return s
}

func (s *sampler) stop() map[string]any {
	close(s.stopCh)
	return <-s.done
}

// dockerStats returns container short name -> [cpu %, memory MB].
func dockerStats() map[string][2]float64 {
	out, err := exec.Command("docker", "stats", "--no-stream", "--format", "{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}",
		"poc-nakama-lobby-go-room-nakama-1", "poc-nakama-lobby-go-room-room-1", "poc-nakama-lobby-go-room-db-1").Output()
	res := map[string][2]float64{}
	if err != nil {
		return res
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "|")
		if len(f) != 3 {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(f[0], "poc-nakama-lobby-go-room-"), "-1")
		cpu, _ := strconv.ParseFloat(strings.TrimSuffix(f[1], "%"), 64)
		res[name] = [2]float64{cpu, parseMem(strings.TrimSpace(strings.Split(f[2], "/")[0]))}
	}
	return res
}

func parseMem(s string) float64 {
	units := []struct {
		suffix string
		mb     float64
	}{{"GiB", 1024}, {"MiB", 1}, {"KiB", 1.0 / 1024}, {"GB", 1000}, {"MB", 1}, {"kB", 0.001}, {"B", 1.0 / (1 << 20)}}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			v, _ := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64)
			return v * u.mb
		}
	}
	return 0
}

// --- helpers ---

func parallel(n, limit int, fn func(int) error) []error {
	errs := make([]error, n)
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			errs[i] = fn(i)
		}(i)
	}
	wg.Wait()
	return errs
}

func countNil(errs []error) int {
	n := 0
	for _, e := range errs {
		if e == nil {
			n++
		}
	}
	return n
}

func firstErr(errs []error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func avg(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func summarize(xs []float64) map[string]float64 {
	if len(xs) == 0 {
		return map[string]float64{"n": 0}
	}
	s := append([]float64{}, xs...)
	sort.Float64s(s)
	at := func(p float64) float64 { return s[int(p*float64(len(s)-1))] }
	return map[string]float64{"n": float64(len(s)), "p50": at(0.5), "p95": at(0.95), "p99": at(0.99), "max": s[len(s)-1]}
}
