// Command vegeta-check separates two effects in vegeta's reported p99 when
// a server stalls: latency measured from when a request is sent rather than
// when it was due (coordinated omission, with -max-workers set), and the error
// of vegeta's t-digest percentile estimate.
//
// It runs vegeta v12.13.0 as a library against an in-process server that
// answers in 5 ms and holds every request arriving during a stall until the
// stall ends. For each run it prints vegeta's estimated p99, the exact p99 of
// the latencies vegeta measured, the exact p99 of the server's hold times,
// and the exact p99 measured from each request's scheduled time.
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"time"

	vegeta "github.com/tsenart/vegeta/v12/lib"
)

const stall = 200 * time.Millisecond

func main() {
	var mu sync.Mutex
	var start time.Time // Stalls begin at start, start+5s, start+10s, ...
	var held []time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered := time.Now()
		if phase := time.Since(start) % (5 * time.Second); phase < stall {
			time.Sleep(stall - phase)
		}
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		held = append(held, time.Since(entered))
		mu.Unlock()
	}))
	defer srv.Close()

	for _, c := range []struct {
		rate       uint64
		duration   time.Duration
		firstStall time.Duration
		workers    uint64
	}{
		{2000, 3 * time.Second, time.Second, 50},
		{2000, 3 * time.Second, time.Second, vegeta.DefaultMaxWorkers},
		// Two stalls in 10 s: 2 x 1,024 held of 200,000, as in the comparison.
		{20000, 10 * time.Second, 4 * time.Second, 1024},
		{20000, 10 * time.Second, 4 * time.Second, vegeta.DefaultMaxWorkers},
	} {
		mu.Lock()
		held = held[:0]
		mu.Unlock()
		atk := vegeta.NewAttacker(vegeta.MaxWorkers(c.workers))
		tr := vegeta.NewStaticTargeter(vegeta.Target{Method: "GET", URL: srv.URL})
		var m vegeta.Metrics
		var sent, fromDue []time.Duration
		begin := time.Now()
		start = begin.Add(c.firstStall - 5*time.Second)
		for res := range atk.Attack(tr, vegeta.Rate{Freq: int(c.rate), Per: time.Second}, c.duration, "") {
			m.Add(res)
			sent = append(sent, res.Latency)
			due := begin.Add(time.Duration(res.Seq) * time.Second / time.Duration(c.rate))
			fromDue = append(fromDue, res.Timestamp.Add(res.Latency).Sub(due))
		}
		m.Close()
		mu.Lock()
		server := append([]time.Duration(nil), held...)
		mu.Unlock()
		workers := fmt.Sprint(c.workers)
		if c.workers == vegeta.DefaultMaxWorkers {
			workers = "unlimited"
		}
		fmt.Printf("%5d/s  workers %-9s  requests %6d  rate %.0f  success %.0f%%  |  p99: vegeta estimate %4dms, exact %4dms, server-held %4dms, from schedule %4dms\n",
			c.rate, workers, m.Requests, m.Rate, m.Success*100,
			m.Latencies.P99.Milliseconds(), p99(sent), p99(server), p99(fromDue))
	}
}

func p99(d []time.Duration) int64 {
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[len(d)*99/100].Milliseconds()
}
