# SwarmGo

**SwarmGo** は、Go言語で開発された軽量なHTTP負荷試験ツールです。
シンプルなCLIインターフェースで、大規模な並行リクエストを安定して計測できるように設計しました。

![Demo](demo.gif)

## 🚀 機能

- **Worker Pool アーキテクチャ**: ゴルーチン（Goroutine）の生成数を制御し、数百万リクエスト規模でもメモリ枯渇を起こさない設計にしました。
- **シンプルなCLI**: 複雑な設定ファイル不要で、「URL」「リクエスト総数」「並列数」をフラグで指定するだけ。
- **グレースフルシャットダウン**: `Ctrl+C` での割り込み時も、実行中のリクエスト完了を待ってから安全に終了します。
- **詳細なレポート**: ステータスコードごとの集計、成功/失敗数、合計所要時間を結果として出します。

## 🛠 アーキテクチャ

```mermaid
graph TD
    Init[Start / Flags Parsing] --> Runner
    Runner --> Dispatch[Job Dispatcher]
    
    Dispatch -->|Fill Jobs| JobCh[Job Channel (Buffered)]
    
    subgraph "Worker Pool (Concurrency Limit)"
        JobCh --> W1[Worker 1]
        JobCh --> W2[Worker 2]
        JobCh --> W3[Worker ...]
    end
    
    W1 -->|HTTP GET| Target[Target Service]
    W2 -->|HTTP GET| Target
    W3 -->|HTTP GET| Target
    
    Target -->|Response| W1
    Target -->|Response| W2
    Target -->|Response| W3
    
    W1 -->|Result| ResCh[Result Channel]
    W2 -->|Result| ResCh
    W3 -->|Result| ResCh
    
    ResCh --> Agg[Aggregator (Summary)]
    Agg --> Report[Print Report]
```

## 💡 技術的なハイライト: メモリ枯渇問題の解決

開発当初の実装では、総リクエスト数の数だけゴルーチンを起動していました。100万リクエストのような大規模なテストでは、大量のゴルーチンが待機状態となり、数GBのメモリを消費してプロセスがクラッシュする問題に直面しました。

この問題を解決するため、**Worker Pool パターン**を導入しました。同時実行数の分だけゴルーチンを起動し、それらがジョブキューからタスクを取得して処理する方式に変更しました。これにより、メモリ使用量を数MB〜数十MBに抑えつつ、高速な処理を実現させています。

## 📦 インストール

Go 1.22+ が必要です。

```bash
git clone https://github.com/ryokotaka/SwarmGo.git
cd SwarmGo
go mod tidy
```

## 📖 使い方

ビルドして実行します。

```bash
# ビルド
go build -o swarmgo cmd/worker/main.go

# 実行例: 100回のリクエストを、10並列で実行
./swarmgo -url https://example.com -n 100 -c 10
```

### オプション

| フラグ | 説明 | 必須 | デフォルト |
|--------|------|:----:|------------|
| `-url` | テスト対象のURL (URL to be tested) | ✓ | - |
| `-n` | リクエスト総数 (Total Requests) | ✓ | 0 |
| `-c` | 同時実行数 (Concurrency) | ✓ | 0 |

## 📊 出力例

```
Summary:
  Total Requests: 10
  Success:        10
  Failed:         0
  Total Duration: 116.75ms
--------------------------------------------------
  RPS:            85.65 req/s
  Mean Latency:   23.26ms
--------------------------------------------------
Status codes:
  200: 10
```

## 🗺 今後のロードマップ

- [ ] リアルタイムの進捗バー表示 (TUI)
- [ ] POSTメソッドやカスタムヘッダーのサポート
- [ ] RPS (Requests Per Second) の計測と表示
- [ ] レイテンシ分布 (P50, P90, P99) の計算

## 📜 ライセンス

MIT
