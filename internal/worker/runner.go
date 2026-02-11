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


	for myI := 0; myI < totalRequests; myI++ {
		go func() {
			

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
