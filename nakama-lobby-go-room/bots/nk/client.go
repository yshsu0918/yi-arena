// Package nk is a minimal Nakama client for the test bots: the REST calls
// they need and the realtime socket. Messages use nakama-common's protobuf
// types, encoded as JSON the way Nakama does (protojson with proto names).
package nk

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/heroiclabs/nakama-common/api"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var (
	marshal   = protojson.MarshalOptions{UseProtoNames: true}
	unmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}
)

type Client struct {
	BaseURL   string // e.g. http://localhost:8920
	ServerKey string
	HTTP      *http.Client
}

func NewClient(baseURL, serverKey string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), ServerKey: serverKey, HTTP: &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{MaxIdleConnsPerHost: 200},
	}}
}

// Session is an authenticated user.
type Session struct {
	c        *Client
	Token    string
	UserID   string
	Username string
}

// Error is a non-2xx reply from Nakama. Message holds the runtime error text
// (for example "busy" from the lobby plugin).
type Error struct {
	Status  int
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("nakama %d: %s (code %d)", e.Status, e.Message, e.Code)
}

// ErrMessage returns the Nakama error message, or "" for other errors.
func ErrMessage(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Message
	}
	return ""
}

func (c *Client) do(ctx context.Context, method, path string, auth func(*http.Request), body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	auth(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		e := &Error{Status: resp.StatusCode}
		json.Unmarshal(data, e)
		if e.Message == "" {
			e.Message = string(data)
		}
		return nil, e
	}
	return data, nil
}

// Healthy reports whether Nakama answers its health check.
func (c *Client) Healthy(ctx context.Context) bool {
	_, err := c.do(ctx, http.MethodGet, "/healthcheck", func(*http.Request) {}, nil)
	return err == nil
}

// AuthenticateCustom logs in with a custom ID, creating the account if needed.
func (c *Client) AuthenticateCustom(ctx context.Context, customID, username string) (*Session, error) {
	body, _ := json.Marshal(map[string]string{"id": customID})
	q := url.Values{"create": {"true"}, "username": {username}}
	data, err := c.do(ctx, http.MethodPost, "/v2/account/authenticate/custom?"+q.Encode(), func(r *http.Request) {
		r.SetBasicAuth(c.ServerKey, "")
	}, body)
	if err != nil {
		return nil, err
	}
	var s api.Session
	if err := unmarshal.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	sess := &Session{c: c, Token: s.Token}
	if err := sess.parseToken(); err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Session) parseToken() error {
	parts := strings.Split(s.Token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("bad session token")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	var claims struct {
		UID string `json:"uid"`
		USN string `json:"usn"`
	}
	if err := json.Unmarshal(b, &claims); err != nil {
		return err
	}
	s.UserID, s.Username = claims.UID, claims.USN
	return nil
}

func (s *Session) call(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	return s.c.do(ctx, method, path, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+s.Token) }, body)
}

// AddFriends sends friend requests by username; adding someone who already
// asked you accepts their request.
func (s *Session) AddFriends(ctx context.Context, usernames ...string) error {
	_, err := s.call(ctx, http.MethodPost, "/v2/friend?"+url.Values{"usernames": usernames}.Encode(), nil)
	return err
}

// Friend states.
const (
	FriendMutual   = 0
	FriendSent     = 1
	FriendReceived = 2
)

func (s *Session) ListFriends(ctx context.Context) ([]*api.Friend, error) {
	data, err := s.call(ctx, http.MethodGet, "/v2/friend?limit=100", nil)
	if err != nil {
		return nil, err
	}
	var fl api.FriendList
	if err := unmarshal.Unmarshal(data, &fl); err != nil {
		return nil, err
	}
	return fl.Friends, nil
}

// RPC calls a runtime RPC with a JSON payload and decodes the JSON reply into
// out (when not nil).
func (s *Session) RPC(ctx context.Context, id string, in, out any) error {
	body, _ := json.Marshal(in)
	data, err := s.call(ctx, http.MethodPost, "/v2/rpc/"+id+"?unwrap=true", body)
	if err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ServerRPC calls an RPC with the runtime HTTP key instead of a user session.
func (c *Client) ServerRPC(ctx context.Context, httpKey, id string, in, out any) error {
	body, _ := json.Marshal(in)
	q := url.Values{"http_key": {httpKey}, "unwrap": {"true"}}
	data, err := c.do(ctx, http.MethodPost, "/v2/rpc/"+id+"?"+q.Encode(), func(*http.Request) {}, body)
	if err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (s *Session) ListNotifications(ctx context.Context) ([]*api.Notification, error) {
	data, err := s.call(ctx, http.MethodGet, "/v2/notification?limit=100", nil)
	if err != nil {
		return nil, err
	}
	var nl api.NotificationList
	if err := unmarshal.Unmarshal(data, &nl); err != nil {
		return nil, err
	}
	return nl.Notifications, nil
}

// decode is used for protojson replies inside JSON strings.
func decode(data []byte, m proto.Message) error { return unmarshal.Unmarshal(data, m) }
