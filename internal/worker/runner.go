package worker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// MyResult represents the outcome of a single request.
type MyResult struct {
	MyStatusCode int           // HTTP status code to return
	MyDuration   time.Duration // Duration of the request from start to response completion
	MyErr        error         // Failure of the communication itself
}

// MySummary represents the results after all requests are completed.
// For performance reasons, only updated from a single goroutine (no locks needed).
type MySummary struct {
	MyTotal         int               // Total number of requests executed
	MySuccess       int               // Number of successful requests
	MyFailed        int               // Number of failed requests
	MyFirstErr      error             // First error encountered (for logging when MyFailed > 0)
	MyStatusCodeCnt map[int]int       // Number of requests for each status code (pair of [status code] and [number of requests])
	MyErrorReasons  map[string]int    // 失敗要因ごとの発生回数（TUI で上位表示するため Master へ送る）
	MyTotalDuration time.Duration     // Total duration of all requests (used for average calculation)
	LatencyP50      time.Duration     // 50th percentile latency (successful requests only)
	LatencyP90      time.Duration     // 90th percentile latency (successful requests only)
	LatencyP99      time.Duration     // 99th percentile latency (successful requests only)
}

// MyRunner is the main struct for running the load test.
// It holds an HTTP client.
type MyRunner struct {
	MyClient *http.Client
}

// loadRootCAs tries to load CA certs from SSL_CERT_FILE, then common paths.
// Returns nil if none found (Go will use default); used when CGO_ENABLED=0 and OS path discovery fails.
func loadRootCAs() *x509.CertPool {
	candidates := []string{
		os.Getenv("SSL_CERT_FILE"),
		"/etc/ssl/certs/ca-certificates.crt", // Alpine (Debian style)
		"/etc/ssl/cert.pem",                 // Alpine alternative
	}
	for _, path := range candidates {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM(data) {
			return pool
		}
	}
	return nil
}

// NewMyRunner creates and returns a single MyRunner.
// HTTP client settings (connection pooling and timeouts) are configured here.
// If INSECURE_SKIP_VERIFY=1 or true, TLS certificate verification is skipped (Docker/dev use).
// Otherwise, RootCAs are loaded explicitly from SSL_CERT_FILE or common paths so that static Go binaries (CGO_ENABLED=0) on Alpine find the CA bundle.
func NewMyRunner() *MyRunner {
	tlsInsecure := os.Getenv("INSECURE_SKIP_VERIFY") == "1" || os.Getenv("INSECURE_SKIP_VERIFY") == "true"
	tlsConfig := &tls.Config{
		InsecureSkipVerify: tlsInsecure,
	}
	if !tlsInsecure {
		if pool := loadRootCAs(); pool != nil {
			tlsConfig.RootCAs = pool
		}
	}
	myTransport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig:     tlsConfig,
	}
	myClient := &http.Client{
		Transport: myTransport,
		Timeout:   30 * time.Second,
	}
	return &MyRunner{MyClient: myClient}
}

// OnProgressFunc is called periodically during MyRun with current completed/success/failed counts and elapsed time.
// Optional; pass nil to disable. May be called from the aggregation goroutine; implementors should not block (e.g. send to channel only).
type OnProgressFunc func(completed, success, failed int, elapsed time.Duration)

// MyRun sends totalRequests GET requests to the given URL, with up to concurrency concurrent executions.
// Uses a worker pool: a fixed number of workers take jobs and call executeRequest.
// Returns an aggregated MySummary when done. If ctx is cancelled, unstarted requests are skipped and the run exits.
// If onProgress is non-nil, it is called periodically (every progressIntervalCount results or progressIntervalTime) with current progress.
func (r *MyRunner) MyRun(ctx context.Context, url string, totalRequests, concurrency int, onProgress OnProgressFunc) (*MySummary, error) {
	// Argument check: return error if count or concurrency is zero or less.
	if totalRequests <= 0 || concurrency <= 0 {
		return nil, fmt.Errorf("totalRequests and concurrency must be positive, got %d, %d", totalRequests, concurrency)
	}

	// Channel for job dispatch (buffer size = concurrency so memory stays O(concurrency) even with huge totalRequests).
	myJobs := make(chan struct{}, concurrency)
	// Single channel for sending and receiving results; one goroutine does all aggregation so no locking is needed.
	myResults := make(chan MyResult, concurrency)

	var myWg sync.WaitGroup

	// Start exactly concurrency workers (loop only starts them, so it exits quickly).
	for i := 0; i < concurrency; i++ {
		// We are about to start one worker, so add one to the wait count.
		myWg.Add(1)
		go func() {
			defer myWg.Done() // When this goroutine exits, signal one completion to the WaitGroup.
			for range myJobs {
				// Check for cancellation (e.g. Ctrl+C)
				select {
				case <-ctx.Done():
					myResults <- MyResult{MyErr: ctx.Err()}
					return
				default:
				}
				// Execute one HTTP request and send the result.
				myResults <- r.executeRequest(ctx, url)
			}
		}()
	}

	// Producer: enqueue jobs in a separate goroutine so we can react to ctx.Done() and avoid blocking main.
	go func() {
		defer close(myJobs)
		for i := 0; i < totalRequests; i++ {
			select {
			case <-ctx.Done():
				return
			case myJobs <- struct{}{}:
			}
		}
	}()

	// Close the results channel after all workers finish (done once, outside the loop).
	go func() {
		myWg.Wait() // Block until the count reaches zero.
		close(myResults)
	}()

	// Receive results one by one from myResults and aggregate (safe because only this goroutine writes).
	// Collect successful request durations for percentile calculation.
	const progressIntervalCount = 50
	const progressIntervalTime = 200 * time.Millisecond

	runStart := time.Now()
	var lastProgressAt time.Time
	myDurations := make([]time.Duration, 0, totalRequests)
	mySum := &MySummary{
		MyStatusCodeCnt: make(map[int]int),
		MyErrorReasons:  make(map[string]int),
	}
	for res := range myResults {
		if res.MyErr != nil {
			mySum.MyTotal++
			mySum.MyFailed++
			if mySum.MyFirstErr == nil {
				mySum.MyFirstErr = res.MyErr
			}
			// 4xx/5xx など HTTP エラーで失敗した場合もステータスコードの内訳を集計する（TUI で 500 が何回か等が分かる）。
			if res.MyStatusCode != 0 {
				mySum.MyStatusCodeCnt[res.MyStatusCode]++
			}
			// エラー要因を集計（Master の TUI で上位表示するため。通信エラーは汎用名に丸める）。
			reason := errorReasonString(res)
			if reason != "" {
				mySum.MyErrorReasons[reason]++
			}
			goto reportProgress
		}
		mySum.MyTotal++
		mySum.MyTotalDuration += res.MyDuration
		mySum.MySuccess++
		mySum.MyStatusCodeCnt[res.MyStatusCode]++
		myDurations = append(myDurations, res.MyDuration)

	reportProgress:
		if onProgress != nil {
			now := time.Now()
			elapsed := now.Sub(runStart)
			shouldReport := mySum.MyTotal%progressIntervalCount == 0 ||
				lastProgressAt.IsZero() || now.Sub(lastProgressAt) >= progressIntervalTime
			if shouldReport {
				lastProgressAt = now
				onProgress(mySum.MyTotal, mySum.MySuccess, mySum.MyFailed, elapsed)
			}
		}
	}
	// Compute latency percentiles from successful requests only.
	if len(myDurations) > 0 {
		sort.Slice(myDurations, func(i, j int) bool { return myDurations[i] < myDurations[j] })
		mySum.LatencyP50 = percentile(myDurations, 0.50)
		mySum.LatencyP90 = percentile(myDurations, 0.90)
		mySum.LatencyP99 = percentile(myDurations, 0.99)
	}
	return mySum, nil // Return the aggregated result to the caller.
}

// percentile returns the duration at the given percentile (0.0–1.0) from a sorted slice.
// The slice must be non-empty and sorted in ascending order.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := p * float64(len(sorted))
	if idx >= float64(len(sorted)) {
		idx = float64(len(sorted) - 1)
	}
	return sorted[int(idx)]
}

// errorReasonString は MyResult から TUI 用のエラー要因文字列を返す。失敗時のみ意味がある。
// HTTP 4xx/5xx の場合は "HTTP 500 Internal Server Error" 形式、通信エラーは sanitizeError で汎用名に丸める。
func errorReasonString(res MyResult) string {
	if res.MyErr == nil {
		return ""
	}
	return sanitizeError(res.MyErr.Error())
}

// sanitizeError は長いエラー文字列（URL 含む）を TUI 表示用に汎用的な短い名前に丸める。
func sanitizeError(s string) string {
	// すでに "HTTP 500 ..." 形式ならそのまま返す
	if strings.HasPrefix(s, "HTTP ") {
		return s
	}
	lower := strings.ToLower(s)
	switch {
	case strings.Contains(lower, "connection refused"):
		return "connection refused"
	case strings.Contains(lower, "connection reset"):
		return "connection reset by peer"
	case strings.Contains(lower, "i/o timeout"), strings.Contains(lower, "timeout"):
		return "timeout"
	case strings.Contains(lower, "no such host"), strings.Contains(lower, "unknown host"):
		return "no such host"
	case strings.Contains(lower, "tls:"), strings.Contains(lower, "x509"):
		return "TLS/certificate error"
	case strings.Contains(lower, "context canceled"), strings.Contains(lower, "context deadline"):
		return "context canceled"
	}
	// それ以外は長すぎる場合は先頭のみ（例: "Get \"https://...\": ..." → 末尾の理由部分を優先したいが、簡易的に 80 文字で切る）
	const maxLen = 80
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// executeRequest performs a single HTTP GET and returns the result.
// Request creation, send, and response handling are centralized here for readability.
func (r *MyRunner) executeRequest(ctx context.Context, url string) MyResult {
	if ctx.Err() != nil {
		return MyResult{MyErr: ctx.Err()}
	}

	// Record time just before sending the HTTP GET so we can measure duration.
	myStart := time.Now()

	// Standard: http.NewRequestWithContext creates a request for GET to this URL with this context.
	// No request body, so the fourth argument is nil.
	myReq, myErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if myErr != nil {
		return MyResult{MyErr: myErr} // Error creating the request (typically URL-related).
	}

	// Standard: *http.Client.Do(myReq) sends the request and blocks until the response is received.
	myResp, myErr := r.MyClient.Do(myReq)
	// Duration from myStart (just before send) to now (just after response) is this request's elapsed time.
	myDuration := time.Since(myStart)
	if myErr != nil {
		return MyResult{MyErr: myErr, MyDuration: myDuration} // Error during the round-trip (typically network).
	}

	// In Go, myResp.Body is a stream (ReadCloser) for reading the response; it holds network connections
	// and buffers, so it must be closed when done.
	defer myResp.Body.Close()

	// Go の Client.Do() は 5xx が返っても「通信は成功した」とみなし err を返さない。
	// 負荷テストでは 4xx/5xx を Fail として数えるため、err != nil に加えて StatusCode >= 400 も失敗とする。
	if myResp.StatusCode >= 400 {
		return MyResult{
			MyStatusCode: myResp.StatusCode,
			MyDuration:   myDuration,
			MyErr:        fmt.Errorf("HTTP %d %s", myResp.StatusCode, myResp.Status),
		}
	}
	// Return the result (status code, duration, no error).
	return MyResult{
		MyStatusCode: myResp.StatusCode,
		MyDuration:   myDuration,
		MyErr:        nil,
	}
}

