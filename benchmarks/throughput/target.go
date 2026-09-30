package main

import (
 "bytes"
 "encoding/json"
 "flag"
 "fmt"
 "io"
 "log"
 "net/http"
 "os"
 "strings"
 "sync"
 "sync/atomic"
 "time"
 hdr "github.com/HdrHistogram/hdrhistogram-go"
 "github.com/valyala/fasthttp"
)
var payload = []byte(`{"data":"` + strings.Repeat("x",1013) + `"}`)
func main() {
 mode:=flag.String("mode","server","server, stats or reset")
 flag.Parse()
 if *mode=="server" { server();return }
 if *mode!="stats" && *mode!="reset" {log.Fatal("unknown mode")}
 resp,err:=http.Get("http://127.0.0.1:8080/"+*mode)
 if err!=nil {log.Fatal(err)}
 defer resp.Body.Close()
 io.Copy(os.Stdout,resp.Body)
}
func server() {
	var requests, failures, first, last, bodyBytes, active, peak atomic.Int64
	// Requests that ask for an injected delay or stall also record how long
	// the target held them, the ground truth for latency-accuracy runs.
	var heldMu sync.Mutex
	held := hdr.New(1, 60_000_000, 3)
	// monotonic_ns cannot step when the VM adjusts its wall clock, so rates
	// computed from it are immune to clock syncs during a measurement.
	started := time.Now()
	s := &fasthttp.Server{NoDefaultServerHeader: true, Handler: func(ctx *fasthttp.RequestCtx) {
		switch string(ctx.Path()) {
		case "/reset":
			requests.Store(0)
			failures.Store(0)
			first.Store(0)
			last.Store(0)
			bodyBytes.Store(0)
			active.Store(0)
			peak.Store(0)
			heldMu.Lock()
			held.Reset()
			heldMu.Unlock()
			ctx.SetBodyString("ok")
			return
		case "/stats":
			stats := map[string]any{"snapshot_unix_ns": time.Now().UnixNano(), "monotonic_ns": time.Since(started).Nanoseconds(), "requests": requests.Load(), "invalid": failures.Load(), "body_bytes": bodyBytes.Load(), "active_requests": active.Load(), "peak_active_requests": peak.Load(), "elapsed_seconds": float64(last.Load()-first.Load()) / 1e9}
			heldMu.Lock()
			if n := held.TotalCount(); n > 0 {
				stats["held_count"] = n
				for _, q := range []float64{50, 90, 95, 99, 99.9} {
					stats[fmt.Sprintf("held_p%g_us", q)] = held.ValueAtQuantile(q)
				}
				stats["held_max_us"] = held.Max()
			}
			heldMu.Unlock()
			b, _ := json.Marshal(stats)
			ctx.SetBody(b)
			return
		case "/work":
		default:
			ctx.SetStatusCode(404)
			return
		}
		first.CompareAndSwap(0, time.Now().UnixNano())
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		bodyBytes.Add(int64(len(ctx.PostBody())))
		if !ctx.IsPost() || !bytes.Equal(ctx.PostBody(), payload) || string(ctx.Request.Header.Protocol()) != "HTTP/1.1" {
			failures.Add(1)
			ctx.SetStatusCode(400)
		} else {
			if len(ctx.URI().QueryString()) > 0 {
				entered := time.Now()
				args := ctx.QueryArgs()
				// stall_ms of every stall_every_ms, counted from server start,
				// holds each request that arrives until the stall ends.
				if every, stall := args.GetUintOrZero("stall_every_ms"), args.GetUintOrZero("stall_ms"); every > 0 && stall > 0 {
					phase := time.Since(started) % (time.Duration(every) * time.Millisecond)
					if hold := time.Duration(stall)*time.Millisecond - phase; hold > 0 {
						time.Sleep(hold)
					}
				}
				delay := time.Duration(args.GetUintOrZero("delay_ms"))*time.Millisecond + time.Duration(args.GetUintOrZero("delay_us"))*time.Microsecond
				if delay > 0 {
					time.Sleep(delay)
				}
				heldMu.Lock()
				held.RecordValue(time.Since(entered).Microseconds())
				heldMu.Unlock()
			}
			ctx.SetContentType("application/json")
			ctx.Response.SetBodyRaw(payload)
		}
		requests.Add(1)
		last.Store(time.Now().UnixNano())
	}}
	log.Fatal(s.ListenAndServe(":8080"))
}
