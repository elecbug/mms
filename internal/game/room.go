package game

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	mathrand "math/rand"
	"sort"
	"sync"
	"time"
)

const (
	PhaseWaiting = "waiting"
	PhasePlaying = "playing"
	PhaseEnded   = "ended"
)

type Room struct {
	mu sync.Mutex

	id  string
	cfg Config
	rng *mathrand.Rand

	phase string
	seq   uint64

	board []Cell

	players map[string]*Player
	result  *Result

	revealedSafe int
	safeTotal    int

	scorePool         float64
	lastScoreTime     time.Time
	lastValidAction   time.Time
	lastScoringPlayer string

	subscribers map[string]chan Snapshot
	stopTicker  chan struct{}
}

func NewRoom(id string, cfg Config) *Room {
	seed := secureSeed()
	r := &Room{
		id:          id,
		cfg:         cfg,
		rng:         mathrand.New(mathrand.NewSource(seed)),
		phase:       PhaseWaiting,
		players:     make(map[string]*Player),
		subscribers: make(map[string]chan Snapshot),
		stopTicker:  make(chan struct{}),
	}
	r.generateBoard()
	go r.idleLoop()
	return r
}

func (r *Room) Join(name string) (*Player, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if name == "" {
		name = fmt.Sprintf("Player %d", len(r.players)+1)
	}
	if len(r.players) >= r.cfg.MaxPlayers {
		return nil, ErrRoomFull
	}
	id := fmt.Sprintf("p%d", len(r.players)+1)
	p := &Player{ID: id, Name: name, Connected: true}
	r.players[id] = p
	r.bumpLocked()

	if len(r.players) == r.cfg.MaxPlayers && r.phase == PhaseWaiting {
		r.startLocked()
	}

	return clonePlayer(p), nil
}

func (r *Room) Subscribe(playerID string) (<-chan Snapshot, func()) {
	ch := make(chan Snapshot, 16)
	r.mu.Lock()
	r.subscribers[playerID] = ch
	ch <- r.snapshotLocked(time.Now())
	r.mu.Unlock()

	cancel := func() {
		r.mu.Lock()
		if current, ok := r.subscribers[playerID]; ok && current == ch {
			delete(r.subscribers, playerID)
			close(ch)
		}
		if p, ok := r.players[playerID]; ok {
			p.Connected = false
		}
		r.bumpLocked()
		r.broadcastLocked()
		r.mu.Unlock()
	}
	return ch, cancel
}

func (r *Room) Reveal(playerID string, x, y int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	if r.phase != PhasePlaying {
		return fmt.Errorf("game is not playing")
	}
	p, ok := r.players[playerID]
	if !ok {
		return fmt.Errorf("unknown player")
	}
	if !r.inBounds(x, y) {
		return fmt.Errorf("cell is out of bounds")
	}
	idx := r.index(x, y)
	cell := &r.board[idx]
	if cell.Revealed {
		return fmt.Errorf("cell is already revealed")
	}

	r.accrueScorePoolLocked(now)

	if cell.Mine {
		cell.Revealed = true
		cell.OpenedBy = playerID
		winnerID := r.otherPlayerIDLocked(playerID)
		r.result = &Result{
			WinnerID: winnerID,
			LoserID:  playerID,
			Reason:   "mine",
			Message:  fmt.Sprintf("%s clicked a mine", p.Name),
		}
		r.phase = PhaseEnded
		r.bumpLocked()
		r.broadcastLocked()
		return nil
	}

	opened := r.revealFloodLocked(x, y, playerID)
	multiplier := comboMultiplier(p.Combo)
	gain := r.scorePool*multiplier + float64(opened)*r.cfg.CellBonus
	p.Score += gain

	if r.lastScoringPlayer == playerID {
		p.Combo++
	} else {
		p.Combo = 1
	}
	for id, other := range r.players {
		if id != playerID {
			other.Combo = 0
		}
	}

	r.scorePool = 0
	r.lastScoreTime = now
	r.lastValidAction = now
	r.lastScoringPlayer = playerID

	if r.revealedSafe >= r.safeTotal {
		r.endByScoreLocked()
	}

	r.bumpLocked()
	r.broadcastLocked()
	return nil
}

func (r *Room) Resign(playerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase == PhaseEnded {
		return
	}
	winnerID := r.otherPlayerIDLocked(playerID)
	r.result = &Result{WinnerID: winnerID, LoserID: playerID, Reason: "resign", Message: "player resigned"}
	r.phase = PhaseEnded
	r.bumpLocked()
	r.broadcastLocked()
}

func (r *Room) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked(time.Now())
}

func (r *Room) startLocked() {
	now := time.Now()
	r.phase = PhasePlaying
	r.scorePool = 0
	r.lastScoreTime = now
	r.lastValidAction = now
	r.lastScoringPlayer = ""
}

func (r *Room) idleLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.handleIdleReveal()
		case <-r.stopTicker:
			return
		}
	}
}

func (r *Room) handleIdleReveal() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	if r.phase != PhasePlaying || r.cfg.IdleAfter <= 0 {
		return
	}
	if now.Sub(r.lastValidAction) < r.cfg.IdleAfter {
		return
	}

	candidates := make([]int, 0)
	for i, c := range r.board {
		if !c.Mine && !c.Revealed {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		r.endByScoreLocked()
		r.bumpLocked()
		r.broadcastLocked()
		return
	}

	idx := candidates[r.rng.Intn(len(candidates))]
	r.board[idx].Revealed = true
	r.board[idx].OpenedBy = "auto"
	r.revealedSafe++

	// Waiting must not become a dominant strategy.
	r.scorePool = 0
	r.lastScoreTime = now
	r.lastValidAction = now
	r.lastScoringPlayer = ""
	for _, p := range r.players {
		p.Combo = 0
	}

	if r.revealedSafe >= r.safeTotal {
		r.endByScoreLocked()
	}

	r.bumpLocked()
	r.broadcastLocked()
}

func (r *Room) generateBoard() {
	r.board = make([]Cell, r.cfg.Width*r.cfg.Height)
	r.safeTotal = r.cfg.Width*r.cfg.Height - r.cfg.MineCount

	excluded := map[int]bool{}
	cx, cy := r.cfg.Width/2, r.cfg.Height/2
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			x, y := cx+dx, cy+dy
			if r.inBounds(x, y) {
				excluded[r.index(x, y)] = true
			}
		}
	}

	candidates := make([]int, 0, len(r.board))
	for i := range r.board {
		if !excluded[i] {
			candidates = append(candidates, i)
		}
	}
	r.rng.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	for i := 0; i < r.cfg.MineCount && i < len(candidates); i++ {
		r.board[candidates[i]].Mine = true
	}

	for y := 0; y < r.cfg.Height; y++ {
		for x := 0; x < r.cfg.Width; x++ {
			idx := r.index(x, y)
			if r.board[idx].Mine {
				continue
			}
			r.board[idx].Value = r.countMinesAround(x, y)
		}
	}

	// Reveal a common safe 3x3 starting area without granting score.
	for idx := range excluded {
		if idx >= 0 && idx < len(r.board) && !r.board[idx].Mine && !r.board[idx].Revealed {
			r.board[idx].Revealed = true
			r.board[idx].OpenedBy = "start"
			r.revealedSafe++
		}
	}
}

func (r *Room) revealFloodLocked(x, y int, openedBy string) int {
	queue := [][2]int{{x, y}}
	opened := 0
	seen := make(map[int]bool)

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		cx, cy := cur[0], cur[1]
		if !r.inBounds(cx, cy) {
			continue
		}
		idx := r.index(cx, cy)
		if seen[idx] || r.board[idx].Revealed || r.board[idx].Mine {
			continue
		}
		seen[idx] = true
		r.board[idx].Revealed = true
		r.board[idx].OpenedBy = openedBy
		opened++
		r.revealedSafe++

		if r.board[idx].Value == 0 {
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if dx == 0 && dy == 0 {
						continue
					}
					queue = append(queue, [2]int{cx + dx, cy + dy})
				}
			}
		}
	}
	return opened
}

func (r *Room) endByScoreLocked() {
	players := make([]*Player, 0, len(r.players))
	for _, p := range r.players {
		players = append(players, p)
	}
	if len(players) == 0 {
		r.result = &Result{Reason: "draw", Message: "no players"}
	} else if len(players) == 1 {
		r.result = &Result{WinnerID: players[0].ID, Reason: "score", Message: "all safe cells were revealed"}
	} else if players[0].Score > players[1].Score {
		r.result = &Result{WinnerID: players[0].ID, LoserID: players[1].ID, Reason: "score", Message: "all safe cells were revealed"}
	} else if players[1].Score > players[0].Score {
		r.result = &Result{WinnerID: players[1].ID, LoserID: players[0].ID, Reason: "score", Message: "all safe cells were revealed"}
	} else {
		r.result = &Result{Reason: "draw", Message: "scores are tied"}
	}
	r.phase = PhaseEnded
}

func (r *Room) accrueScorePoolLocked(now time.Time) {
	if r.phase != PhasePlaying {
		return
	}
	if r.lastScoreTime.IsZero() {
		r.lastScoreTime = now
		return
	}
	delta := now.Sub(r.lastScoreTime).Seconds()
	if delta > 0 {
		r.scorePool += delta * r.cfg.ScoreRate
		r.lastScoreTime = now
	}
}

func (r *Room) snapshotLocked(now time.Time) Snapshot {
	pool := r.scorePool
	if r.phase == PhasePlaying && !r.lastScoreTime.IsZero() {
		pool += now.Sub(r.lastScoreTime).Seconds() * r.cfg.ScoreRate
	}

	players := make([]*Player, 0, len(r.players))
	for _, p := range r.players {
		players = append(players, clonePlayer(p))
	}
	sort.Slice(players, func(i, j int) bool { return players[i].ID < players[j].ID })

	revealed := make([]PublicCell, 0, r.revealedSafe)
	for y := 0; y < r.cfg.Height; y++ {
		for x := 0; x < r.cfg.Width; x++ {
			c := r.board[r.index(x, y)]
			if c.Revealed {
				value := c.Value
				if c.Mine && r.phase == PhaseEnded {
					value = -1
				}
				revealed = append(revealed, PublicCell{X: x, Y: y, Value: value, OpenedBy: c.OpenedBy})
			}
		}
	}

	idleLeft := int64(0)
	if r.phase == PhasePlaying && r.cfg.IdleAfter > 0 {
		idleLeft = r.cfg.IdleAfter.Milliseconds() - now.Sub(r.lastValidAction).Milliseconds()
		if idleLeft < 0 {
			idleLeft = 0
		}
	}

	return Snapshot{
		Seq:          r.seq,
		RoomID:       r.id,
		Phase:        r.phase,
		Width:        r.cfg.Width,
		Height:       r.cfg.Height,
		MineCount:    r.cfg.MineCount,
		Players:      players,
		Revealed:     revealed,
		RevealedSafe: r.revealedSafe,
		SafeTotal:    r.safeTotal,
		ScorePool:    pool,
		ScoreRate:    r.cfg.ScoreRate,
		IdleMsLeft:   idleLeft,
		Result:       cloneResult(r.result),
	}
}

func (r *Room) broadcastLocked() {
	snap := r.snapshotLocked(time.Now())
	for id, ch := range r.subscribers {
		select {
		case ch <- snap:
		default:
			// Drop stale subscribers rather than blocking the game lock.
			delete(r.subscribers, id)
			close(ch)
		}
	}
}

func (r *Room) bumpLocked() { r.seq++ }

func (r *Room) inBounds(x, y int) bool {
	return x >= 0 && x < r.cfg.Width && y >= 0 && y < r.cfg.Height
}

func (r *Room) index(x, y int) int { return y*r.cfg.Width + x }

func (r *Room) countMinesAround(x, y int) int {
	count := 0
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			if dx == 0 && dy == 0 {
				continue
			}
			nx, ny := x+dx, y+dy
			if r.inBounds(nx, ny) && r.board[r.index(nx, ny)].Mine {
				count++
			}
		}
	}
	return count
}

func (r *Room) otherPlayerIDLocked(playerID string) string {
	for id := range r.players {
		if id != playerID {
			return id
		}
	}
	return ""
}

func comboMultiplier(combo int) float64 {
	m := 1.0 + float64(combo)*0.1
	if m > 1.5 {
		return 1.5
	}
	return m
}

func clonePlayer(p *Player) *Player {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

func cloneResult(r *Result) *Result {
	if r == nil {
		return nil
	}
	cp := *r
	return &cp
}

func secureSeed() int64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UnixNano()
	}
	return int64(binary.LittleEndian.Uint64(b[:]))
}
