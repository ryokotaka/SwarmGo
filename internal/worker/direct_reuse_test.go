package worker

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDirectReconnectsAfterUnannouncedIdleClose(t *testing.T) {
	srv := directRawServer(t, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
	sum := directTestRun(t, directTestRunner(t), srv.URL, 5, RequestOptions{})
	if sum.MySuccess != 5 || sum.MyFailed != 0 {
		t.Fatalf("idle close lost GETs: %+v", sum)
	}
}

func TestDirectLargeHeadersAndStreamedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Large", strings.Repeat("x", 12000))
		w.Header().Set("Content-Length", "1048576")
		for range 1024 {
			io.WriteString(w, strings.Repeat("b", 1024))
		}
	}))
	defer srv.Close()
	sum := directTestRun(t, directTestRunner(t), srv.URL, 3, RequestOptions{})
	if sum.MySuccess != 3 || sum.MyFailed != 0 {
		t.Fatalf("large response failed: %+v", sum)
	}
}

func TestDirectHEADWithoutContentLengthKeepsAlive(t *testing.T) {
	runner := directTestRunner(t)
	// Normal server supplies no Content-Length for HEAD when no body is written.
	var connections atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	sum, err := runner.MyRunWithOptions(context.Background(), srv.URL, 3, 1, RequestOptions{Method: "HEAD"}, nil)
	if err != nil || sum.MySuccess != 3 {
		t.Fatalf("HEAD failed: %+v %v", sum, err)
	}
	if connections.Load() != 1 {
		t.Fatalf("HEAD without body dropped reusable socket")
	}
}

func TestDirectFollowsRedirectWithoutWaitingForItsLargeBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/finish" {
			io.WriteString(w, "ok")
			return
		}
		w.Header().Set("Location", "/finish")
		w.Header().Set("Content-Length", "100000")
		w.WriteHeader(302)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	sum := directTestRun(t, directTestRunner(t), srv.URL, 1, RequestOptions{})
	if sum.MySuccess != 1 || sum.MyStatusCodeCnt[200] != 1 {
		t.Fatalf("redirect stalled on irrelevant body: %+v", sum)
	}
}

func TestDirectRejectsCorruptMixedCaseGzip(t *testing.T) {
	srv := directRawServer(t, "HTTP/1.1 200 OK\r\nContent-Length: 3\r\nContent-Encoding: GZip\r\n\r\nbad")
	sum := directTestRun(t, directTestRunner(t), srv.URL, 1, RequestOptions{})
	if sum.MyFailed != 1 {
		t.Fatalf("corrupt GZip succeeded: %+v", sum)
	}
}
