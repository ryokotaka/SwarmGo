package worker

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Result represents the outcome of a single request.
type Result struct {
	StatusCode int // HTTP status code to return
	Duration time.Duration // Duration of the request from start to response completion
	Err error // Failure of the communication itself
}

// Summary represents the results after all requests are completed.
// For performance reasons, only updated from a single goroutine (no locks needed).
type Summary struct {
	Total         int            // Total number of requests executed
	Success       int            // Number of successful requests
	Failed        int            // Number of failed requests
	StatusCodeCnt map[int]int    // Number of requests for each status code (pair of [status code] and [number of requests])
	TotalDuration time.Duration  // Total duration of all requests (used for average calculation)
}

// Runner is the main struct for running the load test.
// It holds an HTTP client.
type Runner struct {
	Client *http.Client
}

// NewRunner creates a new Runner and returns it.
// It sets up the HTTP client.
func NewRunner() *Runner {
	Transport := &http.Transport{
		MaxIdleConns:        100,             // Maximum number of idle connections
		MaxIdleConnsPerHost: 100,             // Maximum number of idle connections per host
		IdleConnTimeout:     90 * time.Second, // Idle connections are closed after 90 seconds
	}
	Client := &http.Client{
		Transport: Transport,
		Timeout:   30 * time.Second, // Request is aborted after 30 seconds
	}
	return &Runner{Client: Client}  // Returns the Runner with the created Client.
}

func (r *MyRunner) MyRun(ctx context.Context, url string, totalRequests, concurrency int) (*MySummary, error) {
	// 引数チェック。回数や同時実行数が 0 以下ならエラーを返す
	if totalRequests <= 0 || concurrency <= 0 {
		return nil, fmt.Errorf("totalRequests and concurrency must be positive, got %d, %d", totalRequests, concurrency)
	}


	// 「同時に動いていい数」を concurrency に制限するためのセマフォ。
	// チャネルに concurrency 個だけ値を入れてく。
	mySem := make(chan struct{}, concurrency)
	for myI := 0; myI < concurrency; myI++ {
		mySem <- struct{}{} // 最初にトークンを concurrency 個入れておく
	}

	// 結果を送る先・受け取る元になる 1 本のチャネルを、事前に 1 回だけ用意（集計は1つの Goroutine だけで行うので競合しない）
	myResults := make(chan MyResult, totalRequests)

	// WaitGroup という型の変数 wg を用意することで Add / Done / Wait が使える
	var myWg sync.WaitGroup
	myWg.Add(totalRequests) // 起動する数だけ Add しておく

	for myI := 0; myI < totalRequests; myI++ {
		go func() {
			defer myWg.Done() // この Goroutine が終わったら「1本終了」と WaitGroup に伝える

			// 事前にキャンセルされてるかどうかを確認し、2つ目の select を実行・評価するコストを削減する。
			select {
			case <-ctx.Done():
				myResults <- MyResult{MyErr: ctx.Err()}
				return
			default:
			}

			// 2つのcaseのうちどちらかを実行する。
			select {
			case <-ctx.Done():
				myResults <- MyResult{MyErr: ctx.Err()}
				return
			case <-mySem:
			}
			defer func() { mySem <- struct{}{} }()


			// 実際に HTTP GET を送る
 			// かかった時間を計るため、リクエスト送信直前の時刻を返す
			myStart := time.Now()

			// 標準: http.NewRequestWithContext で「GET でこの URL、この context で」というリクエストオブジェクトを作る。
			// ちなみにこっちからサーバーに送るリクエスト側のBodyはないということでnilを第四引数に。
			myReq, myErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)

			// リクエストを作る処理のエラー（主に URL まわり）
			if myErr != nil {
				myResults <- MyResult{MyErr: myErr}
				return
			}
			// 標準: *http.Client の Do(myReq) で、そのリクエストを実際に送り、レスポンスが返るまで待つ。
			// ちなみにレスポンスのBodyがここで返ってくる。
			myResp, myErr := r.MyClient.Do(myReq)

			// myStart（リクエスト送信直前）から今（レスポンスが返った直後）までの時間＝この1回のHTTPリクエストにかかった時間 
			myDuration := time.Since(myStart) 

			// 主にネットワークまわり処理のエラー
			if myErr != nil {
				myResults <- MyResult{MyErr: myErr, MyDuration: myDuration}
				return
			}

			// Go では myResp.Body が中身を少しずつ読むためのストリーム（ ReadCloser )なので、この仕組みが裏でネットワークの接続やバッファを使っているため、Bodyは使い終わったら閉じること。
			defer myResp.Body.Close() 
			
			// 結果をチャネルに送る（ステータスコード・かかった時間・エラーなし）
			myResults <- MyResult{
				MyStatusCode: myResp.StatusCode,
				MyDuration:   myDuration,
				MyErr:        nil,
			}
		}()
	}

	// すべての Goroutine が終わったら myResults チャネルを閉じる
	go func() {
		// カウントが 0 になるまでブロック。
		myWg.Wait()
		// チャネルを閉じる。
		close(myResults)
	}()

	// myResults から結果を1件ずつ受け取り、集計する（1つの Goroutine だけが書くので安全）
	mySum := &MySummary{MyStatusCodeCnt: make(map[int]int)} // MyStatusCodeCnt は map なので make が必要
	for myRes := range myResults {
		mySum.MyTotal++                         // 総数に 1 足す
		mySum.MyTotalDuration += myRes.MyDuration   // かかった時間を合計に加える
		if myRes.MyErr != nil {
			mySum.MyFailed++                    // エラーなら失敗数に 1 足して次へ
			continue
		}
		mySum.MySuccess++                       // 成功数に 1 足す
		mySum.MyStatusCodeCnt[myRes.MyStatusCode]++ // そのステータスコードの出現回数を 1 増やす
	}

	return mySum, nil // 集計結果を呼び出し元に返す
}
