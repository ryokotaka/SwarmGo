package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func pacedTestRunner(t *testing.T, concurrency int) *MyRunner {
	t.Helper()
	r := NewMyRunnerWithConcurrency(concurrency)
	r.MyClient.Timeout = 2 * time.Second
	t.Cleanup(r.MyClient.CloseIdleConnections)
	return r
}

func checkPaceConservation(t *testing.T, s *PaceSummary) {
	t.Helper()
	if s.Planned != s.Started+s.Missed || s.Started != s.Completed || s.Completed != s.Succeeded+s.Failed || s.Missed != s.BusyMissed+s.LateMissed+s.CanceledMissed {
		t.Fatalf("inconsistent summary: %+v", s)
	}
	var planned, completed, missed int64
	for _, w := range s.Windows {
		if w.Planned != w.Started+w.Missed || w.Started != w.Completed || w.Completed != w.Succeeded+w.Failed || w.Missed != w.BusyMissed+w.LateMissed+w.CanceledMissed {
			t.Fatalf("inconsistent window: %+v", w)
		}
		planned += w.Planned
		completed += w.Completed
		missed += w.Missed
	}
	if planned != s.Planned || completed != s.Completed || missed != s.Missed {
		t.Fatalf("window totals differ from summary: %+v", s)
	}
}

func TestPacedPlanCountsAndValidation(t *testing.T) {
	for _, tc := range []struct {
		rate     int
		duration time.Duration
		count    int64
	}{
		{10, 250 * time.Millisecond, 3},
		{3, time.Second, 3},
		{3, 1100 * time.Millisecond, 4},
		{1, time.Nanosecond, 1},
		{1000000, 24 * time.Hour, 86400000000},
	} {
		o := PaceOptions{Rate: tc.rate, Duration: tc.duration, Concurrency: 1, MaxStartDelay: time.Millisecond}
		count, err := validatePaceOptions(o)
		if err != nil || count != tc.count {
			t.Fatalf("rate=%d duration=%s: got %d, %v; want %d", tc.rate, tc.duration, count, err, tc.count)
		}
		if last := paceOffset(count-1, int64(tc.rate)); last >= tc.duration {
			t.Fatalf("last scheduled offset=%s exceeds duration %s", last, tc.duration)
		}
	}
	valid := PaceOptions{Rate: 1, Duration: time.Second, Concurrency: 1, MaxStartDelay: time.Millisecond}
	for name, mutate := range map[string]func(*PaceOptions){
		"rate":        func(o *PaceOptions) { o.Rate = 0 },
		"duration":    func(o *PaceOptions) { o.Duration = 0 },
		"too_long":    func(o *PaceOptions) { o.Duration = 24*time.Hour + 1 },
		"concurrency": func(o *PaceOptions) { o.Concurrency = 0 },
		"delay":       func(o *PaceOptions) { o.MaxStartDelay = 0 },
		"status":      func(o *PaceOptions) { o.ExpectedStatus = 99 },
		"overflow":    func(o *PaceOptions) { o.Rate = math.MaxInt; o.Duration = 24 * time.Hour },
	} {
		t.Run(name, func(t *testing.T) {
			if name == "overflow" && uint64(math.MaxInt) < uint64(math.MaxInt64) {
				t.Skip("32-bit rates cannot overflow the int64 plan within 24h")
			}
			o := valid
			mutate(&o)
			if _, err := validatePaceOptions(o); err == nil {
				t.Fatalf("accepted invalid options %+v", o)
			}
		})
	}
	runner := pacedTestRunner(t, 1)
	if _, err := runner.RunPaced(context.Background(), "not a URL", valid); err == nil {
		t.Fatal("accepted invalid URL")
	}
	valid.Request.Headers = map[string]string{"Content-Length": "1"}
	if _, err := runner.RunPaced(context.Background(), "http://localhost", valid); err == nil {
		t.Fatal("accepted invalid request options")
	}
}

func TestPacedWaitsForDurationAndReusesConnection(t *testing.T) {
	var connections atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || string(body) != "payload" {
			t.Errorf("method=%s body=%q", r.Method, body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	o := PaceOptions{Rate: 5, Duration: 450 * time.Millisecond, Concurrency: 1, MaxStartDelay: 100 * time.Millisecond,
		Request: RequestOptions{Method: http.MethodPost, Body: []byte("payload")}}
	s, err := pacedTestRunner(t, 1).RunPaced(context.Background(), srv.URL, o)
	if err != nil {
		t.Fatal(err)
	}
	checkPaceConservation(t, s)
	if s.Planned != 3 || s.Succeeded != 3 || s.Missed != 0 || connections.Load() != 1 || s.ElapsedSeconds < o.Duration.Seconds() {
		t.Fatalf("summary=%+v connections=%d", s, connections.Load())
	}
	if len(s.Windows) != 1 || s.Windows[0].Seconds != .45 {
		t.Fatalf("partial window: %+v", s.Windows)
	}
}

func TestPacedBusyLanesDoNotQueueAndDrainStartedBody(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		entered <- struct{}{}
		select {
		case <-release:
			io.WriteString(w, "ok")
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	o := PaceOptions{Rate: 50, Duration: 200 * time.Millisecond, Concurrency: 1, MaxStartDelay: 100 * time.Millisecond}
	type outcome struct {
		s   *PaceSummary
		err error
	}
	done := make(chan outcome, 1)
	runner := pacedTestRunner(t, 1)
	go func() { s, err := runner.RunPaced(context.Background(), srv.URL, o); done <- outcome{s, err} }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}
	select {
	case result := <-done:
		t.Fatalf("returned before the started body completed: %+v", result)
	case <-time.After(250 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		s := result.s
		checkPaceConservation(t, s)
		if s.Planned != 10 || s.Started != 1 || s.Succeeded != 1 || s.BusyMissed == 0 || s.LatencyP99US < 200000 || s.Windows[0].LastCompletionSeconds < .2 {
			t.Fatalf("slow response changed plan or queued requests: %+v", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not drain after releasing body")
	}
}

func TestPacedStatusClassificationAndFailedLatency(t *testing.T) {
	for _, tc := range []struct {
		name       string
		expected   int
		truncated  bool
		wantFailed int64
	}{
		{"default_429", 0, false, 1},
		{"expected_200", 200, false, 1},
		{"expected_429", 429, false, 0},
		{"expected_429_bad_body", 429, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(10 * time.Millisecond)
				if tc.truncated {
					w.Header().Set("Content-Length", "10")
				}
				w.WriteHeader(http.StatusTooManyRequests)
				io.WriteString(w, "no")
			}))
			t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
			o := PaceOptions{Rate: 1, Duration: 20 * time.Millisecond, Concurrency: 1, MaxStartDelay: 100 * time.Millisecond, ExpectedStatus: tc.expected}
			s, err := pacedTestRunner(t, 1).RunPaced(context.Background(), srv.URL, o)
			if err != nil {
				t.Fatal(err)
			}
			checkPaceConservation(t, s)
			if s.Completed != 1 || s.Failed != tc.wantFailed || s.StatusCodes[429] != 1 || s.LatencyP99US < 9000 {
				t.Fatalf("bad status/latency classification: %+v", s)
			}
			wantIncomplete := int64(0)
			if tc.truncated {
				wantIncomplete = 1
			}
			if s.IncompleteResponses != wantIncomplete {
				t.Fatalf("body completeness lost: %+v", s)
			}
		})
	}
}

func TestPacedFutureStart(t *testing.T) {
	arrived := make(chan time.Time, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		arrived <- time.Now()
		w.WriteHeader(204)
	}))
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	o := PaceOptions{Rate: 1, Duration: 20 * time.Millisecond, Concurrency: 1, MaxStartDelay: 100 * time.Millisecond, StartAt: time.Now().Add(100 * time.Millisecond)}
	s, err := pacedTestRunner(t, 1).RunPaced(context.Background(), srv.URL, o)
	if err != nil {
		t.Fatal(err)
	}
	checkPaceConservation(t, s)
	if s.Succeeded != 1 || s.ElapsedSeconds < .02 {
		t.Fatalf("unexpected summary: %+v", s)
	}
	if when := <-arrived; when.Before(o.StartAt) {
		t.Fatalf("sent at %s before requested start %s", when, o.StartAt)
	}
}

func TestPacedCancelBeforeFutureStartCountsWholePlan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o := PaceOptions{Rate: 10, Duration: 2300 * time.Millisecond, Concurrency: 2, MaxStartDelay: time.Millisecond, StartAt: time.Now().Add(time.Hour)}
	s, err := pacedTestRunner(t, 2).RunPaced(ctx, "http://127.0.0.1:1", o)
	if !errors.Is(err, context.Canceled) || !s.Canceled {
		t.Fatalf("err=%v summary=%+v", err, s)
	}
	checkPaceConservation(t, s)
	if s.Planned != 23 || s.CanceledMissed != 23 || s.Started != 0 || s.ElapsedSeconds != 0 || len(s.Windows) != 3 || s.Windows[2].Seconds != .3 {
		t.Fatalf("bad canceled plan: %+v", s)
	}
}

func TestPacedCancelInterruptsResponseBody(t *testing.T) {
	entered := make(chan struct{}, 2)
	closed := make(chan struct{}, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(200)
		io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		entered <- struct{}{}
		<-r.Context().Done()
		closed <- struct{}{}
	}))
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runner := pacedTestRunner(t, 2)
	runner.MyClient.Timeout = 10 * time.Second
	o := PaceOptions{Rate: 100, Duration: time.Second, Concurrency: 2, MaxStartDelay: 100 * time.Millisecond}
	done := make(chan *PaceSummary, 1)
	go func() {
		s, err := runner.RunPaced(ctx, srv.URL, o)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("canceled run error=%v", err)
		}
		done <- s
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request body did not begin")
	}
	cancel()
	select {
	case s := <-done:
		checkPaceConservation(t, s)
		if !s.Canceled || s.Started == 0 || s.Failed != s.Started || s.CanceledMissed == 0 {
			t.Fatalf("bad canceled body counts: %+v", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not interrupt response body")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("canceled connection was left open")
	}
}

func TestPacedExpiredStartDoesNotReplayBacklog(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(204)
	}))
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	o := PaceOptions{Rate: 10000, Duration: 100 * time.Millisecond, Concurrency: 4, MaxStartDelay: 10 * time.Millisecond, StartAt: time.Now().Add(-time.Second)}
	s, err := pacedTestRunner(t, 4).RunPaced(context.Background(), srv.URL, o)
	if err != nil {
		t.Fatal(err)
	}
	checkPaceConservation(t, s)
	if s.Planned != 1000 || s.LateMissed != 1000 || s.Started != 0 || requests.Load() != 0 {
		t.Fatalf("replayed expired schedule: %+v requests=%d", s, requests.Load())
	}
}

func TestPacedDoesNotDropSlotsInsideLatenessBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	o := PaceOptions{Rate: 1000, Duration: 50 * time.Millisecond, Concurrency: 50, MaxStartDelay: 500 * time.Millisecond, StartAt: time.Now().Add(-20 * time.Millisecond)}
	s, err := pacedTestRunner(t, 50).RunPaced(context.Background(), srv.URL, o)
	if err != nil {
		t.Fatal(err)
	}
	checkPaceConservation(t, s)
	if s.Succeeded != 50 || s.Missed != 0 {
		t.Fatalf("discarded slots still inside lateness budget: %+v", s)
	}
}

func TestPacedWindowKeepsLateCompletionAndReleasesHistograms(t *testing.T) {
	o := PaceOptions{Rate: 2, Duration: 1500 * time.Millisecond}
	state := newPaceState(o, 3)
	state.complete(2, MyResult{MyStatusCode: 200, MyDuration: 100 * time.Millisecond, ResponseComplete: true}, 1.1, 2*time.Microsecond)
	state.miss(1, 2, paceBusy)
	state.complete(0, MyResult{MyStatusCode: 429, MyDuration: 1500 * time.Millisecond, MyErr: fmt.Errorf("HTTP 429 Too Many Requests"), ResponseComplete: true}, 1.5, time.Microsecond)
	s := state.summary(1.5, false)
	checkPaceConservation(t, s)
	if s.Windows[0].Completed != 1 || s.Windows[0].Failed != 1 || s.Windows[0].LastCompletionSeconds != 1.5 || s.Windows[1].Succeeded != 1 || s.LatencyP99US < 1499000 {
		t.Fatalf("late failed latency was omitted or moved: %+v", s)
	}
	for i, w := range state.windows {
		if w.latency != nil || w.delay != nil {
			t.Fatalf("window %d retained histograms after all outcomes were accounted for", i)
		}
	}
}
