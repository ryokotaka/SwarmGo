package worker

import (
	"context"
	"math"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	hdr "github.com/HdrHistogram/hdrhistogram-go"
)

// Quantiles use microseconds and three significant digits (up to 0.1% value
// quantization, plus less than 1 us from conversion). Space is independent of
// the request count. Shards own their histograms, never one histogram per lane.
const maxLatencyMicros = math.MaxInt64 / int64(time.Microsecond)

func latencyHistogram() *hdr.Histogram { return hdr.New(1, maxLatencyMicros, 3) }

type resultShard struct {
	sync.Mutex
	sum       MySummary
	histogram *hdr.Histogram
}

func (s *resultShard) add(batch []MyResult) {
	s.Lock()
	defer s.Unlock()
	for _, res := range batch {
		s.sum.MyTotal++
		if res.MyStatusCode != 0 {
			s.sum.MyStatusCodeCnt[res.MyStatusCode]++
		}
		if res.MyErr != nil {
			s.sum.MyFailed++
			if s.sum.MyFirstErr == nil {
				s.sum.MyFirstErr = res.MyErr
			}
			s.sum.MyErrorReasons[errorReasonString(res)]++
		} else {
			s.sum.MySuccess++
			s.sum.MyTotalDuration += res.MyDuration
			// time.Duration cannot exceed the histogram's configured range.
			_ = s.histogram.RecordValue(max(0, res.MyDuration.Microseconds()))
		}
	}
}

func (r *MyRunner) runLanes(ctx context.Context, template *http.Request, total, concurrency int, onProgress OnProgressFunc) (*MySummary, error) {
	plan, err := r.directPlan(template)
	if err != nil {
		return nil, err
	}
	if plan != nil && plan.transport.base.MaxConnsPerHost > 0 {
		concurrency = min(concurrency, plan.transport.base.MaxConnsPerHost)
	}
	started := time.Now()
	shards := make([]resultShard, min(concurrency, runtime.GOMAXPROCS(0)))
	for i := range shards {
		shards[i].histogram = latencyHistogram()
		shards[i].sum.MyStatusCodeCnt = make(map[int]int)
		shards[i].sum.MyErrorReasons = make(map[string]int)
	}
	done, progressDone := make(chan struct{}), make(chan struct{})
	if onProgress != nil {
		go func() {
			defer close(progressDone)
			tick := time.NewTicker(200 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-done:
					return
				case <-tick.C:
					var completed, success, failed int
					for i := range shards {
						s := &shards[i]
						s.Lock()
						completed += s.sum.MyTotal
						success += s.sum.MySuccess
						failed += s.sum.MyFailed
						s.Unlock()
					}
					onProgress(completed, success, failed, time.Since(started))
				}
			}
		}()
	} else {
		close(progressDone)
	}
	// Amortize work allocation on long runs. Short runs keep one-request
	// leases so high-concurrency tests do not end with large uneven tails.
	leaseSize := int64(1)
	if total/concurrency >= 4096 {
		leaseSize = 32
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(shard *resultShard) {
			defer wg.Done()
			var lane *directLane
			if plan != nil {
				lane = plan.lane(ctx)
				defer lane.finish()
			}
			var batch [32]MyResult
			count := 0
			var pendingTime time.Duration
			var ticket, end int64
			for ctx.Err() == nil {
				if ticket == end {
					end = next.Add(leaseSize)
					ticket = end - leaseSize
					end = min(end, int64(total))
					if ticket >= end {
						break
					}
				}
				ticket++
				if ctx.Err() != nil {
					break
				}
				if lane != nil {
					batch[count] = lane.execute()
				} else {
					batch[count] = r.executeRequest(ctx, template)
				}
				pendingTime += batch[count].MyDuration
				count++
				if count == len(batch) || pendingTime >= 200*time.Millisecond {
					shard.add(batch[:count])
					clear(batch[:count])
					count = 0
					pendingTime = 0
				}
			}
			if count > 0 {
				shard.add(batch[:count])
			}
		}(&shards[i%len(shards)])
	}
	wg.Wait()
	elapsed := time.Since(started)
	close(done)
	<-progressDone
	sum := &MySummary{Elapsed: elapsed, MyStatusCodeCnt: make(map[int]int), MyErrorReasons: make(map[string]int)}
	histogram := latencyHistogram()
	for i := range shards {
		s := &shards[i].sum
		sum.MyTotal += s.MyTotal
		sum.MySuccess += s.MySuccess
		sum.MyFailed += s.MyFailed
		sum.MyTotalDuration += s.MyTotalDuration
		if sum.MyFirstErr == nil {
			sum.MyFirstErr = s.MyFirstErr
		}
		for k, v := range s.MyStatusCodeCnt {
			sum.MyStatusCodeCnt[k] += v
		}
		for k, v := range s.MyErrorReasons {
			sum.MyErrorReasons[k] += v
		}
		histogram.Merge(shards[i].histogram)
	}
	if sum.MySuccess > 0 {
		sum.LatencyP50 = time.Duration(min(maxLatencyMicros, histogram.ValueAtQuantile(50))) * time.Microsecond
		sum.LatencyP90 = time.Duration(min(maxLatencyMicros, histogram.ValueAtQuantile(90))) * time.Microsecond
		sum.LatencyP99 = time.Duration(min(maxLatencyMicros, histogram.ValueAtQuantile(99))) * time.Microsecond
	}
	if onProgress != nil {
		onProgress(sum.MyTotal, sum.MySuccess, sum.MyFailed, elapsed)
	}
	return sum, nil
}
