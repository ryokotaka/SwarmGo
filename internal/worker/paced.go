package worker

import (
	"context"
	"fmt"
	"math"
	"math/bits"
	"sync"
	"time"

	hdr "github.com/HdrHistogram/hdrhistogram-go"
)

// PaceOptions schedules requests independently of response completion. Rate is
// for this runner, and Concurrency bounds requests in flight, including dials.
type PaceOptions struct {
	Rate           int
	Duration       time.Duration
	Concurrency    int
	MaxStartDelay  time.Duration
	Request        RequestOptions
	StartAt        time.Time
	ExpectedStatus int // Zero retains the usual HTTP status < 400 rule.
}

// PaceWindow attributes every result to its planned start, even if its response
// finishes in a later window. Latency includes failed requests and the full body.
type PaceWindow struct {
	Index                 int           `json:"index"`
	Seconds               float64       `json:"seconds"`
	Planned               int64         `json:"planned"`
	Started               int64         `json:"started"`
	Completed             int64         `json:"completed"`
	Succeeded             int64         `json:"succeeded"`
	Failed                int64         `json:"failed"`
	IncompleteResponses   int64         `json:"incomplete_responses"`
	Missed                int64         `json:"missed"`
	BusyMissed            int64         `json:"busy_missed"`
	LateMissed            int64         `json:"late_missed"`
	CanceledMissed        int64         `json:"canceled_missed"`
	LatencyP95US          int64         `json:"latency_p95_us"`
	LatencyP99US          int64         `json:"latency_p99_us"`
	StartDelayP99US       int64         `json:"start_delay_p99_us"`
	LastCompletionSeconds float64       `json:"last_completion_seconds"`
	StatusCodes           map[int]int64 `json:"status_codes"`
}

type PaceSummary struct {
	Planned             int64         `json:"planned"`
	Started             int64         `json:"started"`
	Completed           int64         `json:"completed"`
	Succeeded           int64         `json:"succeeded"`
	Failed              int64         `json:"failed"`
	IncompleteResponses int64         `json:"incomplete_responses"`
	Missed              int64         `json:"missed"`
	BusyMissed          int64         `json:"busy_missed"`
	LateMissed          int64         `json:"late_missed"`
	CanceledMissed      int64         `json:"canceled_missed"`
	ElapsedSeconds      float64       `json:"elapsed_seconds"`
	LatencyP95US        int64         `json:"latency_p95_us"`
	LatencyP99US        int64         `json:"latency_p99_us"`
	StartDelayP99US     int64         `json:"start_delay_p99_us"`
	Canceled            bool          `json:"canceled"`
	Windows             []PaceWindow  `json:"windows"`
	StatusCodes         map[int]int64 `json:"status_codes"`
}

type paceWindowState struct {
	PaceWindow
	latency, delay *hdr.Histogram
}

type paceState struct {
	sync.Mutex
	windows        []paceWindowState
	latency, delay *hdr.Histogram
	rate           int64
}

type paceJob struct {
	ticket int64
	due    time.Time
}

type paceMiss int

const (
	paceBusy paceMiss = iota
	paceLate
	paceCanceled
)

// RunPaced starts at t=0, then at k/Rate while t < Duration: the plan contains
// ceil(Rate*Duration) requests. It never queues behind occupied execution lanes.
// Dispatch handles at most min(MaxStartDelay, 1 ms) of planned work per batch.
// After a late wake, adjacent batches may catch up within MaxStartDelay; older
// due slots are missed. The delay histogram retains this scheduling jitter.
// MaxStartDelay also applies when a lane actually starts, including the last
// batch: it can begin just beyond Duration but creates no new scheduled slots.
// The function waits until the scheduled end and all started responses finish.
// Cancellation returns the conserved partial summary together with ctx.Err().
func (r *MyRunner) RunPaced(ctx context.Context, target string, options PaceOptions) (*PaceSummary, error) {
	planned, err := validatePaceOptions(options)
	if err != nil {
		return nil, err
	}
	template, err := requestTemplate(target, options.Request)
	if err != nil {
		return nil, err
	}
	plan, err := r.directPlan(template)
	if err != nil {
		return nil, err
	}
	concurrency := int(min(int64(options.Concurrency), planned))
	if plan != nil && plan.transport.base.MaxConnsPerHost > 0 {
		concurrency = min(concurrency, plan.transport.base.MaxConnsPerHost)
	}
	state := newPaceState(options, planned)
	ready := make(chan chan paceJob, concurrency)
	queues := make([]chan paceJob, concurrency)
	var wg sync.WaitGroup
	startAt := options.StartAt
	if startAt.IsZero() {
		startAt = time.Now()
	}
	endAt := startAt.Add(options.Duration)
	for i := range queues {
		jobs := make(chan paceJob, 1)
		queues[i] = jobs
		wg.Add(1)
		go func() {
			defer wg.Done()
			var lane *directLane
			if plan != nil {
				lane = plan.lane(ctx)
				defer lane.finish()
			}
			for job := range jobs {
				now := time.Now()
				if ctx.Err() != nil {
					state.miss(job.ticket, job.ticket+1, paceCanceled)
				} else if now.Sub(job.due) > options.MaxStartDelay {
					state.miss(job.ticket, job.ticket+1, paceLate)
				} else {
					var result MyResult
					if lane != nil {
						result = lane.execute()
					} else {
						result = r.executeRequest(ctx, template)
					}
					if options.ExpectedStatus != 0 && result.ResponseComplete {
						if result.MyStatusCode == options.ExpectedStatus {
							result.MyErr = nil
						} else {
							result.MyErr = fmt.Errorf("HTTP status %d; expected %d", result.MyStatusCode, options.ExpectedStatus)
						}
					}
					state.complete(job.ticket, result, time.Since(startAt).Seconds(), now.Sub(job.due))
				}
				ready <- jobs
			}
		}()
	}

	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	quantum := min(options.MaxStartDelay, time.Millisecond)
	batchLimit := paceCeilCount(quantum, int64(options.Rate))
	lastDue := paceOffset(planned-1, int64(options.Rate))
	var next int64
	unused := 0
	for next < planned {
		if ctx.Err() != nil {
			state.miss(next, planned, paceCanceled)
			break
		}
		offset := paceOffset(next, int64(options.Rate))
		wakeOffset := min(lastDue, (offset+quantum-1)/quantum*quantum)
		if !paceWait(ctx, timer, startAt.Add(wakeOffset)) {
			state.miss(next, planned, paceCanceled)
			break
		}
		now := time.Now()
		// Only expire slots outside the user's lateness budget. Normal timer
		// jitter must not drop valid slots simply because a quantum elapsed.
		elapsed := max(0, now.Sub(startAt))
		latest := planned - 1
		if elapsed < options.Duration {
			latest = min(latest, paceDueIndex(elapsed, int64(options.Rate)))
		}
		first := next
		if elapsed > options.MaxStartDelay {
			cutoff := elapsed - options.MaxStartDelay
			if cutoff >= options.Duration {
				first = planned
			} else {
				first = max(first, paceCeilCount(cutoff, int64(options.Rate)))
			}
		}
		state.miss(next, first, paceLate)
		next = first
		batchEnd := min(latest, first+min(batchLimit-1, planned-first))
		for next <= batchEnd {
			if ctx.Err() != nil {
				break
			}
			due := startAt.Add(paceOffset(next, int64(options.Rate)))
			if time.Since(due) > options.MaxStartDelay {
				state.miss(next, next+1, paceLate)
				next++
				continue
			}
			// Reuse an available lane before opening another connection. The
			// configured concurrency is a ceiling, not a target pool size.
			var jobs chan paceJob
			select {
			case jobs = <-ready:
			default:
				if unused < len(queues) {
					jobs = queues[unused]
					unused++
				}
			}
			if jobs != nil {
				jobs <- paceJob{ticket: next, due: due}
				next++
			} else {
				state.miss(next, latest+1, paceBusy)
				next = latest + 1
			}
		}
	}
	paceWait(ctx, timer, endAt)
	for _, jobs := range queues {
		close(jobs)
	}
	wg.Wait()
	summary := state.summary(max(0, time.Since(startAt).Seconds()), ctx.Err() != nil)
	return summary, ctx.Err()
}

func validatePaceOptions(o PaceOptions) (int64, error) {
	if o.Rate <= 0 || o.Duration <= 0 || o.Duration > 24*time.Hour || o.Concurrency <= 0 || o.MaxStartDelay <= 0 {
		return 0, fmt.Errorf("rate, duration, concurrency, and max start delay must be positive; duration must not exceed 24h")
	}
	if o.ExpectedStatus != 0 && (o.ExpectedStatus < 100 || o.ExpectedStatus > 599) {
		return 0, fmt.Errorf("expected status must be 0 or between 100 and 599")
	}
	hi, lo := bits.Mul64(uint64(o.Rate), uint64(o.Duration))
	if hi >= uint64(time.Second) {
		return 0, fmt.Errorf("planned request count overflows int64")
	}
	count, rem := bits.Div64(hi, lo, uint64(time.Second))
	if rem != 0 {
		if count == math.MaxUint64 {
			return 0, fmt.Errorf("planned request count overflows int64")
		}
		count++
	}
	if count > math.MaxInt64 {
		return 0, fmt.Errorf("planned request count overflows int64")
	}
	return int64(count), nil
}

func paceOffset(ticket, rate int64) time.Duration {
	seconds, remainder := ticket/rate, ticket%rate
	hi, lo := bits.Mul64(uint64(remainder), uint64(time.Second))
	nanos, _ := bits.Div64(hi, lo, uint64(rate))
	return time.Duration(seconds)*time.Second + time.Duration(nanos)
}

func paceDueIndex(elapsed time.Duration, rate int64) int64 {
	if elapsed <= 0 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(elapsed), uint64(rate))
	q, _ := bits.Div64(hi, lo, uint64(time.Second))
	return int64(q) // The caller bounds elapsed by the validated plan duration.
}

func paceCeilCount(elapsed time.Duration, rate int64) int64 {
	hi, lo := bits.Mul64(uint64(elapsed), uint64(rate))
	q, rem := bits.Div64(hi, lo, uint64(time.Second))
	if rem != 0 {
		q++
	}
	return int64(q)
}

func paceWait(ctx context.Context, timer *time.Timer, until time.Time) bool {
	if ctx.Err() != nil {
		return false
	}
	delay := time.Until(until)
	if delay <= 0 {
		return true
	}
	timer.Reset(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

func newPaceState(options PaceOptions, planned int64) *paceState {
	count := int((options.Duration + time.Second - 1) / time.Second)
	s := &paceState{windows: make([]paceWindowState, count), rate: int64(options.Rate)}
	for i := range s.windows {
		w := &s.windows[i]
		w.Index = i
		w.Seconds = min(time.Second, options.Duration-time.Duration(i)*time.Second).Seconds()
		w.Planned = min(s.rate, planned-int64(i)*s.rate)
	}
	return s
}

func (s *paceState) miss(first, end int64, reason paceMiss) {
	s.Lock()
	defer s.Unlock()
	for first < end {
		w := &s.windows[first/s.rate]
		n := min(end-first, s.rate-first%s.rate)
		w.Missed += n
		switch reason {
		case paceBusy:
			w.BusyMissed += n
		case paceLate:
			w.LateMissed += n
		case paceCanceled:
			w.CanceledMissed += n
		}
		s.finishWindow(w)
		first += n
	}
}

func (s *paceState) complete(ticket int64, result MyResult, completedAt float64, delay time.Duration) {
	s.Lock()
	defer s.Unlock()
	w := &s.windows[ticket/s.rate]
	w.Started++
	paceRecord(&w.delay, max(0, delay.Microseconds()))
	w.Completed++
	if !result.ResponseComplete {
		w.IncompleteResponses++
	}
	w.LastCompletionSeconds = max(w.LastCompletionSeconds, completedAt)
	if result.MyErr == nil {
		w.Succeeded++
	} else {
		w.Failed++
	}
	if result.MyStatusCode != 0 {
		if w.StatusCodes == nil {
			w.StatusCodes = make(map[int]int64)
		}
		w.StatusCodes[result.MyStatusCode]++
	}
	paceRecord(&w.latency, max(0, result.MyDuration.Microseconds()))
	s.finishWindow(w)
}

// Once every planned slot has a final outcome, only the compact window remains.
// Live histograms are bounded by in-flight windows, not the whole run duration.
func (s *paceState) finishWindow(w *paceWindowState) {
	if w.Completed+w.Missed != w.Planned {
		return
	}
	if w.latency != nil {
		w.LatencyP95US = w.latency.ValueAtQuantile(95)
		w.LatencyP99US = w.latency.ValueAtQuantile(99)
		paceMerge(&s.latency, w.latency)
		w.latency = nil
	}
	if w.delay != nil {
		w.StartDelayP99US = w.delay.ValueAtQuantile(99)
		paceMerge(&s.delay, w.delay)
		w.delay = nil
	}
}

func (s *paceState) summary(elapsed float64, canceled bool) *PaceSummary {
	sum := &PaceSummary{ElapsedSeconds: elapsed, Canceled: canceled, Windows: make([]PaceWindow, len(s.windows)), StatusCodes: make(map[int]int64)}
	for i := range s.windows {
		w := s.windows[i].PaceWindow
		sum.Planned += w.Planned
		sum.Started += w.Started
		sum.Completed += w.Completed
		sum.Succeeded += w.Succeeded
		sum.Failed += w.Failed
		sum.IncompleteResponses += w.IncompleteResponses
		sum.Missed += w.Missed
		sum.BusyMissed += w.BusyMissed
		sum.LateMissed += w.LateMissed
		sum.CanceledMissed += w.CanceledMissed
		for code, n := range w.StatusCodes {
			sum.StatusCodes[code] += n
		}
		if w.StatusCodes == nil {
			w.StatusCodes = make(map[int]int64)
		}
		sum.Windows[i] = w
	}
	if s.latency != nil {
		sum.LatencyP95US = s.latency.ValueAtQuantile(95)
		sum.LatencyP99US = s.latency.ValueAtQuantile(99)
	}
	if s.delay != nil {
		sum.StartDelayP99US = s.delay.ValueAtQuantile(99)
	}
	return sum
}

// Start small, then grow instead of silently clipping a late timeout or an
// unusually long response. All histograms keep three significant digits.
func paceHistogramRoom(h **hdr.Histogram, value int64) {
	if *h != nil && value <= (*h).HighestTrackableValue() {
		return
	}
	limit := int64(time.Second / time.Microsecond)
	if *h != nil {
		limit = (*h).HighestTrackableValue()
	}
	for limit < value {
		limit = min(maxLatencyMicros, limit*2)
	}
	grown := hdr.New(1, limit, 3)
	if *h != nil {
		grown.Merge(*h)
	}
	*h = grown
}

func paceRecord(h **hdr.Histogram, value int64) {
	paceHistogramRoom(h, value)
	_ = (*h).RecordValue(value)
}

func paceMerge(to **hdr.Histogram, from *hdr.Histogram) {
	paceHistogramRoom(to, min(maxLatencyMicros, from.Max()))
	(*to).Merge(from)
}
