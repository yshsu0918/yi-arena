package room

import (
	"errors"
	"sort"
	"sync"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/proto/roompb"
)

var ErrExists = errors.New("room exists")

// Manager is the room table: create, find, list and close rooms.
type Manager struct {
	cfg     Config
	emitter Emitter

	mu    sync.RWMutex
	rooms map[string]*Room
}

func NewManager(cfg Config, emitter Emitter) *Manager {
	return &Manager{cfg: cfg, emitter: emitter, rooms: map[string]*Room{}}
}

func (m *Manager) Create(id string, members []*roompb.Member) (*Room, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rooms[id]; ok {
		return nil, ErrExists
	}
	r := newRoom(id, members, m.cfg, m.emitter, m.remove)
	m.rooms[id] = r
	Metrics.RoomsOpened.Add(1)
	go r.Run()
	return r, nil
}

func (m *Manager) remove(r *Room) {
	m.mu.Lock()
	if m.rooms[r.ID] == r {
		delete(m.rooms, r.ID)
	}
	m.mu.Unlock()
}

func (m *Manager) Get(id string) *Room {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rooms[id]
}

func (m *Manager) List() []*roompb.RoomInfo {
	m.mu.RLock()
	out := make([]*roompb.RoomInfo, 0, len(m.rooms))
	for _, r := range m.rooms {
		out = append(out, r.Info())
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].RoomId < out[j].RoomId })
	return out
}

func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.rooms)
}

// Close force-closes a room. It reports whether the room existed.
func (m *Manager) Close(id, reason string) bool {
	r := m.Get(id)
	if r == nil {
		return false
	}
	r.Close(reason)
	return true
}
