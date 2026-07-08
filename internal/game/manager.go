package game

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
)

type Manager struct {
	cfg   Config
	mu    sync.Mutex
	rooms map[string]*Room
}

func NewManager(cfg Config) *Manager {
	if cfg.Width <= 0 {
		cfg.Width = 12
	}
	if cfg.Height <= 0 {
		cfg.Height = 12
	}
	if cfg.MineCount <= 0 {
		cfg.MineCount = 25
	}
	if cfg.MaxPlayers <= 0 {
		cfg.MaxPlayers = 2
	}
	return &Manager{cfg: cfg, rooms: make(map[string]*Room)}
}

func (m *Manager) GetOrCreateRoom(roomID string) *Room {
	m.mu.Lock()
	defer m.mu.Unlock()
	if roomID == "" {
		roomID = shortID()
	}
	if r, ok := m.rooms[roomID]; ok {
		return r
	}
	r := NewRoom(roomID, m.cfg)
	m.rooms[roomID] = r
	return r
}

func (m *Manager) JoinRoom(roomID, playerName string) (*Room, *Player, error) {
	r := m.GetOrCreateRoom(roomID)
	p, err := r.Join(playerName)
	if err != nil {
		return nil, nil, err
	}
	return r, p, nil
}

var ErrRoomFull = errors.New("room is full")

func shortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "room"
	}
	return hex.EncodeToString(b)
}
