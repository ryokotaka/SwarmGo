package worker

import (
	"sync"
	"time"
)

// Per-request timeouts are enforced by one watchdog per run instead of socket
// deadlines. Setting a deadline on every request modifies runtime timers twice
// (read and write) on the hot path; the watchdog costs each request two atomic
// stores and checks all lanes a few times per timeout period. A late request
// fails at most one check interval after its timeout.

// errRequestTimeout reports a request that exceeded the client timeout.
var errRequestTimeout error = requestTimeoutError{}

type requestTimeoutError struct{}

func (requestTimeoutError) Error() string   { return "request exceeded client timeout (i/o timeout)" }
func (requestTimeoutError) Timeout() bool   { return true }
func (requestTimeoutError) Temporary() bool { return true }

// laneExpired marks a lane whose current request the watchdog timed out.
const laneExpired = -1

type laneWatch struct {
	mu    sync.Mutex
	epoch time.Time
	lanes map[*directLane]struct{}
	stop  chan struct{}
}

// watchInterval bounds how late a timeout can fire: 1% of the timeout,
// between 1 ms and 50 ms (50 ms for the default 30-second timeout).
func watchInterval(timeout time.Duration) time.Duration {
	return min(max(timeout/100, time.Millisecond), 50*time.Millisecond)
}

// register starts the watchdog with the first lane of a run.
func (p *directPlan) register(l *directLane) {
	w := &p.watch
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lanes == nil {
		w.lanes = make(map[*directLane]struct{})
		w.epoch = time.Now()
	}
	w.lanes[l] = struct{}{}
	if w.stop == nil {
		w.stop = make(chan struct{})
		go p.watchLanes(w.stop)
	}
}

// unregister stops the watchdog when the last lane of a run finishes.
func (p *directPlan) unregister(l *directLane) {
	w := &p.watch
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.lanes, l)
	if len(w.lanes) == 0 && w.stop != nil {
		close(w.stop)
		w.stop = nil
	}
}

func (p *directPlan) watchLanes(stop <-chan struct{}) {
	tick := time.NewTicker(watchInterval(p.timeout))
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		w := &p.watch
		w.mu.Lock()
		now := int64(time.Since(w.epoch))
		for l := range w.lanes {
			l.expireIfLate(now)
		}
		w.mu.Unlock()
	}
}

// begin records the start of a request. The value is never zero (idle) or
// laneExpired, so the watchdog and the lane can claim it with one CAS each.
func (l *directLane) begin(start time.Time) int64 {
	if l.plan.timeout <= 0 {
		return 0
	}
	token := max(int64(start.Sub(l.plan.watch.epoch)), 0) + 1
	l.started.Store(token)
	return token
}

// end reports whether the request finished before the watchdog timed it out.
// A request whose response arrived after its timeout is still a timeout, as it
// would be with a socket deadline.
func (l *directLane) end(token int64) bool {
	if token == 0 || l.started.CompareAndSwap(token, 0) {
		return true
	}
	l.started.Store(0)
	return false
}

// timedOut reports whether the watchdog has closed the current request's socket.
func (l *directLane) timedOut() bool {
	return l.started.Load() == laneExpired
}

func (l *directLane) expireIfLate(now int64) {
	started := l.started.Load()
	if started <= 0 || now-started < int64(l.plan.timeout) || !l.started.CompareAndSwap(started, laneExpired) {
		return
	}
	l.mu.Lock()
	if l.watched != nil {
		l.watched.Close()
	}
	l.mu.Unlock()
}
