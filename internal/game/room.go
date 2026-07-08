package game

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
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

	MarkFlag     = "flag"
	MarkQuestion = "question"
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
	events  []Event

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
		safeTotal:   cfg.Width*cfg.Height - cfg.MineCount,
	}
	r.logLocked("room", "", "room created")
	go r.idleLoop()
	return r
}

func (r *Room) Join(name, requestedPlayerID, token string) (*Player, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if requestedPlayerID != "" || token != "" {
		p, ok := r.players[requestedPlayerID]
		if !ok || p.Token == "" || p.Token != token {
			return nil, ErrInvalidToken
		}
		if name != "" {
			p.Name = name
		}
		p.Connected = true
		p.DisconnectedAt = time.Time{}
		r.bumpLocked()
		r.logLocked("reconnect", p.ID, fmt.Sprintf("%s reconnected", p.Name))
		r.broadcastLocked()
		return clonePlayerWithToken(p), nil
	}

	if name == "" {
		name = fmt.Sprintf("Player %d", len(r.players)+1)
	}
	if len(r.players) >= r.cfg.MaxPlayers {
		return nil, ErrRoomFull
	}
	id := fmt.Sprintf("p%d", len(r.players)+1)
	p := &Player{
		ID:        id,
		Name:      name,
		Connected: true,
		Token:     newToken(),
		Marks:     make(map[int]string),
	}
	r.players[id] = p
	r.bumpLocked()
	r.logLocked("join", id, fmt.Sprintf("%s joined", name))
	r.broadcastLocked()
	return clonePlayerWithToken(p), nil
}

func (r *Room) Subscribe(playerID string) (<-chan Snapshot, func()) {
	ch := make(chan Snapshot, 16)
	r.mu.Lock()
	r.subscribers[playerID] = ch
	ch <- r.snapshotLocked(time.Now(), playerID)
	r.mu.Unlock()

	cancel := func() {
		r.mu.Lock()
		if current, ok := r.subscribers[playerID]; ok && current == ch {
			delete(r.subscribers, playerID)
			close(ch)
			if p, ok := r.players[playerID]; ok {
				p.Connected = false
				p.DisconnectedAt = time.Now()
				r.logLocked("disconnect", playerID, fmt.Sprintf("%s disconnected", p.Name))
			}
			r.bumpLocked()
			r.broadcastLocked()
		}
		r.mu.Unlock()
	}
	return ch, cancel
}

func (r *Room) SetReady(playerID string, ready bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.players[playerID]
	if !ok {
		return fmt.Errorf("unknown player")
	}
	if r.phase == PhasePlaying {
		return fmt.Errorf("game is already playing")
	}
	p.Ready = ready
	if ready {
		r.logLocked("ready", playerID, fmt.Sprintf("%s is ready", p.Name))
	} else {
		r.logLocked("unready", playerID, fmt.Sprintf("%s is not ready", p.Name))
	}

	if r.canStartLocked() {
		r.startLocked()
	}

	r.bumpLocked()
	r.broadcastLocked()
	return nil
}

func (r *Room) ToggleMark(playerID string, x, y int, state string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.players[playerID]
	if !ok {
		return fmt.Errorf("unknown player")
	}
	if !r.inBounds(x, y) {
		return fmt.Errorf("cell is out of bounds")
	}
	idx := r.index(x, y)
	if len(r.board) > 0 && r.board[idx].Revealed {
		return fmt.Errorf("cannot mark a revealed cell")
	}
	if p.Marks == nil {
		p.Marks = make(map[int]string)
	}
	switch state {
	case "":
		delete(p.Marks, idx)
	case MarkFlag, MarkQuestion:
		p.Marks[idx] = state
	default:
		return fmt.Errorf("invalid mark state")
	}

	r.bumpLocked()
	r.broadcastLocked()
	return nil
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
	if !p.Connected {
		return fmt.Errorf("player is disconnected")
	}
	if !r.inBounds(x, y) {
		p.InvalidActs++
		return fmt.Errorf("cell is out of bounds")
	}
	idx := r.index(x, y)
	if p.Marks != nil && p.Marks[idx] == MarkFlag {
		p.InvalidActs++
		return fmt.Errorf("cell is flagged")
	}
	cell := &r.board[idx]
	if cell.Revealed {
		p.InvalidActs++
		return fmt.Errorf("cell is already revealed")
	}

	r.accrueScorePoolLocked(now)

	if cell.Mine {
		r.loseByMineLocked(playerID, idx)
		r.bumpLocked()
		r.broadcastLocked()
		return nil
	}

	opened := r.revealFloodLocked(x, y, playerID)
	r.applyScoreLocked(p, opened, now, "reveal")

	if r.revealedSafe >= r.safeTotal {
		r.endByScoreLocked()
	}

	r.bumpLocked()
	r.broadcastLocked()
	return nil
}

func (r *Room) Chord(playerID string, x, y int) error {
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
		p.InvalidActs++
		return fmt.Errorf("cell is out of bounds")
	}
	idx := r.index(x, y)
	base := r.board[idx]
	if !base.Revealed || base.Mine || base.Value <= 0 {
		p.InvalidActs++
		return fmt.Errorf("chord requires a revealed numbered cell")
	}
	flagCount := r.countPlayerFlagsAroundLocked(p, x, y)
	if flagCount != base.Value {
		p.InvalidActs++
		return fmt.Errorf("flag count does not match cell number")
	}

	r.accrueScorePoolLocked(now)

	opened := 0
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			if dx == 0 && dy == 0 {
				continue
			}
			nx, ny := x+dx, y+dy
			if !r.inBounds(nx, ny) {
				continue
			}
			nidx := r.index(nx, ny)
			if r.board[nidx].Revealed || p.Marks[nidx] == MarkFlag {
				continue
			}
			if r.board[nidx].Mine {
				r.loseByMineLocked(playerID, nidx)
				r.bumpLocked()
				r.broadcastLocked()
				return nil
			}
			opened += r.revealFloodLocked(nx, ny, playerID)
		}
	}
	if opened == 0 {
		p.InvalidActs++
		return fmt.Errorf("no cells opened")
	}

	r.applyScoreLocked(p, opened, now, "chord")
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
	r.logLocked("end", playerID, "player resigned")
	r.bumpLocked()
	r.broadcastLocked()
}

func (r *Room) SnapshotFor(playerID string) Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked(time.Now(), playerID)
}

func (r *Room) canStartLocked() bool {
	if r.phase == PhasePlaying || len(r.players) != r.cfg.MaxPlayers {
		return false
	}
	for _, p := range r.players {
		if !p.Connected || !p.Ready {
			return false
		}
	}
	return true
}

func (r *Room) startLocked() {
	now := time.Now()
	r.phase = PhasePlaying
	r.result = nil
	r.events = nil
	r.scorePool = 0
	r.lastScoreTime = now
	r.lastValidAction = now
	r.lastScoringPlayer = ""
	r.generateBoardLocked()
	for _, p := range r.players {
		p.Score = 0
		p.Combo = 0
		p.Ready = false
		p.OpenedSafe = 0
		p.SafeClicks = 0
		p.InvalidActs = 0
		p.Marks = make(map[int]string)
	}
	r.logLocked("start", "", "normal game started")
}

func (r *Room) idleLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.handlePeriodicChecks()
		case <-r.stopTicker:
			return
		}
	}
}

func (r *Room) handlePeriodicChecks() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	if r.phase != PhasePlaying {
		return
	}
	if r.handleDisconnectLossLocked(now) {
		r.bumpLocked()
		r.broadcastLocked()
		return
	}
	if r.cfg.IdleAfter <= 0 || now.Sub(r.lastValidAction) < r.cfg.IdleAfter {
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
	r.clearMarksLocked(idx)

	// Waiting must not become a dominant strategy.
	r.scorePool = 0
	r.lastScoreTime = now
	r.lastValidAction = now
	r.lastScoringPlayer = ""
	for _, p := range r.players {
		p.Combo = 0
	}
	r.logLocked("auto", "", "idle timeout revealed one safe cell")

	if r.revealedSafe >= r.safeTotal {
		r.endByScoreLocked()
	}

	r.bumpLocked()
	r.broadcastLocked()
}

func (r *Room) handleDisconnectLossLocked(now time.Time) bool {
	if r.cfg.DisconnectGrace <= 0 {
		return false
	}
	for _, p := range r.players {
		if p.Connected || p.DisconnectedAt.IsZero() {
			continue
		}
		if now.Sub(p.DisconnectedAt) >= r.cfg.DisconnectGrace {
			winnerID := r.otherPlayerIDLocked(p.ID)
			r.result = &Result{WinnerID: winnerID, LoserID: p.ID, Reason: "disconnect", Message: fmt.Sprintf("%s disconnected too long", p.Name)}
			r.phase = PhaseEnded
			r.logLocked("end", p.ID, r.result.Message)
			return true
		}
	}
	return false
}

func (r *Room) generateBoardLocked() {
	r.board = make([]Cell, r.cfg.Width*r.cfg.Height)
	r.safeTotal = r.cfg.Width*r.cfg.Height - r.cfg.MineCount
	r.revealedSafe = 0

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
		r.clearMarksLocked(idx)
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

func (r *Room) applyScoreLocked(p *Player, opened int, now time.Time, action string) {
	multiplier := comboMultiplier(p.Combo)
	gain := r.scorePool*multiplier + float64(opened)*r.cfg.CellBonus
	p.Score += gain
	p.OpenedSafe += opened
	p.SafeClicks++

	if r.lastScoringPlayer == p.ID {
		p.Combo++
	} else {
		p.Combo = 1
	}
	for id, other := range r.players {
		if id != p.ID {
			other.Combo = 0
		}
	}

	r.scorePool = 0
	r.lastScoreTime = now
	r.lastValidAction = now
	r.lastScoringPlayer = p.ID
	r.logLocked(action, p.ID, fmt.Sprintf("%s opened %d safe cell(s) and gained %.1f", p.Name, opened, gain))
}

func (r *Room) loseByMineLocked(playerID string, idx int) {
	p := r.players[playerID]
	r.board[idx].Revealed = true
	r.board[idx].OpenedBy = playerID
	r.clearMarksLocked(idx)
	winnerID := r.otherPlayerIDLocked(playerID)
	name := playerID
	if p != nil {
		name = p.Name
	}
	r.result = &Result{
		WinnerID: winnerID,
		LoserID:  playerID,
		Reason:   "mine",
		Message:  fmt.Sprintf("%s clicked a mine", name),
	}
	r.phase = PhaseEnded
	r.logLocked("end", playerID, r.result.Message)
}

func (r *Room) endByScoreLocked() {
	players := make([]*Player, 0, len(r.players))
	for _, p := range r.players {
		players = append(players, p)
	}
	sort.Slice(players, func(i, j int) bool { return players[i].ID < players[j].ID })
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
	r.logLocked("end", r.result.WinnerID, r.result.Message)
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

func (r *Room) snapshotLocked(now time.Time, viewerID string) Snapshot {
	pool := r.scorePool
	if r.phase == PhasePlaying && !r.lastScoreTime.IsZero() {
		pool += now.Sub(r.lastScoreTime).Seconds() * r.cfg.ScoreRate
	}

	players := make([]*Player, 0, len(r.players))
	for _, p := range r.players {
		players = append(players, clonePlayer(p))
	}
	sort.Slice(players, func(i, j int) bool { return players[i].ID < players[j].ID })

	revealed := make([]PublicCell, 0, r.revealedSafe+r.cfg.MineCount)
	if len(r.board) > 0 {
		for y := 0; y < r.cfg.Height; y++ {
			for x := 0; x < r.cfg.Width; x++ {
				c := r.board[r.index(x, y)]
				if c.Revealed || (r.phase == PhaseEnded && c.Mine) {
					value := c.Value
					openedBy := c.OpenedBy
					if c.Mine {
						value = -1
						if openedBy == "" {
							openedBy = "mine"
						}
					}
					revealed = append(revealed, PublicCell{X: x, Y: y, Value: value, OpenedBy: openedBy})
				}
			}
		}
	}

	marks := make([]PublicMark, 0)
	if p, ok := r.players[viewerID]; ok && p.Marks != nil {
		for idx, state := range p.Marks {
			if state == "" {
				continue
			}
			x := idx % r.cfg.Width
			y := idx / r.cfg.Width
			marks = append(marks, PublicMark{X: x, Y: y, State: state})
		}
		sort.Slice(marks, func(i, j int) bool {
			if marks[i].Y == marks[j].Y {
				return marks[i].X < marks[j].X
			}
			return marks[i].Y < marks[j].Y
		})
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
		Marks:        marks,
		RevealedSafe: r.revealedSafe,
		SafeTotal:    r.safeTotal,
		ScorePool:    pool,
		ScoreRate:    r.cfg.ScoreRate,
		IdleMsLeft:   idleLeft,
		Result:       cloneResult(r.result),
		Events:       cloneEvents(r.events),
	}
}

func (r *Room) broadcastLocked() {
	now := time.Now()
	for id, ch := range r.subscribers {
		snap := r.snapshotLocked(now, id)
		select {
		case ch <- snap:
		default:
			// Drop stale subscribers rather than blocking the game lock.
			delete(r.subscribers, id)
			close(ch)
		}
	}
}

func (r *Room) clearMarksLocked(idx int) {
	for _, p := range r.players {
		delete(p.Marks, idx)
	}
}

func (r *Room) countPlayerFlagsAroundLocked(p *Player, x, y int) int {
	count := 0
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			if dx == 0 && dy == 0 {
				continue
			}
			nx, ny := x+dx, y+dy
			if r.inBounds(nx, ny) && p.Marks[r.index(nx, ny)] == MarkFlag {
				count++
			}
		}
	}
	return count
}

func (r *Room) logLocked(kind, playerID, message string) {
	r.events = append(r.events, Event{Seq: r.seq, AtUnixMs: time.Now().UnixMilli(), Type: kind, PlayerID: playerID, Message: message})
	if len(r.events) > 12 {
		r.events = r.events[len(r.events)-12:]
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
	cp.Token = ""
	cp.Marks = nil
	cp.DisconnectedAt = time.Time{}
	return &cp
}

func clonePlayerWithToken(p *Player) *Player {
	if p == nil {
		return nil
	}
	cp := *p
	cp.Marks = nil
	cp.DisconnectedAt = time.Time{}
	return &cp
}

func cloneResult(r *Result) *Result {
	if r == nil {
		return nil
	}
	cp := *r
	return &cp
}

func cloneEvents(events []Event) []Event {
	out := make([]Event, len(events))
	copy(out, events)
	return out
}

func secureSeed() int64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UnixNano()
	}
	return int64(binary.LittleEndian.Uint64(b[:]))
}

func newToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
