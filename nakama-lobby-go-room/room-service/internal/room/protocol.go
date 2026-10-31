package room

import "encoding/json"

// Client <-> room messages (JSON over WebSocket).
//
//	client -> room: hello{ticket} (first, handled by the gateway), chat{text, id, sent_at}, leave
//	room -> client: welcome, chat, member_joined, member_disconnected, member_left,
//	                left, room_closed{reason}, error{code}

type Inbound struct {
	Type   string `json:"type"`
	Ticket string `json:"ticket,omitempty"`
	Text   string `json:"text,omitempty"`
	ID     string `json:"id,omitempty"`      // client message ID, echoed back
	SentAt int64  `json:"sent_at,omitempty"` // client clock, echoed back for latency
}

type MemberOut struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

type WelcomeOut struct {
	Type      string      `json:"type"`
	RoomID    string      `json:"room_id"`
	You       string      `json:"you"`
	Members   []MemberOut `json:"members"`
	Connected []string    `json:"connected"`
	Status    string      `json:"status"`
}

type ChatOut struct {
	Type   string `json:"type"`
	From   string `json:"from"`
	UserID string `json:"user_id"`
	Text   string `json:"text"`
	ID     string `json:"id,omitempty"`
	SentAt int64  `json:"sent_at,omitempty"`
}

type MemberEventOut struct {
	Type   string `json:"type"`
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

type ClosedOut struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

type ErrorOut struct {
	Type string `json:"type"`
	Code string `json:"code"`
}

func encode(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func ErrorMsg(code string) []byte { return encode(ErrorOut{Type: "error", Code: code}) }

const maxChat = 500
