// Package ticket is the entry-ticket format shared by the lobby (which signs)
// and the room service (which verifies). A ticket is
//
//	base64url(json claims) "." base64url(HMAC-SHA256(secret, first part))
//
// It only uses the standard library so that it adds no dependency to the
// Nakama plugin, whose dependencies must match Nakama's exactly.
package ticket

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const RolePlayer = "player"

type Claims struct {
	RoomID    string `json:"room_id"`
	UserID    string `json:"user_id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	ExpiresAt int64  `json:"exp"` // unix seconds
	ID        string `json:"jti"` // unique; the room service accepts it once
}

var (
	ErrMalformed = errors.New("malformed ticket")
	ErrSignature = errors.New("bad ticket signature")
	ErrExpired   = errors.New("ticket expired")
)

var enc = base64.RawURLEncoding

// NewID returns a random ticket ID.
func NewID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Sign encodes and signs the claims. An empty ID is filled in.
func Sign(secret []byte, c Claims) (string, Claims, error) {
	if len(secret) == 0 {
		return "", c, errors.New("empty ticket secret")
	}
	if c.ID == "" {
		c.ID = NewID()
	}
	body, err := json.Marshal(c)
	if err != nil {
		return "", c, err
	}
	payload := enc.EncodeToString(body)
	return payload + "." + enc.EncodeToString(mac(secret, payload)), c, nil
}

// Verify checks the signature and expiry and returns the claims. Checking
// that the ticket is for the right room and has not been used yet is up to
// the caller.
func Verify(secret []byte, token string, now time.Time) (Claims, error) {
	var c Claims
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || payload == "" || sig == "" {
		return c, ErrMalformed
	}
	got, err := enc.DecodeString(sig)
	if err != nil {
		return c, ErrMalformed
	}
	if !hmac.Equal(got, mac(secret, payload)) {
		return c, ErrSignature
	}
	body, err := enc.DecodeString(payload)
	if err != nil || json.Unmarshal(body, &c) != nil || c.RoomID == "" || c.UserID == "" || c.ID == "" {
		return Claims{}, ErrMalformed
	}
	if now.Unix() >= c.ExpiresAt {
		return c, ErrExpired
	}
	return c, nil
}

func mac(secret []byte, payload string) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(payload))
	return h.Sum(nil)
}
