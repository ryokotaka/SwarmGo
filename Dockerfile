FROM golang:alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -o swarmgo ./cmd/swarmgo/

FROM debian:bookworm-slim

WORKDIR /root/

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && update-ca-certificates && rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/swarmgo .

ENTRYPOINT ["./swarmgo"]
