package main

import (
	"context"
	"encoding/json"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/bots/nk"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/bots/roomc"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/roompb"
)

// Scenario D: restarts, outages and reconciliation.
func scenarioD(ctx context.Context, e *Env, r *Report) error {
	r.Title = "故障與對帳"
	users := map[string]*User{}
	for _, name := range []string{"Alice", "Bob", "Carol"} {
		u, err := e.Login(ctx, name)
		if !r.NoErr("D0", name+" 登入", err) {
			return errAbort{"D0"}
		}
		users[name] = u
	}
	alice, bob, carol := users["Alice"], users["Bob"], users["Carol"]
	follow := func() error {
		_, err := carol.K.FollowStatus(ctx, alice.ID(), bob.ID())
		return err
	}
	r.NoErr("D0", "Carol 追蹤 Alice、Bob 的狀態", follow())
	lobbyEmpty := func() bool {
		st, err := e.LobbyStats(ctx, false)
		return err == nil && st.RoomsTotal == 0 && st.BusyUsers == 0
	}

	// D1: the room service restarts while a room is in progress.
	g, err := e.StartGame(ctx, alice, bob)
	if err := must(r.NoErr("D1", "Alice、Bob 進入對局", err), "D1"); err != nil {
		return err
	}
	before, _ := e.RoomServiceStats(ctx)
	mc := carol.K.Mark()
	t0 := time.Now()
	r.NoErr("D1", "重啟房間服務（docker compose restart room）", e.ComposeRun("restart", "room"))
	_, err = carol.K.Wait(mc, 15*time.Second, nk.RoomList(g.RoomID, "remove", ""))
	r.Check("D1", "大廳發現房間消失，從列表移除（Carol 收到推送）", err == nil, "重啟開始後 %s", ms(time.Since(t0)))
	_, errA := carol.K.Wait(mc, 3*time.Second, nk.StatusJoin(alice.ID(), "idle"))
	_, errB := carol.K.Wait(mc, 3*time.Second, nk.StatusJoin(bob.ID(), "idle"))
	r.Check("D1", "Alice、Bob 狀態回到「空閒」", errA == nil && errB == nil, "alice=%v bob=%v", errA, errB)
	r.Check("D1", "沒有幽靈房間", lobbyEmpty(), "")
	after, _ := e.RoomServiceStats(ctx)
	r.Check("D1", "房間服務確實是新的程序", before.InstanceID != "" && after.InstanceID != before.InstanceID, "%s -> %s", before.InstanceID, after.InstanceID)
	select {
	case <-g.CA.Done():
		r.Check("D1", "玩家的房間連線被斷開", true, "")
	case <-time.After(3 * time.Second):
		r.Check("D1", "玩家的房間連線被斷開", false, "")
	}

	// D2: Nakama restarts while a room is in progress.
	g, err = e.StartGame(ctx, alice, bob)
	if err := must(r.NoErr("D2", "Alice、Bob 再開一局", err), "D2"); err != nil {
		return err
	}
	if _, err := g.CB.Wait(3*time.Second, roomc.Type("welcome")); err == nil {
		time.Sleep(500 * time.Millisecond) // let room.started reach the lobby
	}
	t0 = time.Now()
	r.NoErr("D2", "重啟 Nakama（docker compose restart nakama）", e.ComposeRun("restart", "nakama"))
	// Rooms keep working while the lobby is down.
	g.CA.Chat(ctx, "d2-1", "lobby is down?")
	_, err = g.CB.Wait(3*time.Second, roomc.ChatID("d2-1"))
	r.Check("D2", "Nakama 重啟期間房內聊天照常", err == nil, "%v", err)
	select {
	case <-g.CA.Done():
		r.Check("D2", "房間連線沒有中斷", false, "")
	default:
		r.Check("D2", "房間連線沒有中斷", true, "")
	}
	r.NoErr("D2", "Nakama 重新啟動完成", e.WaitHealthy(ctx, 60*time.Second))
	r.Metric("nakama_restart_s", time.Since(t0).Seconds())
	for _, u := range []*User{alice, bob, carol} {
		r.NoErr("D2", u.Name+" 重新連上 Nakama（session token 沿用）", u.Connect(ctx, nil))
	}
	r.NoErr("D2", "Carol 重新追蹤狀態", follow())
	var rooms []RoomView
	took, ok := Eventually(10*time.Second, func() bool {
		rooms, err = carol.ListRooms(ctx)
		return err == nil && len(rooms) == 1 && rooms[0].RoomID == g.RoomID && rooms[0].Status == "playing"
	})
	r.Check("D2", "大廳透過 ListRooms 重建房間列表", ok, "Nakama 健康後 %s：%+v", ms(took), rooms)
	st, _ := e.LobbyStats(ctx, false)
	r.Check("D2", "重建來源是對帳（reconcile_added ≥ 1）", st.Stats.ReconcileAdded >= 1, "reconcile_added=%d", st.Stats.ReconcileAdded)
	presences, err := carol.K.FollowStatus(ctx, alice.ID(), bob.ID())
	playing := 0
	for _, p := range presences {
		if p.GetStatus().GetValue() == "playing" {
			playing++
		}
	}
	r.Check("D2", "Alice、Bob 的狀態恢復為「對局中」", err == nil && playing == 2, "%d/2", playing)

	// D3: room events cannot reach Nakama for a while.
	before, _ = e.RoomServiceStats(ctx)
	st0, _ := e.LobbyStats(ctx, false)
	r.NoErr("D3", "暫停 Nakama（docker compose pause nakama）", e.ComposeRun("pause", "nakama"))
	g.Leave(ctx)
	_, gone := Eventually(5*time.Second, func() bool {
		rs, err := e.RoomServiceStats(ctx)
		return err == nil && rs.Rooms == 0 && rs.EventsPending > 0
	})
	r.Check("D3", "雙方離開，房間服務關房，room.closed 送不出去而排隊", gone, "")
	time.Sleep(4 * time.Second)
	mid, _ := e.RoomServiceStats(ctx)
	r.Check("D3", "送出失敗會重試", mid.EventsRetries > before.EventsRetries, "retries %d -> %d", before.EventsRetries, mid.EventsRetries)
	t0 = time.Now()
	r.NoErr("D3", "恢復 Nakama（docker compose unpause nakama）", e.ComposeRun("unpause", "nakama"))
	took, ok = Eventually(10*time.Second, func() bool {
		rs, err := e.RoomServiceStats(ctx)
		return err == nil && rs.EventsPending == 0
	})
	r.Check("D3", "恢復後事件重送成功", ok, "恢復後 %s", ms(took))
	_, ok = Eventually(5*time.Second, lobbyEmpty)
	r.Check("D3", "大廳移除房間，雙方回到空閒", ok, "恢復後 %s", ms(time.Since(t0)))
	st1, _ := e.LobbyStats(ctx, false)
	applied := st1.Stats.EventsApplied - st0.Stats.EventsApplied
	r.Check("D3", "room.closed 只被處理一次", applied == 1, "events_applied +%d, ignored +%d", applied, st1.Stats.EventsIgnored-st0.Stats.EventsIgnored)

	// Replay an old event as if it had been sent twice: it must be ignored.
	dup, _ := protojson.Marshal(&roompb.RoomEvent{Type: "room.started", RoomId: g.RoomID, Seq: 1,
		Room: &roompb.RoomInfo{RoomId: g.RoomID, Status: roompb.RoomStatus_ROOM_STATUS_PLAYING, Seq: 1,
			Members: []*roompb.Member{{UserId: alice.ID(), Name: "Alice"}, {UserId: bob.ID(), Name: "Bob"}}}})
	var res struct {
		Applied bool `json:"applied"`
	}
	err = e.NK.ServerRPC(ctx, e.HTTPKey, "room_event", json.RawMessage(dup), &res)
	r.Check("D3", "重複送一個舊事件（seq=1）被忽略", err == nil && !res.Applied, "applied=%v err=%v", res.Applied, err)
	r.Check("D3", "房間沒有因為舊事件重新出現", lobbyEmpty(), "")
	err = alice.S.RPC(ctx, "room_event", json.RawMessage(dup), nil)
	r.Check("D3", "（加測）一般使用者不能呼叫 room_event", nk.ErrMessage(err) == "server_only", "錯誤 %q", nk.ErrMessage(err))
	return nil
}
