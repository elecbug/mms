package game

import "time"

type Config struct {
	Width           int
	Height          int
	MineCount       int
	ScoreRate       float64
	CellBonus       float64
	IdleAfter       time.Duration
	DisconnectGrace time.Duration
	MaxPlayers      int
}

type Cell struct {
	Mine     bool
	Value    int
	Revealed bool
	OpenedBy string
}

type PublicCell struct {
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Value    int    `json:"value"`
	OpenedBy string `json:"openedBy,omitempty"`
}

type PublicMark struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	State string `json:"state"`
}

type Player struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Score          float64        `json:"score"`
	Combo          int            `json:"combo"`
	Connected      bool           `json:"connected"`
	Ready          bool           `json:"ready"`
	OpenedSafe     int            `json:"openedSafe"`
	SafeClicks     int            `json:"safeClicks"`
	InvalidActs    int            `json:"invalidActs"`
	Token          string         `json:"-"`
	Marks          map[int]string `json:"-"`
	DisconnectedAt time.Time      `json:"-"`
}

type Result struct {
	WinnerID string `json:"winnerId,omitempty"`
	LoserID  string `json:"loserId,omitempty"`
	Reason   string `json:"reason"`
	Message  string `json:"message"`
}

type Event struct {
	Seq      uint64 `json:"seq"`
	AtUnixMs int64  `json:"atUnixMs"`
	Type     string `json:"type"`
	PlayerID string `json:"playerId,omitempty"`
	Message  string `json:"message"`
}

type Snapshot struct {
	Seq          uint64       `json:"seq"`
	RoomID       string       `json:"roomId"`
	Phase        string       `json:"phase"`
	Width        int          `json:"width"`
	Height       int          `json:"height"`
	MineCount    int          `json:"mineCount"`
	Players      []*Player    `json:"players"`
	Revealed     []PublicCell `json:"revealed"`
	Marks        []PublicMark `json:"marks"`
	RevealedSafe int          `json:"revealedSafe"`
	SafeTotal    int          `json:"safeTotal"`
	ScorePool    float64      `json:"scorePool"`
	ScoreRate    float64      `json:"scoreRate"`
	IdleMsLeft   int64        `json:"idleMsLeft"`
	Result       *Result      `json:"result,omitempty"`
	Events       []Event      `json:"events"`
}
