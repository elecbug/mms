# MultiMinesweeper Normal MVP

A minimal server-authoritative two-player real-time Minesweeper prototype written in Go.

## Implemented rules

- Two players share one board.
- Only revealed cells are sent to clients.
- Mines are stored only on the server.
- The game starts automatically when two players join the same room.
- A common safe 3x3 center area is revealed at board generation.
- Clicking a mine ends the game immediately.
- Safe clicks collect the current score pool.
- The score pool grows over time.
- Consecutive valid clicks by the same player increase combo multiplier up to 1.5x.
- If nobody clicks for `IDLE_AFTER` seconds, the server reveals one random safe cell.
- Idle auto reveal grants no score and resets the score pool and combos.
- When all safe cells are revealed, the higher score wins.

## Run locally

```bash
go mod tidy
go run ./cmd/server
```

Open two browser windows:

- http://localhost:8080/?room=dev&name=Alice
- http://localhost:8080/?room=dev&name=Bob

The page UI has room/name inputs. Use the same room ID for both players.

## Run with Docker

```bash
docker compose up --build
```

Then open:

```text
http://localhost:8080
```

## Useful environment variables

| Variable | Default | Meaning |
|---|---:|---|
| `ADDR` | `:8080` | HTTP listen address |
| `BOARD_WIDTH` | `12` | Board width |
| `BOARD_HEIGHT` | `12` | Board height |
| `MINE_COUNT` | `25` | Number of mines |
| `SCORE_RATE` | `10` | Score pool growth per second |
| `CELL_BONUS` | `1` | Bonus score per safely opened cell |
| `IDLE_AFTER` | `8` | Idle auto reveal timeout in seconds |

## Current limitations

- No account system.
- No matchmaking.
- No persistent ranking.
- No reconnection identity recovery.
- No private flag/note server storage.
- No hardcore mode yet.
