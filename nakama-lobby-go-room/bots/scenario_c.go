package main

import (
	"context"
	"time"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/bots/nk"
	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/bots/roomc"
)

// Scenario C: invite exceptions.
func scenarioC(ctx context.Context, e *Env, r *Report) error {
	r.Title = "邀局的例外"
	users := map[string]*User{}
	for _, name := range []string{"Alice", "Bob", "Carol", "Dave", "Erin"} {
		u, err := e.Login(ctx, name)
		if !r.NoErr("C0", name+" 登入", err) {
			return errAbort{"C0"}
		}
		users[name] = u
	}
	alice, bob, carol := users["Alice"], users["Bob"], users["Carol"]

	// inviteAndDecline checks that a and b can still be invited (and cleans up).
	inviteAndDecline := func(step, what string) {
		mb := bob.K.Mark()
		id, err := alice.Invite(ctx, "Bob")
		if err == nil {
			_, err = bob.WaitNote(mb, codeInviteReceived, 3*time.Second, nil)
		}
		if err == nil {
			_, err = bob.Reply(ctx, id, false)
		}
		r.NoErr(step, what, err)
	}

	// C1: Bob declines.
	ma, mb := alice.K.Mark(), bob.K.Mark()
	id, err := alice.Invite(ctx, "Bob")
	r.NoErr("C1", "Alice 邀請 Bob", err)
	bob.WaitNote(mb, codeInviteReceived, 3*time.Second, nil)
	out, err := bob.Reply(ctx, id, false)
	r.Check("C1", "Bob 拒絕", err == nil && !out.Accepted, "%v", err)
	_, err = alice.WaitNote(ma, codeInviteDeclined, 3*time.Second, nil)
	r.NoErr("C1", "Alice 收到拒絕通知", err)
	inviteAndDecline("C1", "雙方回到可邀請狀態（可以再邀一次）")

	// C3: Alice has a pending invite; Carol's invite to Alice is refused.
	mb = bob.K.Mark()
	id, err = alice.Invite(ctx, "Bob")
	r.NoErr("C3", "Alice 邀請 Bob（待回覆）", err)
	_, err = carol.Invite(ctx, "Alice")
	r.Check("C3", "Carol 邀請 Alice 被拒絕", nk.ErrMessage(err) == "pending_invite", "錯誤 %q", nk.ErrMessage(err))
	_, err = alice.Invite(ctx, "Carol")
	r.Check("C3", "（加測）Alice 有待回覆邀請時也不能再邀別人", nk.ErrMessage(err) == "pending_invite", "錯誤 %q", nk.ErrMessage(err))
	bob.WaitNote(mb, codeInviteReceived, 3*time.Second, nil)
	bob.Reply(ctx, id, false)

	// C4: inviting someone who is in a game is refused.
	g, err := e.StartGame(ctx, users["Dave"], users["Erin"])
	if r.NoErr("C4", "Dave、Erin 進入對局", err) {
		_, err = alice.Invite(ctx, "Dave")
		r.Check("C4", "Alice 邀請對局中的 Dave 被拒絕", nk.ErrMessage(err) == "busy", "錯誤 %q", nk.ErrMessage(err))
		_, err = users["Dave"].Invite(ctx, "Alice")
		r.Check("C4", "（加測）對局中的 Dave 不能邀請別人", nk.ErrMessage(err) == "busy", "錯誤 %q", nk.ErrMessage(err))
		g.Leave(ctx)
	}

	// C5: Bob is offline.
	bob.K.Close()
	time.Sleep(300 * time.Millisecond)
	_, err = alice.Invite(ctx, "Bob")
	r.Check("C5", "Bob 離線時邀請他被拒絕（只能邀請線上玩家）", nk.ErrMessage(err) == "offline", "錯誤 %q", nk.ErrMessage(err))
	err = alice.S.RPC(ctx, "send_note", map[string]string{"to": "Bob", "text": "call me when you are back"}, nil)
	r.NoErr("C5", "Bob 離線時 Alice 寄一則持久化通知給他", err)
	err = bob.Connect(ctx, nil)
	r.NoErr("C5", "Bob 重新登入", err)
	notes, err := bob.S.ListNotifications(ctx)
	found := false
	for _, n := range notes {
		found = found || (n.Code == codeNote && n.SenderId == alice.ID() && n.Persistent)
	}
	r.Check("C5", "Bob 登入後在通知列表看到這則通知", err == nil && found, "%d notifications", len(notes))

	// C2: nobody answers for 60 seconds.
	ma, mb = alice.K.Mark(), bob.K.Mark()
	t0 := time.Now()
	_, err = alice.Invite(ctx, "Bob")
	r.NoErr("C2", "Alice 邀請 Bob，Bob 不回覆", err)
	_, errA := alice.WaitNote(ma, codeInviteExpired, 75*time.Second, nil)
	_, errB := bob.WaitNote(mb, codeInviteExpired, 5*time.Second, nil)
	took := time.Since(t0)
	r.Check("C2", "60 秒後邀請逾時，雙方收到通知", errA == nil && errB == nil && took >= 59*time.Second, "%.1f 秒", took.Seconds())
	inviteAndDecline("C2", "逾時後雙方可以再被邀請")

	// C6: accepted, but one side never connects to the room within 30 s.
	mc := carol.K.Mark()
	ma, mb = alice.K.Mark(), bob.K.Mark()
	id, err = alice.Invite(ctx, "Bob")
	if err == nil {
		bob.WaitNote(mb, codeInviteReceived, 3*time.Second, nil)
		_, err = bob.Reply(ctx, id, true)
	}
	r.NoErr("C6", "Alice 邀請、Bob 接受", err)
	var ga TicketOut
	_, err = alice.WaitNote(ma, codeGameReady, 3*time.Second, &ga)
	r.NoErr("C6", "Alice 收到入場券", err)
	ca, first, err := roomc.Dial(ctx, ga.WSURL, ga.Ticket, nil)
	r.Check("C6", "只有 Alice 連進房間，Bob 不連", err == nil && first.Type == "welcome" && first.Status == "waiting", "%+v %v", first, err)
	t0 = time.Now()
	_, errA = alice.WaitNote(ma, codeRoomCancelled, 40*time.Second, nil)
	_, errB = bob.WaitNote(mb, codeRoomCancelled, 5*time.Second, nil)
	took = time.Since(t0)
	r.Check("C6", "30 秒後房間取消，雙方收到 room_cancelled 通知", errA == nil && errB == nil && took >= 28*time.Second, "%.1f 秒", took.Seconds())
	if ca != nil {
		msg, err := ca.Wait(3*time.Second, roomc.Type("room_closed"))
		r.Check("C6", "Alice 的房間連線收到 room_closed（arrival_timeout）", err == nil && msg.Reason == "arrival_timeout", "%+v", msg)
	}
	_, err = carol.K.Wait(mc, 3*time.Second, nk.RoomList(ga.RoomID, "remove", ""))
	r.Check("C6", "房間從列表移除（Carol 即時收到）", err == nil, "%v", err)
	st, err := e.LobbyStats(ctx, false)
	r.Check("C6", "大廳沒有殘留的房間、邀請、對局中玩家", err == nil && st.RoomsTotal == 0 && st.PendingInvites == 0 && st.BusyUsers == 0,
		"rooms=%d invites=%d busy=%d", st.RoomsTotal, st.PendingInvites, st.BusyUsers)
	inviteAndDecline("C6", "雙方回到空閒，可以再被邀請")
	return nil
}
