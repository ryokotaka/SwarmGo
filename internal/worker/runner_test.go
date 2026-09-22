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
