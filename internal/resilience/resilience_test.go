package resilience

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ryokotaka/SwarmGo/internal/worker"
)

func testConfig() Config {
	return Config{URL: "http://127.0.0.1", ProbeURL: "http://127.0.0.1", Rate: 10, Concurrency: 10, ProbeRate: 10, ProbeConcurrency: 10, ProbeStatus: 200,
		Baseline: time.Second, Spike: time.Second, Recovery: 3 * time.Second, RecoveryWindow: 2 * time.Second,
		RequestTimeout: time.Second, MaxStartDelay: 50 * time.Millisecond, MaxP99: 100 * time.Millisecond, MaxErrorRate: .01}
}

func healthySummary(seconds int) *worker.PaceSummary {
	s := &worker.PaceSummary{Planned: int64(seconds * 10), Started: int64(seconds * 10), Completed: int64(seconds * 10), Succeeded: int64(seconds * 10), StatusCodes: map[int]int64{200: int64(seconds * 10)}}
	for i := 0; i < seconds; i++ {
		s.Windows = append(s.Windows, worker.PaceWindow{Index: i, Seconds: 1, Planned: 10, Started: 10, Completed: 10, Succeeded: 10, LatencyP99US: 10_000, LastCompletionSeconds: float64(i) + .91})
	}
	return s
}

func TestAssessmentSeparatesRejectionFromOrdinaryFailure(t *testing.T) {
	c := testConfig()
	load, probe := healthySummary(1), healthySummary(5)
	load.Failed = 10
	load.Succeeded = 0
	load.StatusCodes = map[int]int64{429: 10}
	if r := Assess(c, load, probe); r.Status != "pass" {
		t.Fatalf("intentional load rejection: %+v", r)
	}
	probe.Windows[1].Failed = 1
	probe.Windows[1].Succeeded = 9
	probe.Failed = 1
	probe.Succeeded--
	if r := Assess(c, load, probe); r.Status != "fail" || !r.Recovered {
		t.Fatalf("ordinary failure hidden: %+v", r)
	}
}

func TestIncompleteLoadCannotPass(t *testing.T) {
	load, probe := healthySummary(1), healthySummary(5)
	load.Missed = 1
	load.Started--
	load.Completed--
	load.Succeeded--
	if r := Assess(testConfig(), load, probe); r.Status != "inconclusive" {
		t.Fatalf("missing load passed: %+v", r)
	}
}

func TestRecoveryMustLastThroughObservation(t *testing.T) {
	c := testConfig()
	load, probe := healthySummary(1), healthySummary(5)
	probe.Windows[4].LatencyP99US = 200_000
	r := Assess(c, load, probe)
	if r.Recovered || r.RecoveryConfirmedSeconds != nil || r.Status != "fail" {
		t.Fatalf("late relapse hidden: %+v", r)
	}
	probe.Windows[4].LatencyP99US = 10_000
	probe.Windows[2].LatencyP99US = 200_000
	probe.Windows[4].LastCompletionSeconds = 5.2
	r = Assess(c, load, probe)
	if !r.Recovered || r.RecoveryConfirmedSeconds == nil || *r.RecoveryConfirmedSeconds < 3.19 {
		t.Fatalf("recovery confirmed before responses completed: %+v", r)
	}
}

func TestBaselineFailureIsInconclusive(t *testing.T) {
	load, probe := healthySummary(1), healthySummary(5)
	probe.Windows[0].LatencyP99US = 200_000
	if r := Assess(testConfig(), load, probe); r.Status != "inconclusive" {
		t.Fatalf("bad baseline: %+v", r)
	}
}

func TestRunUsesSeparateRequestsAndBoundedPhases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Stream") == "load" {
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := testConfig()
	c.URL = srv.URL
	c.ProbeURL = srv.URL
	c.Recovery = time.Second
	c.RecoveryWindow = time.Second
	c.Request = worker.RequestOptions{Headers: map[string]string{"X-Stream": "load"}}
	c.MaxStartDelay = 200 * time.Millisecond
	r, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "pass" || r.Load.StatusCodes[429] != 10 || r.Probe.Succeeded != 30 {
		t.Fatalf("report=%+v load=%+v probe=%+v", r, r.Load, r.Probe)
	}
}

func TestLoadWithTruncatedBodyCannotPass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/load" {
			w.Header().Set("Content-Length", "10")
			w.WriteHeader(429)
			w.Write([]byte("short"))
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := testConfig()
	c.URL = srv.URL + "/load"
	c.ProbeURL = srv.URL + "/ordinary"
	c.Recovery = time.Second
	c.RecoveryWindow = time.Second
	c.MaxStartDelay = 200 * time.Millisecond
	r, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "inconclusive" || r.Load.IncompleteResponses != 10 || r.Probe.Succeeded != 30 {
		t.Fatalf("broken load passed: %+v load=%+v", r, r.Load)
	}
}

func TestReportOmitsURLCredentialsAndQueries(t *testing.T) {
	c := testConfig()
	c.URL = "https://user:secret-value@localhost/work?token=secret-value#secret-value"
	c.ProbeURL = c.URL
	c.Request = worker.RequestOptions{Headers: map[string]string{"Authorization": "secret-value"}, Body: []byte("secret-value")}
	r := Assess(c, healthySummary(1), healthySummary(5))
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret-value") || r.Settings.TargetURL != "https://localhost/work" {
		t.Fatalf("credentials included: %s", b)
	}
}
