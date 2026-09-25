FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /swarmgo ./cmd/swarmgo/

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && update-ca-certificates && rm -rf /var/lib/apt/lists/* \
    && mkdir /work && chown 65532:65532 /work

# Writable directory for `run -output` and `resilience -output` reports.
WORKDIR /work

COPY --from=builder /swarmgo /usr/local/bin/swarmgo

# Load generation needs no privileges.
USER 65532:65532

ENTRYPOINT ["swarmgo"]
