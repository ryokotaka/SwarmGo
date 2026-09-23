package worker

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunnerReadsBodiesAndReusesConnections(t *testing.T) {
	var connections atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "4")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(20 * time.Millisecond)
		io.WriteString(w, "done")
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	runner := NewMyRunner()
	defer runner.MyClient.CloseIdleConnections()
	summary, err := runner.MyRun(context.Background(), srv.URL, 3, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.MySuccess != 3 || summary.MyFailed != 0 {
		t.Fatalf("unexpected counts: %+v", summary)
	}
	if summary.LatencyP50 < 20*time.Millisecond || summary.Elapsed < 60*time.Millisecond {
		t.Fatalf("latency excludes response body: p50=%v elapsed=%v", summary.LatencyP50, summary.Elapsed)
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("opened %d connections for 3 sequential requests; want 1", got)
	}
}

func TestRunnerReusesConnectionsAcrossLargeRequestWaves(t *testing.T) {
	const concurrency = 150 // Exceeds the old fixed pool of 100.
	var connections, requests atomic.Int32
	gates := []chan struct{}{make(chan struct{}), make(chan struct{})}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		n := int(requests.Add(1))
		wave := (n - 1) / concurrency
		if n%concurrency == 0 {
			close(gates[wave])
		}
		select {
		case <-gates[wave]:
			io.WriteString(w, "ok")
		case <-req.Context().Done():
		}
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	runner := NewMyRunnerWithConcurrency(concurrency)
	defer runner.MyClient.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for wave := 0; wave < 2; wave++ {
		summary, err := runner.MyRun(ctx, srv.URL, concurrency, concurrency, nil)
		if err != nil || summary.MySuccess != concurrency || summary.MyFailed != 0 {
			t.Fatalf("wave %d: summary=%+v err=%v", wave, summary, err)
		}
	}
	if got := connections.Load(); got != concurrency {
		t.Fatalf("opened %d connections for two waves; want %d reused connections", got, concurrency)
	}
}

func TestRunnerBoundsConnectionsWithQueuedRequests(t *testing.T) {
	var connections atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Millisecond)
		io.WriteString(w, "ok")
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	runner := NewMyRunnerWithConcurrency(4)
	defer runner.MyClient.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	summary, err := runner.MyRun(ctx, srv.URL, 80, 40, nil)
	if err != nil || summary.MySuccess != 80 || summary.MyFailed != 0 {
		t.Fatalf("queued requests did not complete: summary=%+v err=%v", summary, err)
	}
	if got := connections.Load(); got > 4 {
		t.Fatalf("opened %d connections; want at most 4", got)
	}
}

func TestRunnerCountsTruncatedBodyAsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		io.WriteString(w, "short")
	}))
	defer srv.Close()
	runner := NewMyRunner()
	defer runner.MyClient.CloseIdleConnections()
	summary, err := runner.MyRun(context.Background(), srv.URL, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.MyFailed != 1 || summary.MySuccess != 0 || summary.MyStatusCodeCnt[200] != 1 {
		t.Fatalf("truncated response counted as success: %+v", summary)
	}
}

func TestRunnerHTTPFailuresAndElapsedTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "unavailable")
	}))
	defer srv.Close()
	runner := NewMyRunner()
	defer runner.MyClient.CloseIdleConnections()
	summary, err := runner.MyRun(context.Background(), srv.URL, 5, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.MyFailed != 5 || summary.MyErrorReasons["HTTP 500 Internal Server Error"] != 5 {
		t.Fatalf("unexpected failures: %+v", summary)
	}
	if summary.Elapsed <= 0 || summaryStats(summary).CurrentRps <= 0 {
		t.Fatal("all-failure run must still have elapsed time and throughput")
	}
}

func TestRunnerCanceledBeforeStartDoesNotInventRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := NewMyRunner()
	defer runner.MyClient.CloseIdleConnections()
	summary, err := runner.MyRun(ctx, "http://127.0.0.1:1", 100, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.MyTotal != 0 {
		t.Fatalf("counted %d requests although none started", summary.MyTotal)
	}
}

func TestSummaryRPSUsesWallTime(t *testing.T) {
	stats := summaryStats(&MySummary{MyTotal: 10, MySuccess: 10, MyTotalDuration: 20 * time.Second, Elapsed: 2 * time.Second})
	if stats.CurrentRps != 5 {
		t.Fatalf("RPS=%v; want 5", stats.CurrentRps)
	}
}

func TestNearestRankPercentiles(t *testing.T) {
	durations := make([]time.Duration, 100)
	for i := range durations {
		durations[i] = time.Duration(i+1) * time.Millisecond
	}
	for _, tc := range []struct {
		p    float64
		want time.Duration
	}{{0.5, 50 * time.Millisecond}, {0.9, 90 * time.Millisecond}, {0.99, 99 * time.Millisecond}} {
		if got := percentile(durations, tc.p); got != tc.want {
			t.Errorf("percentile(%v)=%v; want %v", tc.p, got, tc.want)
		}
	}
}

func TestRunnerRejectsInvalidTargets(t *testing.T) {
	for _, target := range []string{"", "example.com", "ftp://example.com", "http://"} {
		if _, err := NewMyRunner().MyRun(context.Background(), target, 1, 1, nil); err == nil {
			t.Errorf("accepted target %q", target)
		}
	}
}

func TestRequestBodySurvives307Redirect(t *testing.T) {
	const body = `{"message":"hello"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/finish", http.StatusTemporaryRedirect)
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil || r.Method != http.MethodPost || string(data) != body || r.ContentLength != int64(len(body)) {
			t.Errorf("redirect changed request: method=%s body=%q length=%d err=%v", r.Method, data, r.ContentLength, err)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	runner := NewMyRunner()
	defer runner.MyClient.CloseIdleConnections()
	summary, err := runner.MyRunWithOptions(context.Background(), srv.URL+"/start", 12, 4, RequestOptions{
		Method: http.MethodPost, Body: []byte(body),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.MySuccess != 12 || summary.MyFailed != 0 {
		t.Fatalf("redirect did not replay POST: %+v", summary)
	}
}

func TestRequestOptionsAreSnapshottedForConcurrentRun(t *testing.T) {
	const body = `{"message":"original"}`
	started, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if first.CompareAndSwap(false, true) {
			close(started)
		}
		<-release
		data, err := io.ReadAll(r.Body)
		if err != nil || string(data) != body || r.Header.Get("X-Test") != "original" {
			t.Errorf("request changed during run: body=%q header=%q err=%v", data, r.Header.Get("X-Test"), err)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	runner := NewMyRunner()
	defer runner.MyClient.CloseIdleConnections()
	options := RequestOptions{Method: http.MethodPost, Body: []byte(body), Headers: map[string]string{"X-Test": "original"}}
	done := make(chan *MySummary, 1)
	go func() {
		summary, err := runner.MyRunWithOptions(context.Background(), srv.URL, 20, 4, options, nil)
		if err != nil {
			t.Error(err)
		}
		done <- summary
	}()
	<-started
	for i := range options.Body {
		options.Body[i] = 'x'
	}
	options.Headers["X-Test"] = "changed"
	close(release)
	summary := <-done
	if summary == nil || summary.MySuccess != 20 || summary.MyFailed != 0 {
		t.Fatalf("run did not keep original request: %+v", summary)
	}
}

func TestValidateRequestOptions(t *testing.T) {
	for _, options := range []RequestOptions{
		{Method: "BAD METHOD"},
		{Headers: map[string]string{"Bad Header": "value"}},
		{Headers: map[string]string{"X-Test": "value\r\ninjected: true"}},
		{Headers: map[string]string{"Content-Length": "1"}},
		{Headers: map[string]string{"Transfer-Encoding": "chunked"}},
		{Headers: map[string]string{"X-Test": "a", "x-test": "b"}},
		{Body: make([]byte, MaxRequestBodyBytes+1)},
	} {
		if err := ValidateRequestOptions(options); err == nil {
			t.Errorf("accepted invalid request: method=%q headers=%v bodyBytes=%d", options.Method, options.Headers, len(options.Body))
		}
	}
}
