package worker

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// replayConn answers every request write with one canned response. It keeps
// the kernel and network out of the measurement, so the benchmark isolates the
// per-request CPU cost of the direct HTTP/1.1 path itself.
type replayConn struct {
	net.Conn
	response []byte
	pending  []byte
}

func (c *replayConn) Write(b []byte) (int, error) {
	c.pending = c.response
	return len(b), nil
}

func (c *replayConn) Read(b []byte) (int, error) {
	n := copy(b, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *replayConn) Close() error                     { return nil }
func (c *replayConn) SetDeadline(time.Time) error      { return nil }
func (c *replayConn) SetReadDeadline(time.Time) error  { return nil }
func (c *replayConn) SetWriteDeadline(time.Time) error { return nil }

func benchmarkDirectRequest(b *testing.B, response string) {
	body := strings.Repeat("x", 1024)
	template, err := requestTemplate("http://127.0.0.1:8080/work", RequestOptions{
		Method: http.MethodPost, Body: []byte(body), Headers: map[string]string{"Content-Type": "application/json"},
	})
	if err != nil {
		b.Fatal(err)
	}
	runner := NewMyRunnerWithConcurrency(1)
	plan, err := runner.directPlan(template)
	if err != nil || plan == nil {
		b.Fatalf("direct plan unavailable: %v", err)
	}
	lane := plan.lane(context.Background())
	conn := &replayConn{response: []byte(response)}
	lane.conn, lane.watched, lane.reader = conn, conn, bufio.NewReaderSize(conn, 4096)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if result := lane.execute(); result.MyErr != nil || !result.ResponseComplete {
			b.Fatalf("request failed: %+v", result)
		}
	}
}

// A typical JSON API response: known length, a handful of headers.
func BenchmarkDirectRequestContentLength(b *testing.B) {
	payload := strings.Repeat("x", 1024)
	benchmarkDirectRequest(b, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nDate: Thu, 25 Sep 2026 00:00:00 GMT\r\n"+
		"Server: bench\r\nCache-Control: no-store\r\nContent-Length: 1024\r\n\r\n"+payload)
}

func BenchmarkDirectRequestChunked(b *testing.B) {
	benchmarkDirectRequest(b, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nTransfer-Encoding: chunked\r\n\r\n"+
		"400\r\n"+strings.Repeat("x", 1024)+"\r\n0\r\n\r\n")
}
