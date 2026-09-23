package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/ryokotaka/SwarmGo/internal/master"
	"github.com/ryokotaka/SwarmGo/internal/worker"
	"github.com/ryokotaka/SwarmGo/proto"
	"google.golang.org/grpc"
)

type runOptions struct {
	port          string
	workers       int
	workerTimeout time.Duration
	timeout       time.Duration
	output        string
	command       *proto.StartCmd
}

type requestCounts struct {
	Expected  int64 `json:"expected"`
	Completed int64 `json:"completed"`
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
}

type latencyPercentiles struct {
	P50 int32 `json:"p50"`
	P90 int32 `json:"p90"`
	P99 int32 `json:"p99"`
}

type latencyMicroseconds struct {
	P50 int64 `json:"p50"`
	P90 int64 `json:"p90"`
	P99 int64 `json:"p99"`
}

type workerReport struct {
	ID           string               `json:"id"`
	State        string               `json:"state"`
	Requests     requestCounts        `json:"requests"`
	DurationMS   *int32               `json:"duration_ms"`
	LatencyMS    *latencyPercentiles  `json:"latency_ms"`
	LatencyUS    *latencyMicroseconds `json:"latency_us"`
	ErrorReasons map[string]int       `json:"error_reasons"`
}

// Percentiles remain per worker. ControllerRPS covers dispatch through the last
// final report; it is not the sum of independently timed worker rates.
type runReport struct {
	SchemaVersion        int            `json:"schema_version"`
	Status               string         `json:"status"`
	Success              bool           `json:"success"`
	Complete             bool           `json:"complete"`
	Error                string         `json:"error,omitempty"`
	TargetURL            string         `json:"target_url"`
	Method               string         `json:"method"`
	ExpectedWorkers      int            `json:"expected_workers"`
	RequestsPerWorker    int32          `json:"requests_per_worker"`
	ConcurrencyPerWorker int32          `json:"concurrency_per_worker"`
	StartedAt            *time.Time     `json:"started_at"`
	FinishedAt           time.Time      `json:"finished_at"`
	ElapsedSeconds       float64        `json:"elapsed_seconds"`
	ControllerRPS        float64        `json:"controller_rps"`
	Requests             requestCounts  `json:"requests"`
	Workers              []workerReport `json:"workers"`
	ErrorReasons         map[string]int `json:"error_reasons"`
}

func runCommand(args []string) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runCommandContext(ctx, args, os.Stderr)
}

func runCommandContext(ctx context.Context, args []string, stderr io.Writer) int {
	options, err := parseRunOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "run: %v\n", err)
		return 2
	}
	// Check the output directory before sending any traffic. Rename the completed
	// report into place so a failed write does not truncate an earlier result.
	output, err := prepareReportFile(options.output)
	if err != nil {
		fmt.Fprintf(stderr, "run: cannot create report: %v\n", err)
		return 1
	}
	defer os.Remove(output.Name())
	defer output.Close()

	listener, err := net.Listen("tcp", ":"+options.port)
	if err != nil {
		fmt.Fprintf(stderr, "run: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "Waiting for %d workers on %s\n", options.workers, listener.Addr())
	report, runErr := executeRun(ctx, options, listener)
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(stderr, "run: cannot write report: %v\n", err)
		return 1
	}
	if err := output.Close(); err != nil {
		fmt.Fprintf(stderr, "run: cannot close report: %v\n", err)
		return 1
	}
	if err := os.Rename(output.Name(), options.output); err != nil {
		fmt.Fprintf(stderr, "run: cannot save report: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "Wrote %s (%d/%d requests completed, %d failed)\n",
		options.output, report.Requests.Completed, report.Requests.Expected, report.Requests.Failed)
	if runErr != nil {
		fmt.Fprintf(stderr, "run: %v\n", runErr)
		return 1
	}
	return 0
}

func parseRunOptions(args []string, stderr io.Writer) (runOptions, error) {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	port := flags.String("p", "50051", "Controller port")
	workers := flags.Int("workers", 1, "Number of workers to wait for and include in this run")
	workerTimeout := flags.Duration("worker-timeout", 30*time.Second, "Maximum wait for workers")
	timeout := flags.Duration("timeout", 2*time.Minute, "Maximum run duration, including command dispatch")
	output := flags.String("output", "report.json", "JSON result file")
	target := flags.String("url", "http://127.0.0.1:8080", "Target URL reachable by every worker")
	requests := flags.Int("n", 100, "Requests per worker")
	concurrency := flags.Int("c", 1, "Concurrency per worker")
	requestFlags := addRequestFlags(flags)
	if err := flags.Parse(args); err != nil {
		return runOptions{}, err
	}
	if flags.NArg() != 0 {
		return runOptions{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	portNumber, err := strconv.Atoi(*port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return runOptions{}, fmt.Errorf("port must be between 1 and 65535")
	}
	if *workers < 1 || *workers > math.MaxInt32 || *requests < 1 || *requests > math.MaxInt32 || *concurrency < 1 || *concurrency > math.MaxInt32 {
		return runOptions{}, fmt.Errorf("workers, requests, and concurrency must be between 1 and 2147483647")
	}
	if *workerTimeout <= 0 || *timeout <= 0 {
		return runOptions{}, fmt.Errorf("worker-timeout and timeout must be positive")
	}
	if *output == "" {
		return runOptions{}, fmt.Errorf("output path must not be empty")
	}
	if err := worker.ValidateTargetURL(*target); err != nil {
		return runOptions{}, err
	}
	request, err := requestFlags.load()
	if err != nil {
		return runOptions{}, err
	}
	return runOptions{
		port: *port, workers: *workers, workerTimeout: *workerTimeout, timeout: *timeout, output: *output,
		command: &proto.StartCmd{
			TargetUrl: *target, TotalRequests: int32(*requests), Concurrency: int32(*concurrency),
			Method: request.Method, Body: request.Body, Headers: request.Headers,
		},
	}, nil
}

func prepareReportFile(path string) (*os.File, error) {
	if info, err := os.Stat(path); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("output is not a regular file: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return os.CreateTemp(filepath.Dir(path), ".swarmgo-report-*.json")
}

// executeRun owns the supplied listener. Its separate readiness and run deadlines
// keep a missing worker or an unresponsive stream from blocking a CLI job forever.
func executeRun(ctx context.Context, options runOptions, listener net.Listener) (runReport, error) {
	server := master.NewServer()
	grpcServer := grpc.NewServer()
	proto.RegisterSwarmServiceServer(grpcServer, server)
	go grpcServer.Serve(listener)
	defer listener.Close()
	defer quitWorkers(server, grpcServer)

	finish := func(err error) (runReport, error) {
		report := makeRunReport(options, server.SnapshotRun(), err)
		if !report.Success && err == nil {
			err = errors.New(report.Error)
		}
		return report, err
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, options.workerTimeout)
	defer cancelWait()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	var runCtx context.Context
	var cancelRun context.CancelFunc
waiting:
	for {
		if err := waitCtx.Err(); err != nil {
			return finish(fmt.Errorf("waiting for %d workers: %w", options.workers, err))
		}
		if len(server.ListWorkers()) >= options.workers {
			runCtx, cancelRun = context.WithTimeout(ctx, options.timeout)
			started := make(chan bool, 1)
			go func() { started <- server.StartRunWithWorkers(options.command, options.workers) }()
			select {
			case ok := <-started:
				if ok {
					break waiting
				}
				cancelRun() // A worker left before the atomic readiness check.
			case <-runCtx.Done():
				cancelRun()
				stopRun(server, grpcServer)
				return finish(fmt.Errorf("dispatching run: %w", runCtx.Err()))
			}
		}
		select {
		case <-waitCtx.Done():
		case <-ticker.C:
		}
	}
	defer cancelRun()
	cancelWait()

	for {
		snapshot := server.SnapshotRun()
		if !snapshot.Running {
			// Polling may resume after the deadline. Accept an on-time final
			// report, but never one that actually completed beyond the limit.
			if deadline, ok := runCtx.Deadline(); ok && snapshot.FinishedAt.After(deadline) {
				return finish(fmt.Errorf("running requests: %w", context.DeadlineExceeded))
			}
			return finish(nil)
		}
		for _, participant := range snapshot.Workers {
			if participant.Disconnected {
				stopRun(server, grpcServer)
				return finish(errors.New("a worker disconnected before completing its run"))
			}
		}
		select {
		case <-runCtx.Done():
			stopRun(server, grpcServer)
			return finish(fmt.Errorf("running requests: %w", runCtx.Err()))
		case <-ticker.C:
		}
	}
}

// STOP allows workers to return their partial counts before QUIT. A blocked send
// is broken by closing gRPC after a short grace period.
func stopRun(server *master.Server, transport *grpc.Server) {
	if !sendWithin(server, transport, &proto.MasterCmd{Cmd: &proto.MasterCmd_Stop{Stop: &proto.StopCmd{}}}, 500*time.Millisecond) {
		return
	}
	deadline := time.NewTimer(500 * time.Millisecond)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for server.SnapshotRun().Running {
		select {
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func quitWorkers(server *master.Server, transport *grpc.Server) {
	defer transport.Stop()
	if !sendWithin(server, transport, &proto.MasterCmd{Cmd: &proto.MasterCmd_Quit{Quit: &proto.QuitCmd{}}}, 500*time.Millisecond) {
		return
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for len(server.ListWorkers()) > 0 {
		select {
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func sendWithin(server *master.Server, transport *grpc.Server, command *proto.MasterCmd, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		server.BroadcastCommand(command)
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		transport.Stop()
		return false
	}
}

func makeRunReport(options runOptions, snapshot master.RunSnapshot, runErr error) runReport {
	report := runReport{
		SchemaVersion: 1, TargetURL: options.command.TargetUrl, Method: options.command.Method,
		ExpectedWorkers: options.workers, RequestsPerWorker: options.command.TotalRequests,
		ConcurrencyPerWorker: options.command.Concurrency, FinishedAt: time.Now().UTC(),
		Requests: requestCounts{Expected: int64(options.workers) * int64(options.command.TotalRequests)},
		Workers:  []workerReport{}, ErrorReasons: make(map[string]int),
	}
	if !snapshot.StartedAt.IsZero() {
		started := snapshot.StartedAt.UTC()
		report.StartedAt = &started
		finished := time.Now()
		if !snapshot.FinishedAt.IsZero() {
			finished = snapshot.FinishedAt
		}
		report.FinishedAt = finished.UTC()
		// Use the original time values to retain Go's monotonic clock readings.
		report.ElapsedSeconds = finished.Sub(snapshot.StartedAt).Seconds()
	}
	report.Complete = report.StartedAt != nil && len(snapshot.Workers) == options.workers
	ids := make([]string, 0, len(snapshot.Workers))
	for id := range snapshot.Workers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		state := snapshot.Workers[id]
		stats := snapshot.Stats[id]
		participant := workerReport{
			ID: id, State: "unfinished", ErrorReasons: state.ErrorReasons,
			Requests: requestCounts{
				Expected:  int64(options.command.TotalRequests),
				Succeeded: int64(stats.SuccessCount), Failed: int64(stats.FailCount),
			},
		}
		participant.Requests.Completed = participant.Requests.Succeeded + participant.Requests.Failed
		if state.Finished {
			participant.State = "finished"
			duration := state.DurationMs
			participant.DurationMS = &duration
			if stats.SuccessCount > 0 {
				participant.LatencyMS = &latencyPercentiles{stats.LatencyP50Ms, stats.LatencyP90Ms, stats.LatencyP99Ms}
				if latency := stats.LatencyUS; latency != nil {
					participant.LatencyUS = &latencyMicroseconds{latency.P50, latency.P90, latency.P99}
				}
			}
		} else if state.Disconnected {
			participant.State = "disconnected"
		}
		if !state.Finished || participant.Requests.Completed != participant.Requests.Expected {
			report.Complete = false
		}
		report.Requests.Completed += participant.Requests.Completed
		report.Requests.Succeeded += participant.Requests.Succeeded
		report.Requests.Failed += participant.Requests.Failed
		for reason, count := range state.ErrorReasons {
			report.ErrorReasons[reason] += count
		}
		report.Workers = append(report.Workers, participant)
	}
	if report.ElapsedSeconds > 0 {
		report.ControllerRPS = float64(report.Requests.Completed) / report.ElapsedSeconds
	}
	report.Complete = report.Complete && report.Requests.Completed == report.Requests.Expected
	report.Success = report.Complete && report.Requests.Failed == 0 && runErr == nil
	report.Status = "incomplete"
	if report.Complete {
		report.Status = "failed"
		if report.Success {
			report.Status = "completed"
		}
	}
	switch {
	case runErr != nil:
		report.Error = runErr.Error()
	case !report.Complete:
		report.Error = "one or more workers did not finish their assigned requests"
	case report.Requests.Failed > 0:
		report.Error = fmt.Sprintf("%d requests failed", report.Requests.Failed)
	}
	return report
}
