package main

import (
	"context"
	"time"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/roompb"
)

// reconcileLoop asks the room service for its rooms right away (so a
// restarted Nakama rebuilds the room list) and then periodically. The room
// service is the source of truth.
func (l *lobby) reconcileLoop() {
	for {
		l.reconcile()
		time.Sleep(l.cfg.reconcileInterval)
	}
}

func (l *lobby) reconcile() {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	resp, err := l.rooms.ListRooms(ctx, &roompb.ListRoomsRequest{})
	cancel()

	l.mu.Lock()
	defer l.mu.Unlock()
	l.st.ReconcileRuns++
	if err != nil {
		l.st.ReconcileErrors++
		l.st.LastReconcileEr = time.Now().Format(time.RFC3339) + " " + err.Error()
		return
	}
	l.st.LastReconcileOK = time.Now().Format(time.RFC3339)
	if l.st.RoomInstanceID != resp.GetInstanceId() {
		if l.st.RoomInstanceID != "" {
			l.log.Warn("room service restarted: instance %s -> %s", l.st.RoomInstanceID, resp.GetInstanceId())
		}
		l.st.RoomInstanceID = resp.GetInstanceId()
	}

	live := map[string]bool{}
	for _, info := range resp.GetRooms() {
		id := info.GetRoomId()
		live[id] = true
		mark := l.seqs[id]
		r := l.table[id]
		switch {
		case r == nil && !mark.removedAt.IsZero() && info.GetSeq() <= mark.seq:
			// Removed after this snapshot's last event: the snapshot is stale.
		case r == nil:
			r = entryFromInfo(info)
			l.addRoom(r)
			for _, m := range r.Members {
				l.setStatus(m.UserID, statusPlaying)
			}
			mark.seq, mark.removedAt = info.GetSeq(), time.Time{}
			l.seqs[id] = mark
			l.st.ReconcileAdded++
			l.pushList("upsert", r, "")
		case r.Status == roomCreating:
			// CreateRoom still in flight; rpcInviteReply finishes it.
		case info.GetSeq() > mark.seq:
			// The lobby missed events (they will be ignored if they arrive).
			mark.seq = info.GetSeq()
			l.seqs[id] = mark
			r.applyInfo(info)
			l.st.ReconcileFixed++
			l.pushList("upsert", r, "")
		}
	}
	for id, r := range l.table {
		// Rooms created after the snapshot was requested may simply be newer.
		if live[id] || r.Status == roomCreating || r.createdAt.After(started) {
			continue
		}
		l.st.ReconcileGhosts++
		l.removeRoom(r, "gone")
	}
	l.pruneSeqs()
}
