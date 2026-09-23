package worker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestShardedRunPreservesCountsAndFinalProgress(t *testing.T) {
	const total = 10003
	var calls atomic.Int64
	runner := &MyRunner{MyClient: &http.Client{Transport: testRoundTripper(func(req *http.Request) (*http.Response, error) {
		n := calls.Add(1)
		code := 200
		if n%7 == 0 {
			code = 503
		}
		return &http.Response{StatusCode: code, Status: fmt.Sprintf("%d %s", code, http.StatusText(code)), Body: io.NopCloser(strings.NewReader("body")), Header: make(http.Header), Request: req}, nil
	})}}
	previous, lastSuccess, lastFailed := 0, 0, 0
	summary, err := runner.MyRun(context.Background(), "http://localhost/test", total, 37, func(completed, success, failed int, _ time.Duration) {
		if completed < previous || completed != success+failed {
			t.Errorf("invalid progress %d/%d/%d after %d", completed, success, failed, previous)
		}
		previous, lastSuccess, lastFailed = completed, success, failed
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != total || summary.MyTotal != total || summary.MyFailed != total/7 || summary.MySuccess != total-total/7 {
		t.Fatalf("lost or repeated work: calls=%d summary=%+v", calls.Load(), summary)
	}
	if summary.MyStatusCodeCnt[503] != total/7 || summary.MyErrorReasons["HTTP 503 Service Unavailable"] != total/7 {
		t.Fatalf("lost error breakdown: %+v", summary)
	}
	if previous != total || lastSuccess != summary.MySuccess || lastFailed != summary.MyFailed {
		t.Fatalf("missing final progress: %d/%d/%d", previous, lastSuccess, lastFailed)
	}
}

func TestLatencyHistogramQuantilesAndLargeOutliers(t *testing.T) {
	h := latencyHistogram()
	for i := 1; i <= 10000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	// Quantization may round the reported value up by at most 0.1% here.
	for _, tc := range []struct {
		q    float64
		want int64
	}{{50, 5000}, {90, 9000}, {99, 9900}} {
		got := h.ValueAtQuantile(tc.q)
		if got < tc.want || got > tc.want+tc.want/1000+1 {
			t.Errorf("p%v=%d want about %d us", tc.q, got, tc.want)
		}
	}
	// Long operations must remain represented, even with a custom no-timeout client.
	if err := h.RecordValue((48 * time.Hour).Microseconds()); err != nil {
		t.Fatal(err)
	}
	if err := h.RecordValue(maxLatencyMicros); err != nil {
		t.Fatal(err)
	}
	if h.TotalCount() != 10002 {
		t.Fatalf("lost latency samples: %d", h.TotalCount())
	}
}
