<div align="center">

# **SwarmGo**

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev/)
[![gRPC](https://img.shields.io/badge/gRPC-1.0+-244C5A?style=for-the-badge&logo=grpc&logoColor=white)](https://grpc.io/)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED?style=for-the-badge&logo=docker&logoColor=white)](https://www.docker.com/)
[![License](https://img.shields.io/badge/License-MIT-blue?style=for-the-badge)](./LICENSE)
[![README English](https://img.shields.io/badge/README-English-00ADD8?style=for-the-badge)](./README.md)

<br>

**分散型の HTTP 負荷テストツール**です。1 台の **Master** が **gRPC** で複数の **Worker** に指示を出し、各 Worker は **Worker Pool** で並行数を抑えつつ HTTP リクエストを送ります。高負荷でもメモリ不足にならないような設計を心がけました。

*分散システムや **goroutine**、**Docker** を実際に書いて理解するため、ゼロから作ってみたプロジェクトです。*

<br>

---

</div>

### 🚀 デモ

以下のコマンドで起動し、Masterノードにアタッチしてください。  
`s` キーで試験開始、`q` キーで終了します。

`docker compose up -d --build`

表示項目:
- 🔄 進捗状況: テストの実行状態をリアルタイムに表示
- 👥 ワーカー数 / RPS: 稼働中のワーカー数と秒間リクエスト数
- ⏱️ レイテンシ: P50, P90, P99 レスポンス時間
- ✅ 成功/失敗: リクエスト結果のカウント
- ⚠️ 主要なエラー: 発生している主なエラー内容の要約

<div align="center">

![SwarmGo デモ（Docker Compose クイックスタート）](demo-docker.gif)

</div>

---

## 📖 このプロジェクトでやっていること

**SwarmGo** は、**Master** 1 台と **Worker** 複数台で「分散して」HTTP 負荷テストをするツールです。

- **Master**: **gRPC** サーバーと、オプションで TUI ダッシュボード。Start / Stop / Quit などのコマンドを Worker に送ります。
- **Worker**: Master に接続し、指定された URL に HTTP GET を打ち、RPS や成功/失敗数・完了イベント/上位エラー状況をストリームで返します。

Worker を増やせばその分スケールするので、1 プロセスで何百万もリクエストを抱え込む必要なし。

---

## 📦 主な機能

| 機能 | 説明 |
|--------|-------------|
| **Master + Workers（gRPC）** | 1 台の Master が複数 Worker に指示を出し、Worker を増やしてスケールできます。 |
| **TUI ダッシュボード** | 接続 Worker 数・ライブ RPS・成功/失敗数・**エラー要因の上位表示**（例: `HTTP 500 ...: 155`）・イベントログを表示。**`s`** でテスト開始、**`q`** で終了。 |
| **失敗 = 通信エラー + HTTP 4xx/5xx** | 往復でエラーになった場合に加え、**ステータスコード 400 以上**（4xx/5xx）も失敗としてカウントします。Go の `Client.Do()` は 5xx でも err を返さないため、明示的にステータスをチェックしています。 |
| **エラー要因（Error Reasons）** | Worker が失敗理由（例: `HTTP 500 Internal Server Error`、`connection refused`、`timeout`）を集計して Master に送り、TUI で**件数上位 5 件**を表示。長いメッセージは切り詰め、失敗が 0 件のときは「Errors: None」と表示します。 |
| **ターゲット URL・n・c の指定** | **`-url`** **`-n`** **`-c`** または環境変数 **`TARGET_URL`** **`TOTAL_REQUESTS`** **`CONCURRENCY`** で指定可能（フラグ省略時は環境変数がデフォルト。Docker Compose で便利）。 |
| **Worker Pool** | 各 Worker 内で固定サイズのプールを使い、並行数を抑えることでメモリを安定させています（大量 **goroutine** による OOM を避けるため）。 |
| **レイテンシ百分位** | 各 Worker が成功リクエストのみで P50/P90/P99 を計算して報告し、TUI で代表値を表示します。 |
| **ヘッドレス Master** | `-no-tui` で **gRPC** だけの Master にでき、CI やスクリプト・リモートサーバーから叩く用途向けです。 |


---

## 🚀 動かし方（クイックスタート）

手軽に試すなら **Docker Compose** がおすすめです。**Go** を入れていなくても、1 コマンドで Master と複数 Worker が立ち上がります。

### Step 1: クローンしてディレクトリへ

```bash
git clone https://github.com/ryokotaka/SwarmGo.git
cd SwarmGo
```

### Step 2: Master と Worker をバックグラウンドで起動

```bash
docker compose up -d --build
```

- **Master**: TUI 付きで起動し、**gRPC** はポート `50051` で待ち受けます。
- **Workers**: デフォルトで 3 台起動し、**Docker** ネットワーク経由で `master:50051` に接続します。

Worker を 5 台にしたい場合などは、次のようにします。

```bash
docker compose up -d --build --scale worker=5
```

TUI で **`s`** を押したときのターゲット URL やリクエスト数・並行数を変えたい場合は、環境変数で指定できます。Master が **`TARGET_URL`**・**`TOTAL_REQUESTS`**・**`CONCURRENCY`** をデフォルトとして読むので、**いちばん手軽なのはプロジェクトルートに `.env` を置くこと**です。そうすれば普段どおり `docker compose up -d --build` だけで可能です。

```bash
# .env の例（プロジェクトルートに .env を作成）
TARGET_URL=https://your-api.example.com
TOTAL_REQUESTS=100
CONCURRENCY=10
```

ファイルを編集したくないときだけ、その場で上書きも可能です。:  
`TARGET_URL=https://your-api.example.com TOTAL_REQUESTS=100 CONCURRENCY=10 docker compose up -d --build`

### Step 3: Master にアタッチして TUI を触る

Master はバックグラウンドで動いているので、TUI を操作するには次のコマンドでアタッチします。

```bash
docker attach $(docker compose ps -q master)
```

- **`s`** で負荷テスト開始（Worker が動いて統計を返してきます）。
- **`q`** で Master 終了（Worker に Quit を送ってから終了）。
- コンテナは動かしたままデタッチしたいときは `Ctrl+P` のあと `Ctrl+Q`。あとからまた `docker attach` できます。

### Step 4: 全部止める

```bash
docker compose down
```

### オプション: フォアグラウンドで起動する

ログを 1 つのターミナルにまとめて見て、Ctrl+C で止めたい場合は次のようにします。

```bash
docker compose up --build
```

このやり方だと TUI の対話はできないので、TUI を触りたいときは上記のアタッチ方法を使ってください。

---

## 🛠 Docker を使わずに動かす（ローカルで Go ビルド）

**Go 1.22+** が必要です。

```bash
go mod tidy
go build -o swarmgo ./cmd/swarmgo/
```

1. **ターミナル 1 — Master**  
   `./swarmgo master -p 50051`  
   任意で `-url`・`-n`・`-c` を指定すると、TUI で **`s`** を押したときの負荷テストに使われます。省略時は環境変数 **`TARGET_URL`**・**`TOTAL_REQUESTS`**・**`CONCURRENCY`** がデフォルト（未設定時は `https://example.com`、`5`、`1`）です。  
 
2. **ターミナル 2 以降 — Worker**  
   `./swarmgo worker`  
   デフォルトでは `localhost:50051` に接続します。別ホストのときは `-addr host:port` や環境変数 `MASTER_ADDR` で指定できます。

3. Master の TUI で **`s`** でテスト開始、**`q`** で終了です。

---

## 🏗 アーキテクチャ

構成はこんなイメージです。

1. **Master** は **gRPC** サーバー（と TUI）を動かし、接続してきた Worker のリストを保持します。各 Worker とは 1 本の **双方向 gRPC ストリーム**でつながっています。
2. **Worker** は Master に接続したあと、まず ID 付きの **Register** を送り、あとはループでコマンド（Start / Stop / Quit）を受け取り、実行中の **Stats** や完了時の **Finish** を送り返します。
3. TUI で **Start** を押すと、Master が接続中の全 Worker に Start を送ります。各 Worker は **固定サイズの Worker Pool** で対象 URL に HTTP GET を実行し、**Stats**（成功/失敗数・RPS・**エラー要因**）を定期的に Master に送り、終わったら **Finish** を送ってダッシュボードを更新します。失敗がある場合は TUI に**エラー要因の上位**（例: HTTP 5xx、connection refused）が表示されます。

HTTP のトラフィックは各 Worker から **対象 URL** に直接向かうだけで、Master はリクエストの中身を見ません。

```mermaid
flowchart LR
    subgraph User
        TUI[TUI: s / q キー]
    end
    subgraph Master
        M[Master gRPC サーバー]
    end
    subgraph Workers
        W1[Worker 1]
        W2[Worker 2]
        WN[Worker N]
    end
    subgraph Target
        URL[対象 URL]
    end
    TUI -->|start/quit| M
    M <-->|gRPC ストリーム: コマンド & 統計| W1
    M <-->|gRPC ストリーム| W2
    M <-->|gRPC ストリーム| WN
    W1 & W2 & WN -->|HTTP GET| URL
```

- **実線:** **gRPC**（Master ↔ Workers）と HTTP（Workers → 対象 URL）。
- **Master** は Worker ごとに 1 本のストリームを持ち、Start/Stop/Quit を全員に送ります。
- **Workers** は最初に **Register** を 1 回送り、以降は同じストリームで **Stats** と **Finish** を送ります。

---

## 🧠 設計で悩んだところと解決の方向性

作っていく中で、並行処理・**gRPC**・デプロイまわりで学んだことの要点だけまとめます。

### 1. Goroutine とメモリ（OOM）

**困ったこと:** 最初は「1 リクエスト 1 **goroutine**」で書いていました。小さい負荷なら問題ないのですが、100 万リクエストなどにすると、goroutine の数とスタックでメモリが一気に食われてクラッシュする可能性がありました。

**やったこと:** **Worker Pool** に切り替えました。リクエスト数 N に対して N 個の goroutine を立てるのではなく、並行数ぶんだけ worker **goroutine** を固定で持ち、**バッファ付き channel** をジョブキューにします。1 つの goroutine がジョブを投入し、worker たちがチャネルから取り出して HTTP を実行。メモリは **O(並行数)** に抑えられ、総リクエスト数が増えても安定するようにしました。

### 2. gRPC の接続とストリーミング

**困ったこと:** Master がコマンドを送り、Worker が統計や完了イベントを返す必要があり、お互いにブロックしない形にしく、また単純なリクエスト/レスポンスだと双方向のリアルタイム感が出ませんでした。

**やったこと:** 1 本の **双方向ストリーミング** RPC（`Connect`）にしました。Worker 1 台につき 1 本の長生きストリームで、Worker は `WorkerMsg`（register / stats / finish）を送り、`MasterCmd`（start / stop / quit）を受け取ります。接続のライフサイクルも「最初のメッセージは **Register**」「ストリームが閉いたらリストから削除」と決めておきました。

### 3. Worker リストの並行アクセス

**困ったこと:** Master は `map[workerID]stream` を持っていて、複数 **goroutine** から読まれたり（TUI、ブロードキャスト）書かれたり（Connect ハンドラ）します。何も考えないと race やパニックの原因になります。

**やったこと:** **mutex** で map を守りつつ、ブロードキャストのときは `stream.Send()` を**ロックを握ったまま**呼ばないようにしました。ロックを取って **スナップショット**（キーとストリームのコピー）だけ取り、ロックを外してからスナップショットに対して Send するようにして、デッドロックや長時間ロックを避けています。

### 4. 同じプロセスで TUI と gRPC を動かす

**困ったこと:** Master は **gRPC** サーバーと TUI の両方を動かすので、gRPC の処理でメインループがブロックすると UI が固まってしまいました。

**やったこと:** **gRPC** の各 `Connect` は別 **goroutine** で動かし、TUI はメイン goroutine で **channel** 経由で更新を受け取る形にしました。gRPC ハンドラは「worker 接続」「stats 更新」などのイベントをチャネルに **非ブロック**（`select` の `default`）で送り、TUI がそれを読んで再描画。UI が応答し続けるようにしています。

### 5. Docker で TUI と複数 Worker を一発で

**困ったこと:** **Go** を入れていない環境でも、1 コマンドで TUI 付き Master と複数 Worker を動かしたかった。

**やったこと:** `docker-compose` で **master** サービス（対話用に `stdin_open` と `tty`）と **worker** サービス（`deploy.replicas: 3`）を定義しました。master は `command: ["master", "-p", "50051"]` のみで起動し、ターゲット URL・リクエスト数・並行数はサービス側の **`environment`** で **`TARGET_URL`**・**`TOTAL_REQUESTS`**・**`CONCURRENCY`** を渡し、Go 側でフラグのデフォルトとして読む設計にしました（compose をシンプルに保ちつつ、アプリで環境変数を扱う形）。Worker は `MASTER_ADDR=master:50051` でサービス名で Master に届くようにしています。TUI を触るときは `docker compose up -d` のあと、master コンテナに `docker attach` して **`s`** / **`q`** を打つ流れにしました。

### 6. コンテナOSの選定とTLS証明書の壁

**困ったこと:** Worker コンテナを極小の **`alpine`** で動かした際、ルート証明書が欠落しており HTTPS 通信で **`x509`** エラーが発生しました。

**やったこと:** 一時的に `INSECURE_SKIP_VERIFY=1` で検証スキップして回避したあと、本番稼働を見据えて証明書周りが手堅い **`debian-slim`** ベースへ移行することで根本解決しました。インフラの選定が分散システムの動作に直結することを学びました。


---

## 📜 ライセンス

**MIT**
