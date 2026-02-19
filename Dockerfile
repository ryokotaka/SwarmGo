# 1. ビルド用ステージ（コンパイルを行う場所）
FROM golang:alpine AS builder

WORKDIR /app

# 依存関係のダウンロード
COPY go.mod go.sum ./
RUN go mod download

# ソースコードのコピー
COPY . .

# バイナリのビルド (Linux用)
RUN CGO_ENABLED=0 go build -o swarmgo ./cmd/swarmgo/

# 2. 実行用ステージ（安定感抜群のDebian軽量版）
FROM debian:bookworm-slim

WORKDIR /root/

# Debian標準の確実な証明書リストをインストール
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && update-ca-certificates && rm -rf /var/lib/apt/lists/*

# ビルドしたバイナリをコピー
COPY --from=builder /app/swarmgo .

# 実行コマンド
ENTRYPOINT ["./swarmgo"]