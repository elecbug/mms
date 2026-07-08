package game

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

type Manager struct {
	cfg   Config
	mu    sync.Mutex
	rooms map[string]*Room
}

func NewManager(cfg Config) *Manager {
	cfg = normalizeConfig(cfg)
	return &Manager{cfg: cfg, rooms: make(map[string]*Room)}
}

func normalizeConfig(cfg Config) Config {
	if cfg.Width <= 0 {
		cfg.Width = 12
	}
	if cfg.Height <= 0 {
		cfg.Height = 12
	}
	if cfg.MaxPlayers <= 0 {
		cfg.MaxPlayers = 2
	}
	if cfg.ScoreRate <= 0 {
		cfg.ScoreRate = 10
	}
	if cfg.IdleAfter <= 0 {
		cfg.IdleAfter = 8 * time.Second
	}
	if cfg.DisconnectGrace <= 0 {
		cfg.DisconnectGrace = 30 * time.Second
	}
	maxMines := cfg.Width*cfg.Height - 9
	if maxMines < 1 {
		maxMines = cfg.Width*cfg.Height - 1
	}
	if cfg.MineCount <= 0 {
		cfg.MineCount = 25
	}
	if cfg.MineCount > maxMines {
		cfg.MineCount = maxMines
	}
	return cfg
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

func (m *Manager) JoinRoom(roomID, playerName, requestedPlayerID, token string) (*Room, *Player, error) {
	r := m.GetOrCreateRoom(roomID)
	p, err := r.Join(playerName, requestedPlayerID, token)
	if err != nil {
		return nil, nil, err
	}
	return r, p, nil
}

var ErrRoomFull = errors.New("room is full")
var ErrInvalidToken = errors.New("invalid reconnect token")

func shortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "room"
	}
	return hex.EncodeToString(b)
}
