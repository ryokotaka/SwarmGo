package main

import (
 "bytes"
 "encoding/json"
 "flag"
 "io"
 "log"
 "net/http"
 "os"
 "strings"
 "sync/atomic"
 "time"
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
			ctx.SetBodyString("ok")
			return
		case "/stats":
			b, _ := json.Marshal(map[string]any{"snapshot_unix_ns": time.Now().UnixNano(), "requests": requests.Load(), "invalid": failures.Load(), "body_bytes": bodyBytes.Load(), "active_requests": active.Load(), "peak_active_requests": peak.Load(), "elapsed_seconds": float64(last.Load()-first.Load()) / 1e9})
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
			delay := ctx.QueryArgs().GetUintOrZero("delay_ms")
			if delay > 0 {
				time.Sleep(time.Duration(delay) * time.Millisecond)
			}
			ctx.SetContentType("application/json")
			ctx.Response.SetBodyRaw(payload)
		}
		requests.Add(1)
		last.Store(time.Now().UnixNano())
	}}
	log.Fatal(s.ListenAndServe(":8080"))
}
