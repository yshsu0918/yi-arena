package lobby

import (
	"sync"
	"sync/atomic"
)

// Client is one connected user as seen by the lobby and rooms. It does not
// know about WebSockets: the gateway drains Outbox and writes to the socket.
type Client struct {
	Name string

	out     chan []byte
	done    chan struct{}
	closing sync.Once
	room    atomic.Pointer[Room]
	stats   *Stats
}

func NewClient(name string, queue int, stats *Stats) *Client {
	return &Client{Name: name, out: make(chan []byte, queue), done: make(chan struct{}), stats: stats}
}

// Send queues a message without blocking. When the queue is full the client
// is too slow: it is kicked so that it never holds up the lobby or a room.
func (c *Client) Send(msg []byte) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.out <- msg:
		c.stats.MessagesOut.Add(1)
		return true
	default:
		c.stats.SlowKicks.Add(1)
		c.Kick()
		return false
	}
}

func (c *Client) Outbox() <-chan []byte { return c.out }
func (c *Client) Done() <-chan struct{} { return c.done }
func (c *Client) Kick()                 { c.closing.Do(func() { close(c.done) }) }

// Room returns the room the client is currently in, or nil when in the lobby.
// Only the lobby changes it.
func (c *Client) Room() *Room { return c.room.Load() }

// Stats are process-wide counters, read by the /stats endpoint.
type Stats struct {
	Connected   atomic.Int64
	MessagesIn  atomic.Int64
	MessagesOut atomic.Int64
	SlowKicks   atomic.Int64
	RoomsOpened atomic.Int64
}
