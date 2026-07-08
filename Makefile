.PHONY: run test fmt docker-up docker-down docker-build

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
