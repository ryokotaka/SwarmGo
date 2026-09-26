package worker

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func directTestRunner(t *testing.T) *MyRunner {
	t.Helper()
	runner := NewMyRunnerWithConcurrency(1)
	if _, ok := runner.MyClient.Transport.(*directTransport); !ok {
		t.Fatal("default runner did not select the direct transport")
	}
	runner.MyClient.Timeout = 2 * time.Second
	t.Cleanup(runner.MyClient.CloseIdleConnections)
	return runner
}

func directTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})
	return srv
}

func directTestRun(t *testing.T, runner *MyRunner, target string, count int, options RequestOptions) *MySummary {
	t.Helper()
	summary, err := runner.MyRunWithOptions(context.Background(), target, count, 1, options, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.MyTotal != count {
		t.Fatalf("completed %d requests; want %d", summary.MyTotal, count)
	}
	return summary
}

func directRawServer(t *testing.T, response string) *httptest.Server {
	t.Helper()
	return directTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		if _, err := io.WriteString(conn, response); err != nil {
			t.Errorf("write raw response: %v", err)
		}
	})
}

func TestDirectChunkedResponseFraming(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		failed     bool
	}{
		{"valid_trailer", "5\r\nhello\r\n0\r\nX-End: yes\r\n\r\n", false},
		{"missing_terminal_chunk", "5\r\nhello\r\n", true},
		{"missing_trailer_terminator", "5\r\nhello\r\n0\r\nX-End: yes\r\n", true},
		{"truncated_chunk", "5\r\nhel", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := directRawServer(t, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nTrailer: X-End\r\n\r\n"+tc.body)
			summary := directTestRun(t, directTestRunner(t), srv.URL, 1, RequestOptions{})
			wantFailed := 0
			if tc.failed {
				wantFailed = 1
			}
			if summary.MyFailed != wantFailed || summary.MySuccess != 1-wantFailed || summary.MyStatusCodeCnt[200] != 1 {
				t.Fatalf("failed=%v: %+v", tc.failed, summary)
			}
		})
	}
}

func TestDirectHEADReusesConnectionWithoutReadingAdvertisedBody(t *testing.T) {
	var connections atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method=%s; want HEAD", r.Method)
		}
		w.Header().Set("Content-Length", "1048576")
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	runner := directTestRunner(t)
	runner.MyClient.Timeout = 500 * time.Millisecond
	summary := directTestRun(t, runner, srv.URL, 3, RequestOptions{Method: http.MethodHead})
	if summary.MySuccess != 3 || summary.MyFailed != 0 || connections.Load() != 1 {
		t.Fatalf("summary=%+v connections=%d; want 3 successes on 1 connection", summary, connections.Load())
	}
}

func TestDirectConsumesInformationalResponses(t *testing.T) {
	srv := directRawServer(t, "HTTP/1.1 103 Early Hints\r\nLink: </style.css>; rel=preload\r\n\r\n"+
		"HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
	summary := directTestRun(t, directTestRunner(t), srv.URL, 1, RequestOptions{})
	if summary.MySuccess != 1 || summary.MyStatusCodeCnt[200] != 1 || len(summary.MyStatusCodeCnt) != 1 {
		t.Fatalf("informational response counted as final: %+v", summary)
	}
}

func TestDirectGzipMatchesAutomaticAndExplicitEncoding(t *testing.T) {
	var buffer bytes.Buffer
	zw := gzip.NewWriter(&buffer)
	if _, err := io.WriteString(zw, strings.Repeat("payload", 1000)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	valid := buffer.Bytes()
	corrupt := bytes.Clone(valid)
	corrupt[len(corrupt)-1] ^= 0xff
	for _, tc := range []struct {
		name, explicit string
		body           []byte
		failed         bool
	}{
		{"automatic_valid", "", valid, false},
		{"automatic_corrupt", "", corrupt, true},
		{"explicit_gzip_is_not_decoded", "gzip", corrupt, false},
		{"explicit_identity_is_not_decoded", "identity", corrupt, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := directTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				want := tc.explicit
				if want == "" {
					want = "gzip"
				}
				if got := r.Header.Get("Accept-Encoding"); got != want {
					t.Errorf("Accept-Encoding=%q; want %q", got, want)
				}
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)))
				w.Write(tc.body)
			})
			options := RequestOptions{}
			if tc.explicit != "" {
				options.Headers = map[string]string{"Accept-Encoding": tc.explicit}
			}
			summary := directTestRun(t, directTestRunner(t), srv.URL, 1, options)
			wantFailed := 0
			if tc.failed {
				wantFailed = 1
			}
			if summary.MyFailed != wantFailed || summary.MySuccess != 1-wantFailed || summary.MyStatusCodeCnt[200] != 1 {
				t.Fatalf("failed=%v: %+v", tc.failed, summary)
			}
		})
	}
}

func TestDirectCancellationInterruptsResponseBody(t *testing.T) {
	bodyStarted, serverCanceled := make(chan struct{}), make(chan struct{})
	srv := directTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		close(bodyStarted)
		<-r.Context().Done()
		close(serverCanceled)
	})
	runner := directTestRunner(t)
	runner.MyClient.Timeout = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		summary *MySummary
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		summary, err := runner.MyRun(ctx, srv.URL, 1, 1, nil)
		done <- outcome{summary, err}
	}()
	select {
	case <-bodyStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("response body did not start")
	}
	cancel()
	select {
	case result := <-done:
		if result.err != nil || result.summary == nil || result.summary.MyTotal != 1 || result.summary.MyFailed != 1 {
			t.Fatalf("canceled body: summary=%+v err=%v", result.summary, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation left the response body read blocked")
	}
	select {
	case <-serverCanceled:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not close the active connection")
	}
}

func TestDirectTLSVerifiesServerCertificate(t *testing.T) {
	t.Setenv("INSECURE_SKIP_VERIFY", "")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	}))
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	for _, trusted := range []bool{false, true} {
		t.Run(strconv.FormatBool(trusted), func(t *testing.T) {
			runner := directTestRunner(t)
			if trusted {
				roots := x509.NewCertPool()
				roots.AddCert(srv.Certificate())
				runner.MyClient.Transport.(*directTransport).base.TLSClientConfig = &tls.Config{RootCAs: roots}
			}
			summary := directTestRun(t, runner, srv.URL, 1, RequestOptions{})
			if (summary.MySuccess == 1) != trusted {
				t.Fatalf("trusted=%v: %+v", trusted, summary)
			}
			if !trusted && (summary.MyFailed != 1 || summary.MyFirstErr == nil || !strings.Contains(summary.MyFirstErr.Error(), "certificate")) {
				t.Fatalf("untrusted TLS failed for an unexpected reason: %+v", summary)
			}
		})
	}
}

func TestDirectRedirectPreservesMethodRulesWithoutDuplicateInitialPOST(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusSeeOther, http.StatusTemporaryRedirect} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var initial, final atomic.Int32
			const body = `{"message":"hello"}`
			srv := directTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				got, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request: %v", err)
				}
				if r.URL.Path == "/start" {
					initial.Add(1)
					if r.Method != http.MethodPost || string(got) != body {
						t.Errorf("initial request: method=%s body=%q", r.Method, got)
					}
					http.Redirect(w, r, "/finish", status)
					return
				}
				final.Add(1)
				wantMethod, wantBody := http.MethodGet, ""
				if status == http.StatusTemporaryRedirect {
					wantMethod, wantBody = http.MethodPost, body
				}
				if r.Method != wantMethod || string(got) != wantBody {
					t.Errorf("redirect request: method=%s body=%q; want %s %q", r.Method, got, wantMethod, wantBody)
				}
				io.WriteString(w, "ok")
			})
			summary := directTestRun(t, directTestRunner(t), srv.URL+"/start", 2, RequestOptions{Method: http.MethodPost, Body: []byte(body)})
			if summary.MySuccess != 2 || initial.Load() != 2 || final.Load() != 2 {
				t.Fatalf("summary=%+v initial=%d final=%d; want 2 of each", summary, initial.Load(), final.Load())
			}
		})
	}
}

func TestDirectRedirectStripsCredentialsAcrossHosts(t *testing.T) {
	var initial, final atomic.Int32
	destination := directTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		final.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("redirect leaked credentials to a different host")
		}
		io.WriteString(w, "ok")
	})
	origin := directTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		initial.Add(1)
		if r.Header.Get("Authorization") != "Bearer private" || r.Header.Get("Cookie") != "session=private" {
			t.Error("initial request lost its credentials")
		}
		http.Redirect(w, r, destination.URL, http.StatusFound)
	})
	// Different ports alone do not trigger net/http's cross-host credential policy.
	originURL := strings.Replace(origin.URL, "127.0.0.1", "localhost", 1)
	summary := directTestRun(t, directTestRunner(t), originURL, 1, RequestOptions{
		Headers: map[string]string{"Authorization": "Bearer private", "Cookie": "session=private"},
	})
	if summary.MySuccess != 1 || initial.Load() != 1 || final.Load() != 1 {
		t.Fatalf("summary=%+v initial=%d final=%d", summary, initial.Load(), final.Load())
	}
}

func TestDirectRedirectSharesWholeRequestTimeout(t *testing.T) {
	var redirected atomic.Bool
	srv := directTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/finish" {
			redirected.Store(true)
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/finish", http.StatusFound)
			return
		}
		io.WriteString(w, "ok")
	})
	runner := directTestRunner(t)
	runner.MyClient.Timeout = 800 * time.Millisecond
	summary := directTestRun(t, runner, srv.URL+"/start", 1, RequestOptions{})
	if !redirected.Load() {
		t.Fatal("request never reached the redirect destination")
	}
	if summary.MyFailed != 1 || summary.MySuccess != 0 {
		t.Fatalf("redirect restarted the timeout instead of sharing the original deadline: %+v", summary)
	}
}

func TestDirectTimeoutFailsSlowResponsesWithoutReplay(t *testing.T) {
	for _, stall := range []string{"header", "body"} {
		t.Run(stall, func(t *testing.T) {
			var requests atomic.Int32
			srv := directTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				// The first request only opens a reusable connection.
				if requests.Add(1) == 1 {
					io.WriteString(w, "ok")
					return
				}
				if stall == "body" {
					w.Header().Set("Content-Length", "4")
					io.WriteString(w, "ok")
					w.(http.Flusher).Flush()
				}
				select {
				case <-time.After(5 * time.Second):
				case <-r.Context().Done():
				}
			})
			runner := directTestRunner(t)
			runner.MyClient.Timeout = 200 * time.Millisecond
			started := time.Now()
			summary := directTestRun(t, runner, srv.URL, 2, RequestOptions{})
			elapsed := time.Since(started)
			if summary.MySuccess != 1 || summary.MyFailed != 1 || summary.MyErrorReasons["timeout"] != 1 {
				t.Fatalf("summary=%+v; want one success and one timeout", summary)
			}
			// A reused GET that times out must not be replayed as if the idle
			// connection had been closed by the server.
			if requests.Load() != 2 {
				t.Fatalf("server saw %d requests; want 2", requests.Load())
			}
			if limit := 200*time.Millisecond + watchInterval(200*time.Millisecond) + time.Second; elapsed > limit {
				t.Fatalf("run took %v; timeout should fire near 200ms", elapsed)
			}
		})
	}
}

func TestDirectTimeoutClosesOnlyTheLateConnection(t *testing.T) {
	var requests atomic.Int32
	srv := directTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			select {
			case <-time.After(5 * time.Second):
			case <-r.Context().Done():
			}
			return
		}
		io.WriteString(w, "ok")
	})
	runner := directTestRunner(t)
	runner.MyClient.Timeout = 200 * time.Millisecond
	summary := directTestRun(t, runner, srv.URL, 4, RequestOptions{})
	if summary.MyFailed != 1 || summary.MySuccess != 3 {
		t.Fatalf("summary=%+v; want the lane to reconnect after one timeout", summary)
	}
}

func TestDirectWatchdogStopsWithLastLane(t *testing.T) {
	template, err := requestTemplate("http://127.0.0.1:1/", RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewMyRunnerWithConcurrency(2).directPlan(template)
	if err != nil || plan == nil {
		t.Fatalf("direct plan unavailable: %v", err)
	}
	first, second := plan.lane(context.Background()), plan.lane(context.Background())
	if plan.watch.stop == nil {
		t.Fatal("watchdog did not start with the first lane")
	}
	first.finish()
	if plan.watch.stop == nil {
		t.Fatal("watchdog stopped while a lane was still running")
	}
	second.finish()
	if plan.watch.stop != nil {
		t.Fatal("watchdog still running after the last lane finished")
	}
}
