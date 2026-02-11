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


