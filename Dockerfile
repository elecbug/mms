# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS build
WORKDIR /src

COPY . .

RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/multiminesweeper ./cmd/server

FROM alpine:3.20
WORKDIR /app

COPY --from=build /out/multiminesweeper /app/multiminesweeper
COPY web /app/web

EXPOSE 8080
ENTRYPOINT ["/app/multiminesweeper"]