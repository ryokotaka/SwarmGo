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

# 2. 実行用ステージ（完成品だけを入れる軽量な箱）
FROM alpine:latest

WORKDIR /root/

# ビルドしたバイナリをコピー
COPY --from=builder /app/swarmgo .

# 実行コマンド（docker-compose で command を渡して master / worker を切り替える）
ENTRYPOINT ["./swarmgo"]
