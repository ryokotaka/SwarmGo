package worker

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryokotaka/SwarmGo/internal/scenario"
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
	benchmarkDirectPlan(b, plan, response)
}

// replayLane is a lane of plan whose connection answers with response.
func replayLane(plan *directPlan, response string) *directLane {
	lane := plan.lane(context.Background())
	conn := &replayConn{response: []byte(response)}
	lane.conn, lane.watched, lane.reader = conn, conn, bufio.NewReaderSize(conn, 4096)
	return lane
}

func benchmarkDirectPlan(b *testing.B, plan *directPlan, response string) {
	lane := replayLane(plan, response)
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

const benchResponse = "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}"

// BenchmarkDirectRequestSmall and BenchmarkDirectScenarioStatic send the same
// POST, plain and as a one-request scenario; the scenario must cost no more.
func BenchmarkDirectRequestSmall(b *testing.B) {
	benchmarkDirectRequest(b, benchResponse)
}

func BenchmarkDirectScenarioStatic(b *testing.B) {
	benchmarkScenario(b, &scenario.Spec{Target: "http://127.0.0.1:8080", Requests: []scenario.RequestSpec{
		{Name: "work", Weight: 1, Method: http.MethodPost, Path: "/work", Headers: map[string]string{"Content-Type": "application/json"}, Body: strings.Repeat("x", 1024)},
	}})
}

// A browse/search/buy mix with a CSV column, a random number and a UUID.
func BenchmarkDirectScenarioMix(b *testing.B) {
	benchmarkScenario(b, benchMixSpec())
}

func benchMixSpec() *scenario.Spec {
	rows := make([][]string, 1000)
	for i := range rows {
		rows[i] = []string{strconv.Itoa(i), "user" + strconv.Itoa(i)}
	}
	return &scenario.Spec{
		Target: "http://127.0.0.1:8080",
		Requests: []scenario.RequestSpec{
			{Name: "browse", Weight: 6, Method: http.MethodGet, Path: "/items/{{random.int(1,100000)}}"},
			{Name: "search", Weight: 3, Method: http.MethodGet, Path: "/search?q={{users.name}}", Headers: map[string]string{"X-User": "{{users.id}}"}},
			{Name: "buy", Weight: 1, Method: http.MethodPost, Path: "/orders", Headers: map[string]string{"Content-Type": "application/json", "Idempotency-Key": "{{random.uuid}}"}, Body: `{"user":{{users.id}},"item":{{random.int(1,100000)}}}`},
		},
		Data: map[string]*scenario.Table{"users": {Columns: []string{"id", "name"}, Rows: rows, Order: "random"}},
	}
}

func benchmarkScenario(b *testing.B, spec *scenario.Spec) {
	plan, err := NewMyRunnerWithConcurrency(1).scenarioPlan(spec, scenario.Position{Worker: 0, Workers: 1})
	if err != nil {
		b.Fatal(err)
	}
	benchmarkDirectPlan(b, plan, benchResponse)
}
