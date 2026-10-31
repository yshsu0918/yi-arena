// Package lobbyclient reports room events to the lobby through Nakama's own
// gRPC API: nakama.api.Nakama/RpcFunc with id "room_event", authenticated by
// the runtime HTTP key (no user session needed).
package lobbyclient

import (
	"context"
	"hash/fnv"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/heroiclabs/nakama-common/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/roompb"
)

const rpcMethod = "/nakama.api.Nakama/RpcFunc"

// Sender delivers one event; the default calls Nakama.
type Sender func(ctx context.Context, ev *roompb.RoomEvent) error

// Dispatcher queues events and retries each until the lobby accepts it.
// Events of one room always go to the same shard, so they arrive in order;
// a failing room only delays rooms on its shard.
type Dispatcher struct {
	send   Sender
	shards []*shard

	Sent    atomic.Int64
	Retries atomic.Int64
}

type shard struct {
	mu   sync.Mutex
	cond *sync.Cond
	q    []*roompb.RoomEvent
}

// NewNakama connects to Nakama's gRPC API (lazily; Nakama may be down).
func NewNakama(addr, httpKey string, shards int) (*Dispatcher, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	send := func(ctx context.Context, ev *roompb.RoomEvent) error {
		payload, err := protojson.Marshal(ev)
		if err != nil {
			return err
		}
		return conn.Invoke(ctx, rpcMethod, &api.Rpc{Id: "room_event", Payload: string(payload), HttpKey: httpKey}, &api.Rpc{})
	}
	return New(send, shards), nil
}

func New(send Sender, shards int) *Dispatcher {
	d := &Dispatcher{send: send}
	for range shards {
		s := &shard{}
		s.cond = sync.NewCond(&s.mu)
		d.shards = append(d.shards, s)
		go d.work(s)
	}
	return d
}

// Emit never blocks.
func (d *Dispatcher) Emit(ev *roompb.RoomEvent) {
	h := fnv.New32a()
	h.Write([]byte(ev.RoomId))
	s := d.shards[int(h.Sum32()%uint32(len(d.shards)))]
	s.mu.Lock()
	s.q = append(s.q, ev)
	s.mu.Unlock()
	s.cond.Signal()
}

// Pending is the number of events not yet accepted by the lobby.
func (d *Dispatcher) Pending() int {
	n := 0
	for _, s := range d.shards {
		s.mu.Lock()
		n += len(s.q)
		s.mu.Unlock()
	}
	return n
}

func (d *Dispatcher) work(s *shard) {
	for {
		s.mu.Lock()
		for len(s.q) == 0 {
			s.cond.Wait()
		}
		ev := s.q[0]
		s.mu.Unlock()

		backoff := 100 * time.Millisecond
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := d.send(ctx, ev)
			cancel()
			if err == nil {
				break
			}
			d.Retries.Add(1)
			log.Printf("room event %s #%d for %s failed, retrying in %s: %v", ev.Type, ev.Seq, ev.RoomId, backoff, err)
			time.Sleep(backoff)
			backoff = min(backoff*2, 2*time.Second)
		}
		d.Sent.Add(1)

		s.mu.Lock()
		s.q[0] = nil
		s.q = s.q[1:]
		s.mu.Unlock()
	}
}
