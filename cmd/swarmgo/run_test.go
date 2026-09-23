package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryokotaka/SwarmGo/internal/master"
	"github.com/ryokotaka/SwarmGo/internal/worker"
	"github.com/ryokotaka/SwarmGo/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type runOutcome struct {
	report runReport
	err    error
}

func testRunOptions(target string, workers, requests int) runOptions {
	return runOptions{
		workers: workers, workerTimeout: time.Second, timeout: 2 * time.Second,
		command: &proto.StartCmd{TargetUrl: target, TotalRequests: int32(requests), Concurrency: 2, Method: "GET"},
	}
}

func startRunTest(t *testing.T, options runOptions) (string, <-chan runOutcome, context.CancelFunc) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	outcome := make(chan runOutcome, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		report, err := executeRun(ctx, options, listener)
		outcome <- runOutcome{report, err}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("run did not stop after cancellation")
		}
	})
	return listener.Addr().String(), outcome, cancel
}

func startRunWorker(t *testing.T, addr string) <-chan error {
	t.Helper()
	client, err := worker.NewGRPCClient(addr)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- client.Start() }()
	return done
}

func runResult(t *testing.T, outcome <-chan runOutcome) runOutcome {
	t.Helper()
	select {
	case result := <-outcome:
		return result
	case <-time.After(4 * time.Second):
		t.Fatal("headless run did not finish")
		return runOutcome{}
	}
}

func workerExited(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("worker did not receive a clean QUIT: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker still running")
	}
}

func TestHeadlessRunCompletesAndReportsHTTPFailures(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(status)
				io.WriteString(w, "response body")
			}))
			defer target.Close()
			addr, outcome, _ := startRunTest(t, testRunOptions(target.URL, 2, 12))
			first, second := startRunWorker(t, addr), startRunWorker(t, addr)
			result := runResult(t, outcome)
			if !result.report.Complete || result.report.Requests.Completed != 24 || requests.Load() != 24 {
				t.Fatalf("incorrect completed run: %+v, requests=%d", result, requests.Load())
			}
			if result.report.Success != (status == http.StatusOK) || (result.err == nil) != (status == http.StatusOK) {
				t.Fatalf("HTTP failures did not affect outcome: %+v", result)
			}
			if result.report.ElapsedSeconds <= 0 || result.report.ControllerRPS <= 0 || len(result.report.Workers) != 2 {
				t.Fatalf("missing timing or participant results: %+v", result.report)
			}
			for _, participant := range result.report.Workers {
				if participant.State != "finished" || participant.DurationMS == nil || participant.Requests.Completed != 12 {
					t.Fatalf("missing final worker result: %+v", participant)
				}
				if (participant.LatencyMS != nil) != (status == http.StatusOK) {
					t.Fatalf("latency must cover successful requests only: %+v", participant)
				}
				if (participant.LatencyUS != nil) != (status == http.StatusOK) {
					t.Fatalf("microsecond latency presence lost through gRPC: %+v", participant)
				}
			}
			if status == http.StatusInternalServerError && result.report.ErrorReasons["HTTP 500 Internal Server Error"] != 24 {
				t.Fatalf("missing grouped errors: %+v", result.report)
			}
			workerExited(t, first)
			workerExited(t, second)
			if conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond); err == nil {
				conn.Close()
				t.Fatal("controller listener was not closed")
			}
		})
	}
}

func TestHeadlessRunCannotPassWhenAllWorkersDisconnect(t *testing.T) {
	addr, outcome, _ := startRunTest(t, testRunOptions("http://127.0.0.1:1", 2, 10))
	streams := make([]proto.SwarmService_ConnectClient, 0, 2)
	cancels := make([]context.CancelFunc, 0, 2)
	for _, id := range []string{"first", "second"} {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stream, err := proto.NewSwarmServiceClient(conn).Connect(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Send(&proto.WorkerMsg{Msg: &proto.WorkerMsg_Register{Register: &proto.RegisterMsg{WorkerId: id}}}); err != nil {
			t.Fatal(err)
		}
		streams = append(streams, stream)
		cancels = append(cancels, cancel)
	}
	for _, stream := range streams {
		command, err := stream.Recv()
		if err != nil || command.GetStart() == nil {
			t.Fatalf("worker did not start: %v", err)
		}
	}
	for _, cancel := range cancels {
		cancel()
	}
	result := runResult(t, outcome)
	if result.err == nil || result.report.Success || result.report.Complete || len(result.report.Workers) != 2 {
		t.Fatalf("disconnected workers counted as successful: %+v", result)
	}
	for _, participant := range result.report.Workers {
		if participant.State != "disconnected" {
			t.Fatalf("lost worker not identified: %+v", participant)
		}
	}
}

func TestHeadlessRunWorkerTimeout(t *testing.T) {
	options := testRunOptions("http://127.0.0.1:1", 1, 10)
	options.workerTimeout = 25 * time.Millisecond
	_, outcome, _ := startRunTest(t, options)
	result := runResult(t, outcome)
	if result.err == nil || result.report.Success || result.report.StartedAt != nil || len(result.report.Workers) != 0 {
		t.Fatalf("missing workers counted as a completed run: %+v", result)
	}
	if !strings.Contains(result.report.Error, "waiting for 1 workers") {
		t.Fatalf("readiness error missing from JSON: %+v", result.report)
	}
}

func TestHeadlessRunStopsActiveRequestsAndSavesPartialCounts(t *testing.T) {
	for _, action := range []string{"timeout", "cancel"} {
		t.Run(action, func(t *testing.T) {
			started, canceled := make(chan struct{}), make(chan struct{})
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-r.Context().Done()
				close(canceled)
			}))
			defer target.Close()
			options := testRunOptions(target.URL, 1, 100)
			options.command.Concurrency = 1
			if action == "timeout" {
				options.timeout = 60 * time.Millisecond
			}
			addr, outcome, cancel := startRunTest(t, options)
			done := startRunWorker(t, addr)
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("request did not start")
			}
			if action == "cancel" {
				cancel()
			}
			result := runResult(t, outcome)
			if result.err == nil || result.report.Complete || result.report.Success || result.report.Requests.Completed != 1 || result.report.Requests.Failed != 1 {
				t.Fatalf("partial work was not preserved: %+v", result)
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("active request was not canceled")
			}
			workerExited(t, done)
		})
	}
}

func TestRunOptionsValidateRequestAndLimits(t *testing.T) {
	for _, args := range [][]string{
		{"-workers", "0"}, {"-n", "0"}, {"-c", "-1"}, {"-p", "70000"},
		{"-worker-timeout", "0s"}, {"-timeout", "-1s"}, {"-url", "invalid"},
		{"-method", "bad method"}, {"-header", "missing colon"}, {"-body-file", "/nonexistent/request.json"},
		{"-output", ""}, {"unexpected"},
	} {
		if _, err := parseRunOptions(args, io.Discard); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
		if code := runCommandContext(context.Background(), args, io.Discard); code != 2 {
			t.Fatalf("invalid arguments did not return exit 2: args=%v code=%d", args, code)
		}
	}
	body := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(body, []byte(`{"model":"demo"}`), 0600); err != nil {
		t.Fatal(err)
	}
	options, err := parseRunOptions([]string{"-method", "POST", "-body-file", body, "-header", "Content-Type: application/json"}, io.Discard)
	if err != nil || options.command.Method != "POST" || string(options.command.Body) != `{"model":"demo"}` || options.command.Headers["Content-Type"] != "application/json" {
		t.Fatalf("request configuration lost: %+v, %v", options.command, err)
	}
}

func TestRunCommandWritesIncompleteJSONAndRejectsUnwritableOutput(t *testing.T) {
	for _, output := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing", "report.json")} {
		if code := runCommandContext(context.Background(), []string{"-output", output}, io.Discard); code != 1 {
			t.Fatalf("unwritable output returned %d", code)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.Split(listener.Addr().String(), ":")[1]
	listener.Close()
	output := filepath.Join(t.TempDir(), "report.json")
	var stderr bytes.Buffer
	code := runCommandContext(context.Background(), []string{"-p", port, "-n", "7", "-worker-timeout", "25ms", "-output", output}, &stderr)
	if code != 1 {
		t.Fatalf("readiness timeout returned %d: %s", code, stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var report runReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("invalid JSON report: %v", err)
	}
	if report.SchemaVersion != 1 || report.Success || report.Complete || report.Requests.Expected != 7 || report.Error == "" {
		t.Fatalf("incomplete report lacks result information: %+v", report)
	}
}

func TestRunReportRequiresEachParticipantToCompleteItsAssignment(t *testing.T) {
	options := testRunOptions("http://127.0.0.1", 2, 10)
	// The total alone is insufficient: one participant did extra work while the
	// other completed less than its assignment.
	snapshot := master.RunSnapshot{
		StartedAt: time.Now(), FinishedAt: time.Now(),
		Workers: map[string]master.WorkerRunState{"a": {Finished: true}, "b": {Finished: true}},
		Stats:   map[string]master.StatsUpdate{"a": {SuccessCount: 15}, "b": {SuccessCount: 5}},
	}
	report := makeRunReport(options, snapshot, nil)
	if report.Success || report.Complete || report.Requests.Completed != 20 {
		t.Fatalf("uneven assignments incorrectly passed: %+v", report)
	}
}

func TestRunReportJSONPreservesLatencyPrecisionAndLegacyNulls(t *testing.T) {
	for _, tc := range []struct {
		name     string
		finished bool
		stats    master.StatsUpdate
		wantUS   string
		wantMS   string
	}{
		{name: "sub-millisecond", finished: true, stats: master.StatsUpdate{SuccessCount: 1, LatencyUS: &proto.LatencyMicros{P50: 125, P90: 750, P99: 900}}, wantUS: `{"p50":125,"p90":750,"p99":900}`, wantMS: `{"p50":0,"p90":0,"p99":0}`},
		{name: "measured zero", finished: true, stats: master.StatsUpdate{SuccessCount: 1, LatencyUS: &proto.LatencyMicros{}}, wantUS: `{"p50":0,"p90":0,"p99":0}`, wantMS: `{"p50":0,"p90":0,"p99":0}`},
		{name: "legacy worker", finished: true, stats: master.StatsUpdate{SuccessCount: 1, LatencyP90Ms: 1, LatencyP99Ms: 2}, wantUS: `null`, wantMS: `{"p50":0,"p90":1,"p99":2}`},
		{name: "no successful requests", finished: true, stats: master.StatsUpdate{FailCount: 1}, wantUS: `null`, wantMS: `null`},
		{name: "unfinished worker", stats: master.StatsUpdate{SuccessCount: 1, LatencyUS: &proto.LatencyMicros{P50: 125, P90: 750, P99: 900}}, wantUS: `null`, wantMS: `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := master.RunSnapshot{
				StartedAt: time.Now(),
				Workers:   map[string]master.WorkerRunState{"worker": {Finished: tc.finished}},
				Stats:     map[string]master.StatsUpdate{"worker": tc.stats},
			}
			report := makeRunReport(testRunOptions("http://127.0.0.1", 1, 1), snapshot, nil)
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Workers []map[string]json.RawMessage `json:"workers"`
			}
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Workers) != 1 {
				t.Fatalf("worker omitted from JSON: %s", encoded)
			}
			got := decoded.Workers[0]
			if string(got["latency_us"]) != tc.wantUS || string(got["latency_ms"]) != tc.wantMS {
				t.Fatalf("wrong precision or missing/null distinction: latency_us=%s latency_ms=%s", got["latency_us"], got["latency_ms"])
			}
		})
	}
}
