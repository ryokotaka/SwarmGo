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

// NewMyRunner creates and returns a single MyRunner.
// HTTP client settings (connection pooling and timeouts) are configured here.
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

// MyRun sends totalRequests GET requests to the given URL, with up to concurrency concurrent executions.
// Uses a worker pool: a fixed number of workers take jobs and call executeRequest.
// Returns an aggregated MySummary when done. If ctx is cancelled, unstarted requests are skipped and the run exits.
func (r *MyRunner) MyRun(ctx context.Context, url string, totalRequests, concurrency int) (*MySummary, error) {
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
	mySum := &MySummary{MyStatusCodeCnt: make(map[int]int)} // MyStatusCodeCnt is a map, so it must be made.
	for res := range myResults {
		if res.MyErr != nil {
			mySum.MyTotal++
			mySum.MyFailed++
			continue
		}
		mySum.MyTotal++
		mySum.MyTotalDuration += res.MyDuration
		mySum.MySuccess++
		mySum.MyStatusCodeCnt[res.MyStatusCode]++
	}
	return mySum, nil // Return the aggregated result to the caller.
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

	// Return the result (status code, duration, no error).
	return MyResult{
		MyStatusCode: myResp.StatusCode,
		MyDuration:   myDuration,
		MyErr:        nil,
	}
}

