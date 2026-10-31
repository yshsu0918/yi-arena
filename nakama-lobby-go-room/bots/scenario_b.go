package main

import (
	"context"
	"time"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/bots/nk"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/bots/roomc"
)

// Scenario B: invite -> room -> chat -> refused intruders -> room closes.
func scenarioB(ctx context.Context, e *Env, r *Report) error {
	r.Title = "邀局到房間結束"
	users := map[string]*User{}
	for _, name := range []string{"Alice", "Bob", "Carol", "Dave", "Erin"} {
		u, err := e.Login(ctx, name)
		if !r.NoErr("B0", name+" 登入", err) {
			return errAbort{"B0"}
		}
		users[name] = u
	}
	alice, bob, carol := users["Alice"], users["Bob"], users["Carol"]
	// Carol watches Alice and Bob's status and the room list from the lobby.
	_, err := carol.K.FollowStatus(ctx, alice.ID(), bob.ID())
	r.NoErr("B0", "Carol 追蹤 Alice、Bob 的狀態", err)
	rooms, err := carol.ListRooms(ctx)
	r.Check("B0", "一開始房間列表是空的", err == nil && len(rooms) == 0, "%d rooms, err=%v", len(rooms), err)

	// B1: Alice invites Bob.
	ma, mb, mc := alice.K.Mark(), bob.K.Mark(), carol.K.Mark()
	inviteID, err := alice.Invite(ctx, "Bob")
	r.NoErr("B1", "Alice 邀請 Bob", err)
	var inv struct {
		InviteID string `json:"invite_id"`
		From     struct {
			Name string `json:"name"`
		} `json:"from"`
	}
	_, err = bob.WaitNote(mb, codeInviteReceived, 3*time.Second, &inv)
	r.Check("B1", "Bob 收到邀請通知", err == nil && inv.InviteID == inviteID && inv.From.Name == "Alice", "%+v err=%v", inv, err)

	// B2: Bob accepts; both get the room address and a ticket.
	acceptAt := time.Now()
	_, err = bob.Reply(ctx, inviteID, true)
	r.NoErr("B2", "Bob 接受邀請", err)
	var ga, gb TicketOut
	_, errA := alice.WaitNote(ma, codeGameReady, 3*time.Second, &ga)
	_, errB := bob.WaitNote(mb, codeGameReady, 3*time.Second, &gb)
	ok := errA == nil && errB == nil && ga.RoomID != "" && ga.RoomID == gb.RoomID && ga.WSURL != "" && ga.Ticket != "" && gb.Ticket != "" && ga.Ticket != gb.Ticket
	r.Check("B2", "雙方都收到「房間位址 + 入場券」通知", ok, "room=%s ws=%s", ga.RoomID, ga.WSURL)
	if err := must(ok, "B2"); err != nil {
		return err
	}
	roomID := ga.RoomID
	// A spare ticket for Bob, kept unused until it expires (for B5).
	spare, err := bob.Reissue(ctx)
	r.NoErr("B2", "Bob 另外換一張入場券（留給 B5 測過期）", err)

	// B3: both connect; the lobby gets room.started.
	ca, first, err := roomc.Dial(ctx, ga.WSURL, ga.Ticket, nil)
	r.Check("B3", "Alice 用入場券連上房間", err == nil && first.Type == "welcome", "%+v %v", first, err)
	cb, first, err := roomc.Dial(ctx, gb.WSURL, gb.Ticket, nil)
	r.Check("B3", "Bob 用入場券連上房間", err == nil && first.Type == "welcome" && first.Status == "playing", "%+v %v", first, err)
	if err := must(ca != nil && cb != nil, "B3"); err != nil {
		return err
	}
	r.Metric("accept_to_both_connected_ms", time.Since(acceptAt).Milliseconds())
	_, err = carol.K.Wait(mc, 3*time.Second, nk.RoomList(roomID, "upsert", "playing"))
	r.Check("B3", "Carol 在大廳即時收到房間「進行中」的推送", err == nil, "%v", err)
	_, errA = carol.K.Wait(mc, 3*time.Second, nk.StatusJoin(alice.ID(), "playing"))
	_, errB = carol.K.Wait(mc, 3*time.Second, nk.StatusJoin(bob.ID(), "playing"))
	r.Check("B3", "Alice、Bob 的狀態變成「對局中」（Carol 看到）", errA == nil && errB == nil, "alice=%v bob=%v", errA, errB)
	rooms, _ = carol.ListRooms(ctx)
	r.Check("B3", "list_rooms 有這個房間，狀態為 playing", len(rooms) == 1 && rooms[0].RoomID == roomID && rooms[0].Status == "playing", "%+v", rooms)

	// B4: chat both ways.
	ca.Chat(ctx, "b4-1", "hello Bob")
	cb.Chat(ctx, "b4-2", "hello Alice")
	_, e1 := cb.Wait(3*time.Second, roomc.ChatID("b4-1"))
	_, e2 := ca.Wait(3*time.Second, roomc.ChatID("b4-2"))
	r.Check("B4", "Bob 收到 Alice 的房內聊天", e1 == nil, "%v", e1)
	r.Check("B4", "Alice 收到 Bob 的房內聊天", e2 == nil, "%v", e2)

	// B4 (extra): Bob's room connection drops; he gets a new ticket from the
	// lobby and comes back to his seat.
	mc = carol.K.Mark()
	cb.Close()
	_, err = ca.Wait(3*time.Second, roomc.Type("member_disconnected"))
	r.Check("B4", "（加測）Bob 的房間連線斷了，Alice 收到 member_disconnected", err == nil, "%v", err)
	_, err = carol.K.Wait(mc, 3*time.Second, nk.RoomConnected(roomID, 1))
	r.Check("B4", "（加測）大廳收到 room.player_disconnected，列表顯示 1 人連線", err == nil, "%v", err)
	again, err := bob.Reissue(ctx)
	if r.NoErr("B4", "（加測）Bob 回大廳用 reissue_ticket 換新的入場券", err) {
		cb, first, err = roomc.Dial(ctx, again.WSURL, again.Ticket, nil)
		r.Check("B4", "（加測）Bob 用新入場券回到房間", err == nil && first.Type == "welcome", "%+v %v", first, err)
		_, err = carol.K.Wait(mc, 3*time.Second, nk.RoomConnected(roomID, 2))
		r.Check("B4", "（加測）大廳收到 room.player_reconnected，列表顯示 2 人連線", err == nil, "%v", err)
	}
	if err := must(cb != nil, "B4"); err != nil {
		return err
	}
	ca.Chat(ctx, "b4-3", "welcome back")
	_, err = cb.Wait(3*time.Second, roomc.ChatID("b4-3"))
	r.Check("B4", "（加測）重連後聊天正常", err == nil, "%v", err)

	// B5: Carol tries to get in.
	refused := func(name, ticket, want string, url string) {
		c, first, err := roomc.Dial(ctx, url, ticket, nil)
		if c != nil {
			c.Close()
		}
		r.Check("B5", "Carol "+name+"：被拒絕並斷線", err == nil && c == nil && first.Code == want, "回覆 %s/%s err=%v", first.Type, first.Code, err)
	}
	refused("沒有入場券", "", "no_ticket", ga.WSURL)
	refused("拿 Alice 用過的入場券", ga.Ticket, "ticket_used", ga.WSURL)
	// Another room for Dave and Erin, to borrow one of its tickets.
	g2, err := e.StartGame(ctx, users["Dave"], users["Erin"])
	if r.NoErr("B5", "Dave、Erin 開另一個房間", err) {
		other, err := users["Dave"].Reissue(ctx)
		if r.NoErr("B5", "取得 Dave 那個房間的入場券", err) {
			refused("拿別的房間的入場券", other.Ticket, "wrong_room", ga.WSURL)
		}
	}
	if wait := time.Until(time.UnixMilli(spare.ExpiresAt)) + 1500*time.Millisecond; wait > 0 {
		r.Check("B5", "等 Bob 的備用入場券過期", true, "等待 %.0f 秒", wait.Seconds())
		time.Sleep(wait)
	}
	refused("拿過期的入場券", spare.Ticket, "ticket_expired", ga.WSURL)
	// The room was not disturbed.
	ca.Chat(ctx, "b5-1", "still here")
	_, err = cb.Wait(3*time.Second, roomc.ChatID("b5-1"))
	r.Check("B5", "被拒絕的連線不影響房內", err == nil, "%v", err)

	// B6: both leave; the lobby gets room.closed.
	mc = carol.K.Mark()
	t0 := time.Now()
	ca.Leave(ctx)
	cb.Leave(ctx)
	_, err = carol.K.Wait(mc, 5*time.Second, nk.RoomList(roomID, "remove", ""))
	r.Check("B6", "房間從列表移除（Carol 即時收到）", err == nil, "%s %v", ms(time.Since(t0)), err)
	_, errA = carol.K.Wait(mc, 3*time.Second, nk.StatusJoin(alice.ID(), "idle"))
	_, errB = carol.K.Wait(mc, 3*time.Second, nk.StatusJoin(bob.ID(), "idle"))
	r.Check("B6", "Alice、Bob 狀態回到「空閒」（Carol 看到）", errA == nil && errB == nil, "alice=%v bob=%v", errA, errB)
	if g2 != nil {
		g2.Leave(ctx)
	}
	_, done := Eventually(5*time.Second, func() bool {
		rooms, err := carol.ListRooms(ctx)
		return err == nil && len(rooms) == 0
	})
	r.Check("B6", "最後房間列表是空的", done, "")
	st, err := e.LobbyStats(ctx, false)
	r.Check("B6", "大廳沒有殘留的房間、邀請、對局中玩家", err == nil && st.RoomsTotal == 0 && st.PendingInvites == 0 && st.BusyUsers == 0,
		"rooms=%d invites=%d busy=%d", st.RoomsTotal, st.PendingInvites, st.BusyUsers)
	_, done = Eventually(5*time.Second, func() bool {
		rs, err := e.RoomServiceStats(ctx)
		return err == nil && rs.Rooms == 0
	})
	r.Check("B6", "房間服務裡也沒有房間", done, "")
	return nil
}
