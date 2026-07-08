package transport

import (
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/elecbug/mms/internal/game"
	"github.com/gorilla/websocket"
)

type incomingMessage struct {
	Type  string `json:"type"`
	X     int    `json:"x,omitempty"`
	Y     int    `json:"y,omitempty"`
	State string `json:"state,omitempty"`
	Ready *bool  `json:"ready,omitempty"`
}

type outgoingMessage struct {
	Type     string         `json:"type"`
	PlayerID string         `json:"playerId,omitempty"`
	Token    string         `json:"token,omitempty"`
	State    *game.Snapshot `json:"state,omitempty"`
	Error    string         `json:"error,omitempty"`
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// For local development. In production, restrict this to known origins.
		return true
	},
}

func RegisterHandlers(mux *http.ServeMux, manager *game.Manager) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleWebSocket(manager, w, r)
	})

	webRoot := filepath.Join("web")
	mux.Handle("/", http.FileServer(http.Dir(webRoot)))
}

func handleWebSocket(manager *game.Manager, w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("room")
	name := r.URL.Query().Get("name")
	playerID := r.URL.Query().Get("player_id")
	token := r.URL.Query().Get("token")
	if roomID == "" {
		roomID = "dev"
	}

	room, player, err := manager.JoinRoom(roomID, name, playerID, token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	sub, cancel := room.Subscribe(player.ID)
	defer cancel()

	out := make(chan outgoingMessage, 32)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for msg := range out {
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
		}
	}()

	out <- outgoingMessage{Type: "welcome", PlayerID: player.ID, Token: player.Token}
	snap := room.SnapshotFor(player.ID)
	out <- outgoingMessage{Type: "state", State: &snap}

	forwardDone := make(chan struct{})
	go func() {
		defer close(forwardDone)
		for snap := range sub {
			select {
			case out <- outgoingMessage{Type: "state", State: &snap}:
			case <-done:
				return
			}
		}
	}()

	conn.SetReadLimit(2048)
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	})

	go pingLoop(conn, done)

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var msg incomingMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			safeSend(out, outgoingMessage{Type: "error", Error: "invalid JSON"}, done)
			continue
		}

		switch msg.Type {
		case "ready":
			ready := true
			if msg.Ready != nil {
				ready = *msg.Ready
			}
			if err := room.SetReady(player.ID, ready); err != nil {
				safeSend(out, outgoingMessage{Type: "error", Error: err.Error()}, done)
			}
		case "mark":
			if err := room.ToggleMark(player.ID, msg.X, msg.Y, msg.State); err != nil {
				safeSend(out, outgoingMessage{Type: "error", Error: err.Error()}, done)
			}
		case "reveal":
			if err := room.Reveal(player.ID, msg.X, msg.Y); err != nil {
				safeSend(out, outgoingMessage{Type: "error", Error: err.Error()}, done)
			}
		case "chord":
			if err := room.Chord(player.ID, msg.X, msg.Y); err != nil {
				safeSend(out, outgoingMessage{Type: "error", Error: err.Error()}, done)
			}
		case "resign":
			room.Resign(player.ID)
		case "ping":
			safeSend(out, outgoingMessage{Type: "pong"}, done)
		default:
			safeSend(out, outgoingMessage{Type: "error", Error: "unknown message type"}, done)
		}
	}

	cancel()
	<-forwardDone
	close(out)
	<-done
}

func pingLoop(conn *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
		case <-done:
			return
		}
	}
}

func safeSend(out chan<- outgoingMessage, msg outgoingMessage, done <-chan struct{}) {
	select {
	case out <- msg:
	case <-done:
	default:
	}
}
