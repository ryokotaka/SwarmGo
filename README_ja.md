<div align="center">

# SwarmGo 

[![Go](https://img.shields.io/badge/Go-1.22+-red?style=for-the-badge&logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue?style=for-the-badge)](./LICENSE)
[![English](https://img.shields.io/badge/README-English-00ADD8?style=for-the-badge)](./README.md)

<br>

Goで実装したHTTP負荷試験ツールです。<br>
Worker Poolで並行数を管理し、リソース（特にメモリ）の使用を抑える構成にしています。

</div>
<br>

![Demo](demo.gif)


## 🚀 機能

<table>
  <tbody>
    <tr>
      <td><strong><span style="color:#ff4d4f;"> ボトルネックを見つけやすい集計</span></strong></td>
      <td>ただ成功・失敗を表示するだけでなく、平均レスポンス時間やステータスコード別の内訳（200 OK, 500 Errorなど）を表示します。</td>
    </tr>
    <tr>
      <td><strong><span style="color:#ff4d4f;"> 1行のコマンドでテスト開始</span></strong></td>
      <td><code>URL / 合計回数 / 同時に送る数</code> を指定するだけで、すぐに負荷テストを実行できます。</td>
    </tr>
    <tr>
      <td><strong><span style="color:#ff4d4f;"> 高並行でもメモリが増えにくい設計</span></strong></td>
      <td>Worker Poolで並行数を制御し、メモリ使用量が増えにくいようにしています。手元のPCの負荷を抑えながらリクエストを送れます。</td>
    </tr>
    <tr>
      <td><strong><span style="color:#ff4d4f;"> 中断しても結果を崩しにくい</span></strong></td>
      <td><code>Ctrl+C</code> で中断しても、進行中のリクエストを処理してから終了します。</td>
    </tr>
  </tbody>
</table>




## 🛠 アーキテクチャ

```mermaid
graph TD
    User((User)) -->|Start Command| Main[Main Process]
    subgraph "SwarmGo Worker Node"
        Main -->|Init| Runner
        Runner -->|Dispatch| JobChannel[Job Channel]
        subgraph "Worker Pool"
            W1[Worker 1]
            W2[Worker 2]
            WX[Worker ...]
        end
        JobChannel --> W1 & W2 & WX
        W1 & W2 & WX -->|HTTP Req| Target[Target Service]
        W1 & W2 & WX -->|Result| ResChannel[Result Channel]
        ResChannel -->|Aggregate| Summary
    end
    Summary -->|Report| Output[Console]
```

## 💡 技術的なハイライト: メモリ枯渇問題の解決

最初頃の実装では、総リクエスト数の数だけゴルーチンを起動していたため、100万リクエストのような大規模なテストでは、大量のゴルーチンが待機状態となり、数GBのメモリを消費しクラッシュしてしまっていました。

そこで、**Worker Pool パターン**を導入し、同時実行数の分だけゴルーチンを起動して、それらがジョブキューからタスクを取得して処理する方式に変更しました。これにより、メモリ使用量を数MB〜数十MBに抑えつつ、高速な処理を可能にしています。

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

# 実行例: 100回のリクエストを、https://example.com に10並列で実行
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

- [ ] リアルタイムの進捗バー表示 
- [ ] POSTメソッドやカスタムヘッダーのサポート
- [ ] RPS の計測と表示
- [ ] レイテンシ分布の計算

## 📜 ライセンス

MIT
