package game

import "testing"

func TestReadyStartsNormalMatch(t *testing.T) {
	r := NewRoom("test", Config{Width: 12, Height: 12, MineCount: 20, ScoreRate: 10, MaxPlayers: 2})
	p1, err := r.Join("Alice", "", "")
	if err != nil {
		t.Fatalf("join p1: %v", err)
	}
	p2, err := r.Join("Bob", "", "")
	if err != nil {
		t.Fatalf("join p2: %v", err)
	}
	if err := r.SetReady(p1.ID, true); err != nil {
		t.Fatalf("ready p1: %v", err)
	}
	if got := r.SnapshotFor(p1.ID).Phase; got != PhaseWaiting {
		t.Fatalf("phase after one ready = %q, want %q", got, PhaseWaiting)
	}
	if err := r.SetReady(p2.ID, true); err != nil {
		t.Fatalf("ready p2: %v", err)
	}
	snap := r.SnapshotFor(p1.ID)
	if snap.Phase != PhasePlaying {
		t.Fatalf("phase = %q, want %q", snap.Phase, PhasePlaying)
	}
	if snap.RevealedSafe == 0 {
		t.Fatalf("starting safe area was not revealed")
	}
}

func TestPrivateMarksAreViewerScoped(t *testing.T) {
	r := NewRoom("test", Config{Width: 12, Height: 12, MineCount: 20, ScoreRate: 10, MaxPlayers: 2})
	p1, err := r.Join("Alice", "", "")
	if err != nil {
		t.Fatalf("join p1: %v", err)
	}
	p2, err := r.Join("Bob", "", "")
	if err != nil {
		t.Fatalf("join p2: %v", err)
	}
	if err := r.ToggleMark(p1.ID, 0, 0, MarkFlag); err != nil {
		t.Fatalf("mark p1: %v", err)
	}
	if got := len(r.SnapshotFor(p1.ID).Marks); got != 1 {
		t.Fatalf("p1 marks = %d, want 1", got)
	}
	if got := len(r.SnapshotFor(p2.ID).Marks); got != 0 {
		t.Fatalf("p2 marks = %d, want 0", got)
	}
}

func TestReconnectRequiresToken(t *testing.T) {
	r := NewRoom("test", Config{Width: 12, Height: 12, MineCount: 20, ScoreRate: 10, MaxPlayers: 2})
	p1, err := r.Join("Alice", "", "")
	if err != nil {
		t.Fatalf("join p1: %v", err)
	}
	if p1.Token == "" {
		t.Fatalf("join did not return reconnect token")
	}
	if _, err := r.Join("Alice", p1.ID, "bad-token"); err != ErrInvalidToken {
		t.Fatalf("bad token err = %v, want ErrInvalidToken", err)
	}
	p1b, err := r.Join("Alice", p1.ID, p1.Token)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if p1b.ID != p1.ID || p1b.Token != p1.Token {
		t.Fatalf("unexpected reconnect identity")
	}
}
