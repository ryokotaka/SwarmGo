package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ryokotaka/SwarmGo/internal/resilience"
)

func parseResilienceOptions(args []string, output io.Writer) (resilience.Config, string, error) {
	var c resilience.Config
	f := flag.NewFlagSet("resilience", flag.ContinueOnError)
	f.SetOutput(output)
	f.StringVar(&c.URL, "url", "http://127.0.0.1:8080", "Owned target for the bounded load spike")
	f.StringVar(&c.ProbeURL, "probe-url", "", "Ordinary request target (default: -url)")
	f.IntVar(&c.Rate, "rate", 100, "Planned load requests per second during the spike")
	f.IntVar(&c.Concurrency, "c", 64, "Maximum simultaneous load requests")
	f.IntVar(&c.ProbeRate, "probe-rate", 10, "Ordinary requests per second throughout all phases")
	f.IntVar(&c.ProbeConcurrency, "probe-c", 32, "Maximum simultaneous ordinary requests")
	f.IntVar(&c.ProbeStatus, "probe-status", 200, "Expected final HTTP status for ordinary requests")
	f.DurationVar(&c.Baseline, "baseline", 5*time.Second, "Baseline duration (whole seconds)")
	f.DurationVar(&c.Spike, "spike", 10*time.Second, "Load spike duration (whole seconds)")
	f.DurationVar(&c.Recovery, "recovery", 10*time.Second, "Recovery observation duration (whole seconds)")
	f.DurationVar(&c.RecoveryWindow, "recovery-window", 2*time.Second, "Consecutive healthy seconds required for recovery")
	f.DurationVar(&c.RequestTimeout, "request-timeout", 5*time.Second, "Deadline for each complete response")
	f.DurationVar(&c.MaxStartDelay, "max-start-delay", 50*time.Millisecond, "Latest allowed request start; later requests are missed")
	f.DurationVar(&c.MaxP99, "max-p99", 500*time.Millisecond, "Maximum ordinary-request p99 in each one-second cohort")
	f.Float64Var(&c.MaxErrorRate, "max-error-rate", .01, "Maximum ordinary-request failure fraction in each second")
	path := f.String("output", "resilience.json", "JSON result file")
	loadFlags, probeFlags := addRequestFlags(f), addRequestFlagsWithPrefix(f, "probe-")
	if err := f.Parse(args); err != nil {
		return c, "", err
	}
	if f.NArg() != 0 {
		return c, "", fmt.Errorf("unexpected positional arguments")
	}
	if *path == "" {
		return c, "", fmt.Errorf("output path must not be empty")
	}
	var err error
	if c.Request, err = loadFlags.load(); err != nil {
		return c, "", err
	}
	if c.ProbeRequest, err = probeFlags.load(); err != nil {
		return c, "", err
	}
	if c.ProbeURL == "" {
		c.ProbeURL = c.URL
	}
	return c, *path, c.Validate()
}

func resilienceCommand(args []string) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return resilienceCommandContext(ctx, args, os.Stderr)
}

func resilienceCommandContext(ctx context.Context, args []string, stderr io.Writer) int {
	c, path, err := parseResilienceOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "resilience: %v\n", err)
		return 2
	}
	file, err := prepareReportFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "resilience: %v\n", err)
		return 2
	}
	defer os.Remove(file.Name())
	defer file.Close()
	fmt.Fprintf(stderr, "Ordinary requests: %d/s throughout. Load: %d/s for %s after %s baseline; observe recovery for %s.\n", c.ProbeRate, c.Rate, c.Spike, c.Baseline, c.Recovery)
	report, err := resilience.Run(ctx, c)
	if err != nil {
		fmt.Fprintf(stderr, "resilience: %v\n", err)
		return 2
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(report); err == nil {
		err = file.Close()
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		fmt.Fprintf(stderr, "resilience: save report: %v\n", err)
		return 2
	}
	w := tabwriter.NewWriter(stderr, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "Phase\tOrdinary completed\tFailed\tMissed\tWorst 1s p99\tAll windows pass")
	for _, phase := range report.Phases {
		fmt.Fprintf(w, "%s\t%d/%d\t%d\t%d\t%.2f ms\t%t\n", phase.Name, phase.Completed, phase.Planned, phase.Failed, phase.Missed, float64(phase.WorstWindowP99US)/1000, phase.Passed)
	}
	w.Flush()
	fmt.Fprintf(stderr, "Load: %d/%d started, %d missed; HTTP 429: %d.\n", report.Load.Started, report.Load.Planned, report.Load.Missed, report.Load.StatusCodes[429])
	if report.RecoveryConfirmedSeconds != nil {
		fmt.Fprintf(stderr, "Recovery confirmed %.2fs after scheduled load stopped.\n", *report.RecoveryConfirmedSeconds)
	}
	fmt.Fprintf(stderr, "Result: %s. Wrote %s\n", report.Status, path)
	for _, reason := range report.Reasons {
		fmt.Fprintln(stderr, "- "+reason)
	}
	switch report.Status {
	case "pass":
		return 0
	case "fail":
		return 1
	default:
		return 2
	}
}
