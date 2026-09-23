// A local demonstration API with limited processing capacity and an optional
// per-client admission limit. This is a test fixture, not a production limiter.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

func main() {
	addr := flag.String("listen", "127.0.0.1:8080", "Listen address")
	limit := flag.Int("limit", 0, "Requests per second per test client; 0 disables admission control")
	check := flag.Bool("check", false, "Check the local fixture's readiness and exit")
	flag.Parse()
	if *check {
		client := &http.Client{Timeout: time.Second}
		response, err := client.Get("http://127.0.0.1:8080/health")
		if err != nil {
			log.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			log.Fatal(response.Status)
		}
		return
	}
	if *limit < 0 {
		log.Fatal("limit must not be negative")
	}
	capacity := make(chan struct{}, 8)
	var mu sync.Mutex
	// Two fixed test identities avoid an unbounded client map in this fixture.
	buckets := [2]bucket{}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ready") })
	mux.HandleFunc("/work", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 1<<20)); err != nil {
			http.Error(w, "invalid body", 400)
			return
		}
		client := 0
		if r.Header.Get("X-Test-Client") == "ordinary" {
			client = 1
		}
		if *limit > 0 {
			mu.Lock()
			b := &buckets[client]
			now := time.Now()
			if b.last.IsZero() {
				b.tokens = 4
			} else {
				b.tokens = min(4, b.tokens+now.Sub(b.last).Seconds()*float64(*limit))
			}
			b.last = now
			allowed := b.tokens >= 1
			if allowed {
				b.tokens--
			}
			mu.Unlock()
			if !allowed {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "rate limited", 429)
				return
			}
		}
		select {
		case capacity <- struct{}{}:
			defer func() { <-capacity }()
		case <-r.Context().Done():
			return
		}
		timer := time.NewTimer(80 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	})
	server := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	fmt.Printf("Local fixture: %s, 8 processing slots, 80ms/request, admission limit %d/s/client\n", *addr, *limit)
	log.Fatal(server.ListenAndServe())
}
