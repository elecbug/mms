.PHONY: run test fmt docker-up docker-down docker-build clean

run:
	go run ./cmd/server

test:
	go test ./...

fmt:
	gofmt -w cmd internal

docker-build:
	docker compose build

docker-up:
	docker compose up --build

docker-down:
	docker compose down

clean:
	rm -f multiminesweeper
