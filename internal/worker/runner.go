package worker

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// MyResult represents the outcome of a single request.
type MyResult struct {
	MyStatusCode int // HTTP status code to return
	MyDuration time.Duration // Duration of the request from start to response completion
	MyErr error // Failure of the communication itself
}

// MySummary represents the results after all requests are completed.
// For performance reasons, only updated from a single goroutine (no locks needed).
type MySummary struct {
	MyTotal         int            // Total number of requests executed
	MySuccess       int            // Number of successful requests
	MyFailed        int            // Number of failed requests
	MyStatusCodeCnt map[int]int    // Number of requests for each status code (pair of [status code] and [number of requests])
	MyTotalDuration time.Duration  // Total duration of all requests (used for average calculation)
}

// MyRunner is the main struct for running the load test.
// It holds an HTTP client.
type MyRunner struct {
	MyClient *http.Client
}

// NewMyRunner は MyRunner を1つ作り、返します。
// HTTP クライアントの設定（接続の持ち方・タイムアウト）をここで行っています。
func NewMyRunner() *MyRunner {
	myTransport := &http.Transport{
		MaxIdleConns:        100,             // Maximum number of idle connections
		MaxIdleConnsPerHost: 100,             // Maximum number of idle connections per host
		IdleConnTimeout:     90 * time.Second, // Idle connections are closed after 90 seconds
	}
	myClient := &http.Client{
		Transport: myTransport,
		Timeout:   30 * time.Second, // Request is aborted after 30 seconds
	}
	return &MyRunner{MyClient: myClient}  // Returns the MyRunner with the created MyClient.
}

// MyRun は、指定した URL に totalRequests 回の GET を送り、最大 concurrency 本まで同時実行します。
// Worker Pool パターンで、固定数のワーカーがジョブを取り出して executeRequest を呼びます。
// 終わったら結果を集計した MySummary を返します。ctx がキャンセルされると、未開始のリクエストはやめて終了します。
func (r *MyRunner) MyRun(ctx context.Context, url string, totalRequests, concurrency int) (*MySummary, error) {
	// 引数チェック。回数や同時実行数が 0 以下ならエラーを返す
	if totalRequests <= 0 || concurrency <= 0 {
		return nil, fmt.Errorf("totalRequests and concurrency must be positive, got %d, %d", totalRequests, concurrency)
	}

	// ジョブ投入用チャネル（ワーカーに「1リクエスト分」の仕事を渡す）
	myJobs := make(chan struct{}, totalRequests)
	// 結果を送る先・受け取る元になる 1 本のチャネルを、事前に 1 回だけ用意（集計は1つの Goroutine だけで行うので競合しない）
	myResults := make(chan MyResult, totalRequests)

	// ジョブを totalRequests 個入れてからチャネルを閉じる
	for i := 0; i < totalRequests; i++ {
		myJobs <- struct{}{}
	}
	close(myJobs)

	// WaitGroup という型の変数  myWg を用意することで Add / Done / Wait が使える
	var myWg sync.WaitGroup

	// ワーカーを concurrency 個だけ起動（ループ内には起動処理のみ）のため、すぐ終了する。
	for i := 0; i < concurrency; i++ {
		// これから1本ワーカーを起動するので、終わるまで待つ対象を1つ増やす。
		myWg.Add(1)
		go func() {
			defer myWg.Done() // この Goroutine が終わったら「1本終了」と WaitGroup に伝える。
			for range myJobs {
				// Check for cancellation (e.g. Ctrl+C)
				select {
				case <-ctx.Done():
					myResults <- MyResult{MyErr: ctx.Err()}
					continue
				default:
				}
				// 実際に 1 回分の HTTP を実行し、結果を送る
				myResults <- r.executeRequest(ctx, url)
			}
		}()
	}

	// 全ワーカー終了後に結果チャネルを閉じる（ループの外で1回だけ）
	go func() {
		myWg.Wait() // カウントが 0 になるまでブロック
		close(myResults)
	}()

	// myResults から結果を1件ずつ受け取り、集計する（1つの Goroutine だけが書くので安全）
	mySum := &MySummary{MyStatusCodeCnt: make(map[int]int)} // MyStatusCodeCnt は map なので make が必要
	for res := range myResults {
		mySum.MyTotal++                         // 総数に 1 足す
		mySum.MyTotalDuration += res.MyDuration // かかった時間を合計に加える
		if res.MyErr != nil {
			mySum.MyFailed++ // エラーなら失敗数に 1 足して次へ
			continue
		}
		mySum.MySuccess++                           // 成功数に 1 足す
		mySum.MyStatusCodeCnt[res.MyStatusCode]++ // そのステータスコードの出現回数を 1 増やす
	}
	return mySum, nil // 集計結果を呼び出し元に返す
}


// executeRequest は 1 回分の HTTP GET を実行し、結果を返す。
// 可読性のため、リクエスト作成・送信・レスポンス処理をここに集約している。
func (r *MyRunner) executeRequest(ctx context.Context, url string) MyResult {
	if ctx.Err() != nil {
		return MyResult{MyErr: ctx.Err()}
	}

	// 実際に HTTP GET を送る。かかった時間を計るため、リクエスト送信直前の時刻を取る
	myStart := time.Now()

	// 標準: http.NewRequestWithContext で「GET でこの URL、この context で」というリクエストオブジェクトを作る。
	// リクエスト側の Body はないので第四引数は nil。
	myReq, myErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if myErr != nil {
		return MyResult{MyErr: myErr} // リクエストを作る処理のエラー（主に URL まわり）
	}

	// 標準: *http.Client の Do(myReq) で、そのリクエストを実際に送り、レスポンスが返るまで待つ。
	myResp, myErr := r.MyClient.Do(myReq)
	// myStart（リクエスト送信直前）から今（レスポンスが返った直後）までの時間＝この1回のHTTPリクエストにかかった時間
	myDuration := time.Since(myStart)
	if myErr != nil {
		return MyResult{MyErr: myErr, MyDuration: myDuration} // 主にネットワークまわり処理のエラー
	}

	// Go では myResp.Body が中身を少しずつ読むためのストリーム（ReadCloser）なので、
	// 裏でネットワークの接続やバッファを使っているため、Body は使い終わったら閉じること。
	defer myResp.Body.Close()

	// 結果を返す（ステータスコード・かかった時間・エラーなし）
	return MyResult{
		MyStatusCode: myResp.StatusCode,
		MyDuration:   myDuration,
		MyErr:        nil,
	}
}

