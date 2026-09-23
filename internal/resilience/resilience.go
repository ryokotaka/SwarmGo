// Package resilience measures ordinary API requests during a bounded load spike.
package resilience

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"time"

	"github.com/ryokotaka/SwarmGo/internal/worker"
)

type Config struct {
	URL, ProbeURL                                  string
	Request, ProbeRequest                          worker.RequestOptions
	Rate, Concurrency, ProbeRate, ProbeConcurrency int
	ProbeStatus                                    int
	Baseline, Spike, Recovery, RecoveryWindow      time.Duration
	RequestTimeout, MaxStartDelay, MaxP99          time.Duration
	MaxErrorRate                                   float64
}

func (c Config) Validate() error {
	for _, target := range []string{c.URL, c.ProbeURL} {
		if err := worker.ValidateTargetURL(target); err != nil {
			return err
		}
	}
	for _, request := range []worker.RequestOptions{c.Request, c.ProbeRequest} {
		if err := worker.ValidateRequestOptions(request); err != nil {
			return err
		}
	}
	for _, value := range []int{c.Rate, c.Concurrency, c.ProbeRate, c.ProbeConcurrency} {
		if value <= 0 || value > 1_000_000 {
			return fmt.Errorf("rates and concurrency must be between 1 and 1000000")
		}
	}
	for _, d := range []time.Duration{c.Baseline, c.Spike, c.Recovery, c.RecoveryWindow} {
		if d < time.Second || d > time.Hour || d%time.Second != 0 {
			return fmt.Errorf("phase durations and recovery window must be whole seconds between 1s and 1h")
		}
	}
	if c.Baseline+c.Spike+c.Recovery > time.Hour {
		return fmt.Errorf("total scenario duration must not exceed 1h")
	}
	if c.RecoveryWindow > c.Recovery {
		return fmt.Errorf("recovery window must fit in the recovery phase")
	}
	if c.RequestTimeout <= 0 || c.RequestTimeout > time.Minute {
		return fmt.Errorf("request timeout must be between 0 and 1m")
	}
	if c.MaxP99 <= 0 || c.MaxP99 > c.RequestTimeout {
		return fmt.Errorf("max p99 must be positive and no greater than request timeout")
	}
	if c.MaxStartDelay <= 0 || c.MaxStartDelay >= time.Second {
		return fmt.Errorf("max start delay must be positive and less than 1s")
	}
	if math.IsNaN(c.MaxErrorRate) || math.IsInf(c.MaxErrorRate, 0) || c.MaxErrorRate < 0 || c.MaxErrorRate >= 1 {
		return fmt.Errorf("max error rate must be a fraction from 0 up to, but not including, 1")
	}
	if c.ProbeStatus < 200 || c.ProbeStatus > 399 {
		return fmt.Errorf("probe status must be between 200 and 399")
	}
	return nil
}

// Settings deliberately excludes request bodies and authorization headers.
type Settings struct {
	TargetURL             string  `json:"target_url"`
	ProbeURL              string  `json:"probe_url"`
	LoadMethod            string  `json:"load_method"`
	ProbeMethod           string  `json:"probe_method"`
	LoadRate              int     `json:"load_rate"`
	LoadConcurrency       int     `json:"load_concurrency"`
	ProbeRate             int     `json:"probe_rate"`
	ProbeConcurrency      int     `json:"probe_concurrency"`
	ProbeStatus           int     `json:"probe_status"`
	BaselineSeconds       float64 `json:"baseline_seconds"`
	SpikeSeconds          float64 `json:"spike_seconds"`
	RecoverySeconds       float64 `json:"recovery_seconds"`
	RecoveryWindowSeconds float64 `json:"recovery_window_seconds"`
	RequestTimeoutSeconds float64 `json:"request_timeout_seconds"`
	MaxStartDelayUS       int64   `json:"max_start_delay_us"`
	MaxP99US              int64   `json:"max_p99_us"`
	MaxErrorRate          float64 `json:"max_error_rate"`
}

type Phase struct {
	Name             string  `json:"name"`
	StartSeconds     int     `json:"start_seconds"`
	EndSeconds       int     `json:"end_seconds"`
	Planned          int64   `json:"planned"`
	Started          int64   `json:"started"`
	Completed        int64   `json:"completed"`
	Failed           int64   `json:"failed"`
	Missed           int64   `json:"missed"`
	ErrorRate        float64 `json:"error_rate"`
	WorstWindowP99US int64   `json:"worst_window_p99_us"`
	Passed           bool    `json:"passed"`
}

type Report struct {
	SchemaVersion                int                 `json:"schema_version"`
	Status                       string              `json:"status"`
	Reasons                      []string            `json:"reasons"`
	StartedAt                    time.Time           `json:"started_at"`
	Settings                     Settings            `json:"settings"`
	Load                         *worker.PaceSummary `json:"load"`
	Probe                        *worker.PaceSummary `json:"probe"`
	Phases                       []Phase             `json:"phases"`
	Recovered                    bool                `json:"recovered"`
	RecoveryConfirmedSeconds     *float64            `json:"recovery_confirmed_seconds"`
	LoadSettledAfterSpikeSeconds float64             `json:"load_settled_after_spike_seconds"`
}

func reportURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	u.RawQuery, u.Fragment, u.RawFragment = "", "", ""
	u.ForceQuery = false
	return u.String()
}

// Run uses separate lane and connection pools for the load and ordinary probes.
// A late or missed probe makes the result inconclusive, not a successful test.
func Run(ctx context.Context, c Config) (*Report, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	loadRunner := worker.NewMyRunnerWithConcurrency(c.Concurrency)
	probeRunner := worker.NewMyRunnerWithConcurrency(c.ProbeConcurrency)
	defer loadRunner.MyClient.CloseIdleConnections()
	defer probeRunner.MyClient.CloseIdleConnections()
	loadRunner.MyClient.Timeout = c.RequestTimeout
	probeRunner.MyClient.Timeout = c.RequestTimeout
	start := time.Now().Add(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(ctx, c.Baseline+c.Spike+c.Recovery+c.RequestTimeout+time.Second)
	defer cancel()
	type outcome struct {
		sum *worker.PaceSummary
		err error
		end time.Time
	}
	loadDone := make(chan outcome, 1)
	go func() {
		sum, err := loadRunner.RunPaced(ctx, c.URL, worker.PaceOptions{
			Rate: c.Rate, Duration: c.Spike, Concurrency: c.Concurrency,
			Request: c.Request, MaxStartDelay: c.MaxStartDelay, StartAt: start.Add(c.Baseline),
		})
		loadDone <- outcome{sum, err, time.Now()}
	}()
	probe, probeErr := probeRunner.RunPaced(ctx, c.ProbeURL, worker.PaceOptions{
		Rate: c.ProbeRate, Duration: c.Baseline + c.Spike + c.Recovery, Concurrency: c.ProbeConcurrency,
		Request: c.ProbeRequest, ExpectedStatus: c.ProbeStatus, MaxStartDelay: c.MaxStartDelay, StartAt: start,
	})
	if probeErr != nil {
		cancel()
	}
	load := <-loadDone
	if load.sum == nil || probe == nil {
		return nil, fmt.Errorf("run streams: load=%v probe=%v", load.err, probeErr)
	}
	report := Assess(c, load.sum, probe)
	report.StartedAt = start.UTC()
	report.LoadSettledAfterSpikeSeconds = math.Max(0, load.end.Sub(start.Add(c.Baseline+c.Spike)).Seconds())
	if load.err != nil || probeErr != nil {
		report.Status = "inconclusive"
		report.Reasons = append(report.Reasons, fmt.Sprintf("stream interrupted: load=%v probe=%v", load.err, probeErr))
	}
	return report, nil
}

func windowPass(w worker.PaceWindow, c Config) bool {
	return w.Planned > 0 && w.Missed == 0 && w.Started == w.Planned && w.Completed == w.Started &&
		float64(w.Failed)/float64(w.Completed) <= c.MaxErrorRate && w.LatencyP99US <= c.MaxP99.Microseconds()
}

func Assess(c Config, load, probe *worker.PaceSummary) *Report {
	report := &Report{SchemaVersion: 1, Status: "pass", Reasons: []string{}, Load: load, Probe: probe}
	report.Settings = Settings{
		TargetURL: reportURL(c.URL), ProbeURL: reportURL(c.ProbeURL), LoadMethod: c.Request.Method, ProbeMethod: c.ProbeRequest.Method,
		LoadRate: c.Rate, LoadConcurrency: c.Concurrency, ProbeRate: c.ProbeRate, ProbeConcurrency: c.ProbeConcurrency, ProbeStatus: c.ProbeStatus,
		BaselineSeconds: c.Baseline.Seconds(), SpikeSeconds: c.Spike.Seconds(), RecoverySeconds: c.Recovery.Seconds(), RecoveryWindowSeconds: c.RecoveryWindow.Seconds(),
		RequestTimeoutSeconds: c.RequestTimeout.Seconds(), MaxStartDelayUS: c.MaxStartDelay.Microseconds(), MaxP99US: c.MaxP99.Microseconds(), MaxErrorRate: c.MaxErrorRate,
	}
	if report.Settings.LoadMethod == "" {
		report.Settings.LoadMethod = "GET"
	}
	if report.Settings.ProbeMethod == "" {
		report.Settings.ProbeMethod = "GET"
	}
	b, s, r := int(c.Baseline/time.Second), int(c.Spike/time.Second), int(c.Recovery/time.Second)
	for _, interval := range []Phase{{Name: "baseline", EndSeconds: b}, {Name: "spike", StartSeconds: b, EndSeconds: b + s}, {Name: "recovery", StartSeconds: b + s, EndSeconds: b + s + r}} {
		interval.Passed = true
		seen := 0
		for _, w := range probe.Windows {
			if w.Index < interval.StartSeconds || w.Index >= interval.EndSeconds {
				continue
			}
			seen++
			interval.Planned += w.Planned
			interval.Started += w.Started
			interval.Completed += w.Completed
			interval.Failed += w.Failed
			interval.Missed += w.Missed
			interval.WorstWindowP99US = max(interval.WorstWindowP99US, w.LatencyP99US)
			interval.Passed = interval.Passed && windowPass(w, c)
		}
		interval.Passed = interval.Passed && seen == interval.EndSeconds-interval.StartSeconds
		if interval.Completed > 0 {
			interval.ErrorRate = float64(interval.Failed) / float64(interval.Completed)
		}
		report.Phases = append(report.Phases, interval)
	}
	// Recovery requires a contiguous run of good one-second request cohorts.
	consecutive, last := 0, -1
	var latestCompletion float64
	for _, w := range probe.Windows {
		if w.Index < b+s {
			continue
		}
		if w.Index != last+1 || !windowPass(w, c) {
			consecutive = 0
			latestCompletion = 0
			report.Recovered = false
			report.RecoveryConfirmedSeconds = nil
		}
		if windowPass(w, c) {
			consecutive++
			latestCompletion = max(latestCompletion, w.LastCompletionSeconds, float64(w.Index+1))
		}
		last = w.Index
		if consecutive == int(c.RecoveryWindow/time.Second) {
			report.Recovered = true
			seconds := latestCompletion - float64(b+s)
			report.RecoveryConfirmedSeconds = &seconds
		}
	}
	if last != b+s+r-1 {
		report.Recovered = false
		report.RecoveryConfirmedSeconds = nil
	}
	if !report.Phases[1].Passed {
		report.Status = "fail"
		report.Reasons = append(report.Reasons, "ordinary requests exceeded their latency or failure limit during the spike")
	}
	if !report.Recovered {
		report.Status = "fail"
		report.Reasons = append(report.Reasons, "ordinary requests did not recover for the required consecutive windows")
	}
	if !report.Phases[0].Passed {
		report.Status = "inconclusive"
		report.Reasons = append(report.Reasons, "ordinary requests did not meet the baseline limits before the spike")
	}
	if load.Canceled || probe.Canceled || load.Missed > 0 || probe.Missed > 0 || load.Started != load.Planned || probe.Started != probe.Planned || load.Completed != load.Started || probe.Completed != probe.Started {
		report.Status = "inconclusive"
		report.Reasons = append(report.Reasons, "planned requests were missed or interrupted; inspect busy, late and canceled counts")
	}
	if load.IncompleteResponses > 0 {
		report.Status = "inconclusive"
		report.Reasons = append(report.Reasons, "some load requests did not receive a complete HTTP response; distinguish target refusal from generator/connection failures")
	}
	return report
}
