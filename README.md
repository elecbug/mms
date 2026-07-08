# MultiMinesweeper Normal Mode

A server-authoritative two-player real-time Minesweeper prototype written in Go.

This version implements only **Normal Mode**. Hardcore mode is intentionally left out.

## Current gameplay rules

- Two players share one board.
- Mines are stored only on the server.
- Clients receive only revealed cells, end-of-game mines, and their own private marks.
- The room waits until two players join and both press **Ready**.
- A common 3x3 safe starting area is revealed when a match starts.
- Clicking a mine ends the game immediately.
- Safe clicks collect the current score pool.
- The score pool grows over time.
- Consecutive valid safe actions by the same player increase combo multiplier up to 1.5x.
- If nobody performs a valid safe action for `IDLE_AFTER` seconds, the server reveals one random safe cell.
- Idle auto reveal grants no score and resets the score pool and combos.
- When all safe cells are revealed, the higher score wins.
- After the game ends, all mines are revealed.

## Added features in this version

- Ready / rematch flow.
- Private per-player marks:
  - `flag`
  - `question`
- Right-click flag cycling in the browser.
- Chord action on revealed numbered cells.
- Server-side prevention of revealing your own flagged cells.
- Server-side event log.
- Reconnection token stored in `localStorage`.
- Disconnect grace timeout. If a player stays disconnected too long during play, they lose.
- End-of-game mine reveal.
- Basic responsive browser UI.
- `.dockerignore` for cleaner Docker builds.

## Run with Docker

```bash
docker compose up --build
```

Then open two browser windows:

```text
http://localhost:8080/?room=dev&name=Alice
http://localhost:8080/?room=dev&name=Bob
```

Both players must click **Ready** before the match starts.

## Run locally

```bash
go mod tidy
go run ./cmd/server
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
| `DISCONNECT_GRACE` | `30` | Disconnect loss timeout in seconds |

## WebSocket client messages

```json
{ "type": "ready", "ready": true }
```

```json
{ "type": "reveal", "x": 3, "y": 4 }
```

```json
{ "type": "mark", "x": 3, "y": 4, "state": "flag" }
```

```json
{ "type": "mark", "x": 3, "y": 4, "state": "question" }
```

```json
{ "type": "mark", "x": 3, "y": 4, "state": "" }
```

```json
{ "type": "chord", "x": 3, "y": 4 }
```

```json
{ "type": "resign" }
```

## Reconnection

The server sends a `playerId` and `token` in the `welcome` message. The browser stores them in `localStorage` per room/name pair.

Reconnect URL shape:

```text
/ws?room=dev&name=Alice&player_id=p1&token=<token>
```

A token is required to reclaim an existing player slot.

## Current limitations

- No account system.
- No random matchmaking.
- No persistent ranking.
- No hardened production origin policy.
- No Redis or multi-instance room distribution.
- No hardcore mode yet.
