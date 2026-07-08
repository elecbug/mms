package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/example/multiminesweeper/internal/game"
	"github.com/example/multiminesweeper/internal/transport"
)

func main() {
	addr := env("ADDR", ":8080")

	manager := game.NewManager(game.Config{
		Width:      envInt("BOARD_WIDTH", 12),
		Height:     envInt("BOARD_HEIGHT", 12),
		MineCount:  envInt("MINE_COUNT", 25),
		ScoreRate:  envFloat("SCORE_RATE", 10.0),
		CellBonus:  envFloat("CELL_BONUS", 1.0),
		IdleAfter:  envDuration("IDLE_AFTER", 8*time.Second),
		MaxPlayers: 2,
	})

	mux := http.NewServeMux()
	transport.RegisterHandlers(mux, manager)

	log.Printf("multiminesweeper server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	var v int
	if _, err := fmtSscanf(os.Getenv(key), "%d", &v); err == nil && v > 0 {
		return v
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	var v float64
	if _, err := fmtSscanf(os.Getenv(key), "%f", &v); err == nil && v > 0 {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	var seconds float64
	if _, err := fmtSscanf(os.Getenv(key), "%f", &seconds); err == nil && seconds > 0 {
		return time.Duration(seconds * float64(time.Second))
	}
	return fallback
}

// fmtSscanf is wrapped to keep the imports in this file explicit and small.
func fmtSscanf(s, format string, a ...any) (int, error) {
	return fmt.Sscanf(s, format, a...)
}
