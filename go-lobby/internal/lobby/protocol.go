package lobby

import (
	"encoding/json"
	"time"
)

// Inbound is a message from a client. To is "lobby" or "room".
type Inbound struct {
	To   string          `json:"to"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Outbound is a message to a client. From is "lobby" or "room:<id>".
// TS is the server time (unix nanoseconds) when the message was built.
type Outbound struct {
	From string `json:"from"`
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
	TS   int64  `json:"ts"`
}

// ChatIn is the payload of a chat message sent by a client. ID and SentAt are
// opaque to the server and echoed back so clients can measure delivery.
type ChatIn struct {
	Text   string `json:"text"`
	ID     string `json:"id,omitempty"`
	SentAt int64  `json:"sent_at,omitempty"`
}

// ChatOut is a chat message delivered to clients.
type ChatOut struct {
	From   string `json:"from"`
	Text   string `json:"text"`
	ID     string `json:"id,omitempty"`
	SentAt int64  `json:"sent_at,omitempty"`
}

type DMIn struct {
	To     string `json:"to"`
	Text   string `json:"text"`
	ID     string `json:"id,omitempty"`
	SentAt int64  `json:"sent_at,omitempty"`
}

type DMOut struct {
	From   string `json:"from"`
	Text   string `json:"text"`
	ID     string `json:"id,omitempty"`
	SentAt int64  `json:"sent_at,omitempty"`
}

type InviteIn struct {
	To string `json:"to"`
}

type InviteReplyIn struct {
	InviteID string `json:"invite_id"`
	Accept   bool   `json:"accept"`
}

type InviteOut struct {
	InviteID string `json:"invite_id"`
	From     string `json:"from"`
	To       string `json:"to"`
}

type InviteResultOut struct {
	InviteID string `json:"invite_id"`
	Result   string `json:"result"` // accepted | declined | expired | cancelled
}

// PlayerInfo is one entry of the player list.
type PlayerInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"` // lobby | room
	RoomID string `json:"room_id,omitempty"`
}

// RoomInfo is one entry of the room list.
type RoomInfo struct {
	ID      string   `json:"id"`
	Players []string `json:"players"`
}

type WelcomeOut struct {
	You     string       `json:"you"`
	Players []PlayerInfo `json:"players"`
	Rooms   []RoomInfo   `json:"rooms"`
}

type PlayersOut struct {
	Players []PlayerInfo `json:"players"`
}

type RoomListOut struct {
	Op   string   `json:"op"` // add | remove
	Room RoomInfo `json:"room"`
}

type RoomJoinedOut struct {
	RoomID  string   `json:"room_id"`
	Players []string `json:"players"`
}

type ErrorOut struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const (
	StatusLobby = "lobby"
	StatusRoom  = "room"

	MaxTextLen = 500
)

func encode(from, typ string, data any) []byte {
	b, err := json.Marshal(Outbound{From: from, Type: typ, Data: data, TS: time.Now().UnixNano()})
	if err != nil {
		panic(err) // only our own structs are encoded here
	}
	return b
}

func errorMsg(from, code, message string) []byte {
	return encode(from, "error", ErrorOut{Code: code, Message: message})
}
