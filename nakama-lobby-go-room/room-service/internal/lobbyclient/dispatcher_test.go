package lobbyclient

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/roompb"
)

// Events are retried until accepted and arrive in order per room.
func TestRetryKeepsOrder(t *testing.T) {
	var mu sync.Mutex
	var got []uint64
	failures := 3
	d := New(func(_ context.Context, ev *roompb.RoomEvent) error {
		mu.Lock()
		defer mu.Unlock()
		if failures > 0 {
			failures--
			return errors.New("lobby down")
		}
		got = append(got, ev.Seq)
		return nil
	}, 4)
	for i := 1; i <= 5; i++ {
		d.Emit(&roompb.RoomEvent{RoomId: "r1", Seq: uint64(i)})
	}
	deadline := time.Now().Add(5 * time.Second)
	for d.Pending() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 5 {
		t.Fatalf("delivered %v", got)
	}
	for i, s := range got {
		if s != uint64(i+1) {
			t.Fatalf("out of order: %v", got)
		}
	}
	if d.Retries.Load() != 3 {
		t.Errorf("retries %d", d.Retries.Load())
	}
}
