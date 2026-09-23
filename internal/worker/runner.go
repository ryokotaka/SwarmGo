package worker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpguts"
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
	MyTotal         int            // Total number of requests executed
	MySuccess       int            // Number of successful requests
	MyFailed        int            // Number of failed requests
	MyFirstErr      error          // First error encountered (for logging when MyFailed > 0)
	MyStatusCodeCnt map[int]int    // Number of requests for each status code (pair of [status code] and [number of requests])
	MyErrorReasons  map[string]int // occurrence count per failure reason (sent to Master for TUI top-N display)
	MyTotalDuration time.Duration  // Sum of successful request durations (used for mean latency)
	Elapsed         time.Duration  // Wall-clock duration of the request run (used for RPS)
	LatencyP50      time.Duration  // 50th percentile latency (successful requests only)
	LatencyP90      time.Duration  // 90th percentile latency (successful requests only)
	LatencyP99      time.Duration  // 99th percentile latency (successful requests only)
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
		"/etc/ssl/cert.pem",                  // Alpine alternative
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
	return NewMyRunnerWithConcurrency(100)
}

// NewMyRunnerWithConcurrency keeps enough idle connections for a full wave of
// requests. A smaller pool repeatedly closes and redials connections when many
// responses finish together, which can exhaust local TCP ports at high load.
func NewMyRunnerWithConcurrency(concurrency int) *MyRunner {
	concurrency = max(1, concurrency)
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
		MaxIdleConns:        concurrency,
		MaxIdleConnsPerHost: concurrency,
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
	return r.MyRunWithOptions(ctx, url, totalRequests, concurrency, RequestOptions{}, onProgress)
}

// MyRunWithOptions snapshots the request body and headers once, then replays
// that request independently in each goroutine. An empty Method means GET.
func (r *MyRunner) MyRunWithOptions(ctx context.Context, url string, totalRequests, concurrency int, options RequestOptions, onProgress OnProgressFunc) (*MySummary, error) {
	// Argument check: return error if count or concurrency is zero or less.
	if totalRequests <= 0 || concurrency <= 0 {
		return nil, fmt.Errorf("totalRequests and concurrency must be positive, got %d, %d", totalRequests, concurrency)
	}

	template, err := requestTemplate(url, options)
	if err != nil {
		return nil, err
	}
	if concurrency > totalRequests {
		concurrency = totalRequests
	}
	runStart := time.Now()

	// Queue and pool sizes depend on concurrency; latency samples are retained separately.
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
					return
				default:
				}
				// Execute one HTTP request and send the result.
				myResults <- r.executeRequest(ctx, template)
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

	var lastProgressAt time.Time
	myDurations := make([]time.Duration, 0, min(totalRequests, 1024))
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
			// Aggregate status code breakdown even for HTTP errors (4xx/5xx) so the TUI can show e.g. how many 500s
			if res.MyStatusCode != 0 {
				mySum.MyStatusCodeCnt[res.MyStatusCode]++
			}
			// Aggregate error reasons for Master's TUI top-N display; network errors are normalized to a generic name
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
	mySum.Elapsed = time.Since(runStart)
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
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// errorReasonString returns a TUI-friendly error reason string from MyResult. Only meaningful when the request failed.
// For HTTP 4xx/5xx we use "HTTP 500 Internal Server Error" style; network errors are normalized via sanitizeError.
func errorReasonString(res MyResult) string {
	if res.MyErr == nil {
		return ""
	}
	return sanitizeError(res.MyErr.Error())
}

// sanitizeError normalizes long error strings (e.g. with URLs) to short, generic names for TUI display.
func sanitizeError(s string) string {
	// Already in "HTTP 500 ..." form; return as-is
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
	// Otherwise truncate if too long (e.g. "Get \"https://...\": ..."; we take first 80 chars for simplicity)
	const maxLen = 80
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ValidateTargetURL rejects invalid targets before a run allocates workers or sends traffic.
func ValidateTargetURL(target string) error {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("target URL must be an absolute http or https URL")
	}
	return nil
}

// MaxRequestBodyBytes keeps start commands comfortably below gRPC's default
// 4 MiB receive limit. The body is sent once to each worker, not once per request.
const MaxRequestBodyBytes = 1 << 20

// RequestOptions describes the request repeated by a load test.
// Header names are case-insensitive; each name has one value.
type RequestOptions struct {
	Method  string
	Body    []byte
	Headers map[string]string
}

func ValidateRequestOptions(options RequestOptions) error {
	if _, err := http.NewRequest(options.Method, "http://localhost", nil); err != nil {
		return fmt.Errorf("invalid HTTP method: %w", err)
	}
	if len(options.Body) > MaxRequestBodyBytes {
		return fmt.Errorf("request body exceeds %d bytes", MaxRequestBodyBytes)
	}
	seen := make(map[string]bool, len(options.Headers))
	for name, value := range options.Headers {
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
			return fmt.Errorf("invalid HTTP header %q", name)
		}
		key := strings.ToLower(name)
		if seen[key] {
			return fmt.Errorf("duplicate HTTP header %q", name)
		}
		seen[key] = true
		switch key {
		case "content-length", "transfer-encoding", "trailer":
			return fmt.Errorf("header %q is managed by the HTTP client", name)
		}
	}
	return nil
}

func requestTemplate(target string, options RequestOptions) (*http.Request, error) {
	if err := ValidateTargetURL(target); err != nil {
		return nil, err
	}
	if err := ValidateRequestOptions(options); err != nil {
		return nil, err
	}
	// Clone the bytes so callers cannot change a run by editing the input buffer.
	req, err := http.NewRequest(options.Method, target, bytes.NewReader(bytes.Clone(options.Body)))
	if err != nil {
		return nil, err
	}
	for name, value := range options.Headers {
		if strings.EqualFold(name, "Host") {
			req.Host = value
		} else {
			req.Header.Set(name, value)
		}
	}
	return req, nil
}

// executeRequest measures the complete response, including reading its body.
// Draining the body also lets the transport reuse keep-alive connections.
func (r *MyRunner) executeRequest(ctx context.Context, template *http.Request) MyResult {
	start := time.Now()
	req := template.Clone(ctx)
	// Clone does not clone Body. GetBody gives each request its own reader and
	// also allows the standard client to replay it across 307/308 redirects.
	if template.GetBody != nil {
		var err error
		req.Body, err = template.GetBody()
		if err != nil {
			return MyResult{MyErr: err, MyDuration: time.Since(start)}
		}
	}
	resp, err := r.MyClient.Do(req)
	if err != nil {
		return MyResult{MyErr: err, MyDuration: time.Since(start)}
	}
	defer resp.Body.Close()
	_, readErr := io.Copy(io.Discard, resp.Body)
	result := MyResult{MyStatusCode: resp.StatusCode, MyDuration: time.Since(start)}
	switch {
	case readErr != nil:
		result.MyErr = fmt.Errorf("read response body: %w", readErr)
	case resp.StatusCode >= http.StatusBadRequest:
		result.MyErr = fmt.Errorf("HTTP %s", resp.Status)
	}
	return result
}
