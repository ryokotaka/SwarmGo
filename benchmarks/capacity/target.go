package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func main() {
	if len(os.Args) > 1 {
		resp, err := http.Get("http://127.0.0.1:8080/" + os.Args[1])
		if err != nil {
			log.Fatal(err)
		}
		defer resp.Body.Close()
		io.Copy(os.Stdout, resp.Body)
		return
	}
	var requests, failures, first, last, bodyBytes, active, peak, connections atomic.Int64
	payload := []byte(`{"data":"` + strings.Repeat("x", 1013) + `"}`)
	small := []byte(`{"data":"` + strings.Repeat("x", 117) + `"}`)
	http.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		requests.Store(0)
		failures.Store(0)
		first.Store(0)
		last.Store(0)
		bodyBytes.Store(0)
		active.Store(0)
		peak.Store(0)
		connections.Store(0)
		w.Write([]byte("ok"))
	})
	http.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"requests": requests.Load(), "invalid": failures.Load(), "body_bytes": bodyBytes.Load(), "active_requests": active.Load(), "peak_active_requests": peak.Load(), "tcp_connections": connections.Load(), "elapsed_seconds": float64(last.Load()-first.Load()) / 1e9})
	})
	http.HandleFunc("/work", func(w http.ResponseWriter, r *http.Request) {
		first.CompareAndSwap(0, time.Now().UnixNano())
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		b, err := io.ReadAll(r.Body)
		r.Body.Close()
		bodyBytes.Add(int64(len(b)))
		ok := err == nil && r.Proto == "HTTP/1.1"
		if r.Method == "POST" {
			ok = ok && bytes.Equal(b, payload)
		} else {
			ok = ok && r.Method == "GET" && len(b) == 0
		}
		if !ok {
			failures.Add(1)
			http.Error(w, "invalid request", 400)
		} else {
			delay, _ := strconv.Atoi(r.URL.Query().Get("delay_ms"))
			if delay > 0 {
				time.Sleep(time.Duration(delay) * time.Millisecond)
			}
			w.Header().Set("Content-Type", "application/json")
			if r.Method == "POST" {
				w.Write(payload)
			} else {
				w.Write(small)
			}
		}
		requests.Add(1)
		last.Store(time.Now().UnixNano())
	})
	server := &http.Server{Addr: ":8080", ConnState: func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			connections.Add(1)
		}
	}}
	log.Fatal(server.ListenAndServe())
}
