package room

import (
	"sync"
	"sync/atomic"
)

// Metrics are process-wide counters, read by /stats.
var Metrics struct {
	Connections atomic.Int64
	MessagesOut atomic.Int64
	SlowKicks   atomic.Int64
	RoomsOpened atomic.Int64
}

// Conn is one WebSocket connection as seen by a room. It does not know about
// WebSockets: the gateway drains Outbox and writes to the socket.
type Conn struct {
	UserID string
	Name   string

	out     chan []byte
	done    chan struct{}
	closing sync.Once
}

func NewConn(userID, name string, queue int) *Conn {
	return &Conn{UserID: userID, Name: name, out: make(chan []byte, queue), done: make(chan struct{})}
}

// Send queues a message without blocking. When the queue is full the
// connection is too slow: it is kicked so that it never holds up the room.
func (c *Conn) Send(msg []byte) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.out <- msg:
		Metrics.MessagesOut.Add(1)
		return true
	default:
		Metrics.SlowKicks.Add(1)
		c.Kick()
		return false
	}
}

// Close asks the writer to close the socket after the messages already
// queued. A nil message is the marker.
func (c *Conn) Close() {
	select {
	case c.out <- nil:
	default:
		c.Kick()
	}
}

// Kick closes the connection right away.
func (c *Conn) Kick() { c.closing.Do(func() { close(c.done) }) }

func (c *Conn) Outbox() <-chan []byte { return c.out }
func (c *Conn) Done() <-chan struct{} { return c.done }
