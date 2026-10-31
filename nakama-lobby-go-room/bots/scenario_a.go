package main

import (
	"context"
	"time"

	"github.com/heroiclabs/nakama-common/rtapi"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/bots/nk"
)

// Scenario A: friends, status and direct messages (Nakama built-ins).
func scenarioA(ctx context.Context, e *Env, r *Report) error {
	r.Title = "好友、私訊"

	// A1: three users log in.
	var users []*User
	for _, name := range []string{"Alice", "Bob", "Carol"} {
		u, err := e.Login(ctx, name)
		if !r.NoErr("A1", name+" 登入並連上即時連線", err) {
			return errAbort{"A1"}
		}
		users = append(users, u)
	}
	alice, bob, carol := users[0], users[1], users[2]

	// A2: Alice adds Bob, Bob accepts. Carol is nobody's friend.
	mb := bob.K.Mark()
	err := alice.S.AddFriends(ctx, "Bob")
	r.NoErr("A2", "Alice 送出好友邀請給 Bob", err)
	_, err = bob.WaitNote(mb, codeFriendRequest, 3*time.Second, nil)
	r.NoErr("A2", "Bob 收到好友邀請通知（Nakama 內建，code -2）", err)
	state := func(u *User, other *User) int32 {
		fs, _ := u.S.ListFriends(ctx)
		for _, f := range fs {
			if f.User.Id == other.ID() {
				return f.GetState().GetValue()
			}
		}
		return -1
	}
	r.Check("A2", "Bob 看到 Alice 的好友邀請（state=2）", state(bob, alice) == nk.FriendReceived, "state=%d", state(bob, alice))
	r.NoErr("A2", "Bob 接受（反向加好友）", bob.S.AddFriends(ctx, "Alice"))
	r.Check("A2", "Alice、Bob 互為好友（state=0）", state(alice, bob) == nk.FriendMutual && state(bob, alice) == nk.FriendMutual,
		"alice→bob=%d bob→alice=%d", state(alice, bob), state(bob, alice))
	cf, _ := carol.S.ListFriends(ctx)
	r.Check("A2", "Carol 沒有任何好友", len(cf) == 0, "%d friends", len(cf))

	// A3: Alice follows her friend Bob and sees him online.
	presences, err := alice.K.FollowStatus(ctx, bob.ID())
	online := false
	for _, p := range presences {
		online = online || p.UserId == bob.ID()
	}
	r.Check("A3", "Alice 追蹤 Bob，看到 Bob 在線", err == nil && online, "presences=%d err=%v", len(presences), err)
	idle := false
	for _, p := range presences {
		idle = idle || (p.UserId == bob.ID() && p.GetStatus().GetValue() == "idle")
	}
	if !idle { // the session-start hook may not have run yet
		_, werr := alice.K.Wait(0, 3*time.Second, nk.StatusJoin(bob.ID(), "idle"))
		idle = werr == nil
	}
	r.Check("A3", "Bob 的狀態是「空閒」（idle，由大廳在連線時設定）", idle, "")

	// A4: Alice messages Bob; Bob gets it, Carol does not.
	mb, mc := bob.K.Mark(), carol.K.Mark()
	chA, err := alice.K.JoinDM(ctx, bob.ID())
	r.NoErr("A4", "Alice 加入與 Bob 的私訊頻道", err)
	_, err = bob.WaitNote(mb, codeDMRequest, 3*time.Second, nil)
	r.NoErr("A4", "Bob 收到私訊邀請通知（Nakama 內建，code -1）", err)
	chB, err := bob.K.JoinDM(ctx, alice.ID())
	r.Check("A4", "Bob 加入同一個私訊頻道", err == nil && chA == chB, "alice=%s bob=%s", chA, chB)
	mb = bob.K.Mark()
	sent := time.Now()
	err = alice.K.SendChannel(ctx, chA, map[string]any{"text": "hi Bob", "id": "dm-1"})
	r.NoErr("A4", "Alice 送出私訊", err)
	isDM := func(e *rtapi.Envelope) bool {
		m := e.GetChannelMessage()
		return m != nil && m.ChannelId == chA && m.SenderId == alice.ID()
	}
	_, err = bob.K.Wait(mb, 3*time.Second, isDM)
	r.Check("A4", "Bob 收到私訊", err == nil, "延遲 %s", ms(time.Since(sent)))
	time.Sleep(500 * time.Millisecond)
	n := carol.K.Count(mc, func(e *rtapi.Envelope) bool { return e.GetChannelMessage() != nil })
	r.Check("A4", "Carol 沒收到任何私訊", n == 0, "%d messages", n)

	// A5: Bob logs out and back in; Alice sees both changes.
	ma := alice.K.Mark()
	t0 := time.Now()
	bob.K.Close()
	_, err = alice.K.Wait(ma, 5*time.Second, nk.StatusLeave(bob.ID()))
	offline := time.Since(t0)
	r.Check("A5", "Bob 登出，Alice 看到 Bob 離線", err == nil, "%s", ms(offline))
	r.Metric("status_offline_after_logout_ms", offline.Milliseconds())

	ma = alice.K.Mark()
	t0 = time.Now()
	err = bob.Connect(ctx, nil)
	if err == nil {
		_, err = alice.K.Wait(ma, 5*time.Second, nk.StatusJoin(bob.ID(), ""))
	}
	online2 := time.Since(t0)
	r.Check("A5", "Bob 再登入，Alice 看到 Bob 上線", err == nil, "%s", ms(online2))
	r.Metric("status_online_after_login_ms", online2.Milliseconds())

	// A6 (extra, for question 4): a client that hangs without closing the
	// socket. Nakama only notices through its ping timeout.
	ma = alice.K.Mark()
	t0 = time.Now()
	bob.K.Freeze()
	_, err = alice.K.Wait(ma, 60*time.Second, nk.StatusLeave(bob.ID()))
	hung := time.Since(t0)
	r.Check("A6", "（加測）Bob 的連線卡住不回應，Alice 最後看到 Bob 離線", err == nil, "%s（Nakama 預設 ping 15 秒、pong 等待 25 秒）", ms(hung))
	r.Metric("status_offline_after_hang_ms", hung.Milliseconds())
	bob.K.Close()
	return nil
}
