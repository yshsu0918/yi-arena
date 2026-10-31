// Package roomc is a WebSocket client for the room service.
package roomc

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Msg is any message from the room (fields depend on Type).
type Msg struct {
	Type   string `json:"type"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
	From   string `json:"from"`
	UserID string `json:"user_id"`
	Text   string `json:"text"`
	ID     string `json:"id"`
	SentAt int64  `json:"sent_at"`
	Status string `json:"status"`
	At     time.Time
}

type Conn struct {
	ws      *websocket.Conn
	onMsg   func(Msg)
	mu      sync.Mutex
	history []Msg
	changed chan struct{}
	done    chan struct{}
}

// Dial connects and sends hello with the ticket. It returns the room's first
// reply: "welcome" with a live connection, or "error" with Code set (the
// connection is then closed by the room). An empty ticket sends a hello
// without one.
func Dial(ctx context.Context, wsURL, ticket string, onMsg func(Msg)) (*Conn, Msg, error) {
	ws, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return nil, Msg{}, err
	}
	hello, _ := json.Marshal(map[string]string{"type": "hello", "ticket": ticket})
	if ticket == "" {
		hello = []byte(`{"type":"hello"}`)
	}
	if err := ws.Write(ctx, websocket.MessageText, hello); err != nil {
		ws.CloseNow()
		return nil, Msg{}, err
	}
	_, data, err := ws.Read(ctx)
	if err != nil {
		ws.CloseNow()
		return nil, Msg{}, fmt.Errorf("no reply to hello: %w", err)
	}
	var first Msg
	json.Unmarshal(data, &first)
	first.At = time.Now()
	if first.Type != "welcome" {
		// The room closes the connection after an error; confirm it does.
		rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, _, err := ws.Read(rctx)
		cancel()
		if err == nil {
			ws.CloseNow()
			return nil, first, fmt.Errorf("connection still open after error %s", first.Code)
		}
		ws.CloseNow()
		return nil, first, nil
	}
	c := &Conn{ws: ws, onMsg: onMsg, changed: make(chan struct{}), done: make(chan struct{})}
	go c.read()
	return c, first, nil
}

func (c *Conn) read() {
	defer close(c.done)
	for {
		_, data, err := c.ws.Read(context.Background())
		if err != nil {
			return
		}
		var m Msg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		m.At = time.Now()
		if c.onMsg != nil {
			c.onMsg(m)
		}
		c.mu.Lock()
		c.history = append(c.history, m)
		close(c.changed)
		c.changed = make(chan struct{})
		c.mu.Unlock()
	}
}

func (c *Conn) send(ctx context.Context, v any) error {
	b, _ := json.Marshal(v)
	return c.ws.Write(ctx, websocket.MessageText, b)
}

func (c *Conn) Chat(ctx context.Context, id, text string) error {
	return c.send(ctx, map[string]any{"type": "chat", "id": id, "text": text, "sent_at": time.Now().UnixMicro()})
}

func (c *Conn) Leave(ctx context.Context) error {
	return c.send(ctx, map[string]string{"type": "leave"})
}

// Done is closed when the connection ends.
func (c *Conn) Done() <-chan struct{} { return c.done }

func (c *Conn) Close() { c.ws.Close(websocket.StatusNormalClosure, "") }

// Wait returns the first message (in arrival order) that matches.
func (c *Conn) Wait(timeout time.Duration, match func(Msg) bool) (Msg, error) {
	deadline := time.After(timeout)
	for i := 0; ; {
		c.mu.Lock()
		for ; i < len(c.history); i++ {
			if match(c.history[i]) {
				m := c.history[i]
				c.mu.Unlock()
				return m, nil
			}
		}
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-changed:
		case <-deadline:
			return Msg{}, fmt.Errorf("timed out after %s", timeout)
		}
	}
}

func Type(t string) func(Msg) bool { return func(m Msg) bool { return m.Type == t } }

func ChatID(id string) func(Msg) bool {
	return func(m Msg) bool { return m.Type == "chat" && m.ID == id }
}
