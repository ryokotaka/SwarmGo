package worker

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryokotaka/SwarmGo/proto"
	"google.golang.org/grpc"
	wireproto "google.golang.org/protobuf/proto"
)

func TestSummaryStatsPreservesLatencyPrecisionAndPresenceOnWire(t *testing.T) {
	for _, tc := range []struct {
		name    string
		success int
		us      [3]int64
		ms      [3]int32
	}{
		{name: "sub-millisecond", success: 1, us: [3]int64{125, 750, 900}},
		{name: "legacy milliseconds retained", success: 1, us: [3]int64{125, 1750, 2900}, ms: [3]int32{0, 1, 2}},
		{name: "measured zero", success: 1},
		{name: "no successful samples"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stats := summaryStats(&MySummary{
				MyTotal: 1, MySuccess: tc.success, MyFailed: 1 - tc.success,
				LatencyP50: time.Duration(tc.us[0]) * time.Microsecond,
				LatencyP90: time.Duration(tc.us[1]) * time.Microsecond,
				LatencyP99: time.Duration(tc.us[2]) * time.Microsecond,
			})
			encoded, err := wireproto.Marshal(&proto.WorkerMsg{Msg: &proto.WorkerMsg_Stats{Stats: stats}})
			if err != nil {
				t.Fatal(err)
			}
			var decoded proto.WorkerMsg
			if err := wireproto.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			got := decoded.GetStats()
			if got == nil || (got.LatencyUs != nil) != (tc.success > 0) {
				t.Fatalf("latency presence lost on wire: %v", got)
			}
			if latency := got.LatencyUs; latency != nil && [3]int64{latency.P50, latency.P90, latency.P99} != tc.us {
				t.Fatalf("microseconds changed: got %v, want %v", latency, tc.us)
			}
			if [3]int32{got.LatencyP50Ms, got.LatencyP90Ms, got.LatencyP99Ms} != tc.ms {
				t.Fatalf("legacy millisecond fields changed: %v", got)
			}
		})
	}
}

type testMaster struct {
	proto.UnimplementedSwarmServiceServer
	commands chan *proto.MasterCmd
	reports  chan *proto.WorkerMsg
}

func (m *testMaster) Connect(stream proto.SwarmService_ConnectServer) error {
	recvErr := make(chan error, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			select {
			case m.reports <- msg:
			case <-stream.Context().Done():
				return
			}
		}
	}()
	for {
		select {
		case cmd := <-m.commands:
			if err := stream.Send(cmd); err != nil {
				return err
			}
		case err := <-recvErr:
			return err
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func startTestClient(t *testing.T) (*testMaster, *grpc.Server, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	master := &testMaster{commands: make(chan *proto.MasterCmd, 10), reports: make(chan *proto.WorkerMsg, 100)}
	proto.RegisterSwarmServiceServer(server, master)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	client, err := NewGRPCClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- client.Start() }()
	if msg := nextReport(t, master); msg.GetRegister() == nil {
		t.Fatal("first message must be registration")
	}
	return master, server, done
}

func nextReport(t *testing.T, master *testMaster) *proto.WorkerMsg {
	t.Helper()
	select {
	case msg := <-master.reports:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for worker report")
		return nil
	}
}

func startCommand(target string, requests, concurrency int32) *proto.MasterCmd {
	return &proto.MasterCmd{Cmd: &proto.MasterCmd_Start{Start: &proto.StartCmd{
		TargetUrl: target, TotalRequests: requests, Concurrency: concurrency,
	}}}
}

func waitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func TestClientQuitAndDisconnectCancelActiveRequests(t *testing.T) {
	for _, action := range []string{"quit", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			started, canceled := make(chan struct{}), make(chan struct{})
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-r.Context().Done()
				close(canceled)
			}))
			defer target.Close()
			master, server, done := startTestClient(t)
			master.commands <- startCommand(target.URL, 100, 1)
			waitSignal(t, started, "request start")
			if action == "quit" {
				master.commands <- &proto.MasterCmd{Cmd: &proto.MasterCmd_Quit{Quit: &proto.QuitCmd{}}}
			} else {
				server.Stop()
			}
			select {
			case err := <-done:
				if action == "quit" && err != nil {
					t.Fatalf("QUIT failed: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("client stayed blocked on the active run")
			}
			waitSignal(t, canceled, "HTTP request cancellation")
		})
	}
}

func receiveFinal(t *testing.T, master *testMaster) *proto.StatsMsg {
	t.Helper()
	var previous int32
	var final *proto.StatsMsg
	for {
		msg := nextReport(t, master)
		if stats := msg.GetStats(); stats != nil {
			completed := stats.SuccessCount + stats.FailCount
			if completed < previous {
				t.Fatalf("progress went backwards: %d -> %d", previous, completed)
			}
			previous, final = completed, stats
		}
		if msg.GetFinish() != nil {
			if final == nil {
				t.Fatal("finish arrived before stats")
			}
			return final
		}
	}
}

func TestClientStopReportsPartialRunAndAllowsRestart(t *testing.T) {
	started := make(chan struct{})
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(started)
			<-r.Context().Done()
			return
		}
		w.Write([]byte("ok"))
	}))
	defer target.Close()
	master, _, done := startTestClient(t)
	master.commands <- startCommand(target.URL, 100, 1)
	waitSignal(t, started, "request start")
	master.commands <- &proto.MasterCmd{Cmd: &proto.MasterCmd_Stop{Stop: &proto.StopCmd{}}}
	partial := receiveFinal(t, master)
	if partial.FailCount != 1 || partial.SuccessCount != 0 {
		t.Fatalf("STOP counted unstarted work: %+v", partial)
	}
	master.commands <- startCommand(target.URL, 150, 5)
	final := receiveFinal(t, master)
	if final.SuccessCount != 150 || final.FailCount != 0 || final.CurrentRps <= 0 {
		t.Fatalf("incorrect final stats after restart: %+v", final)
	}
	master.commands <- &proto.MasterCmd{Cmd: &proto.MasterCmd_Quit{Quit: &proto.QuitCmd{}}}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client did not exit")
	}
}

func TestClientInvalidRunStillFinishes(t *testing.T) {
	master, _, done := startTestClient(t)
	master.commands <- startCommand("invalid", 1, 1)
	if msg := nextReport(t, master); msg.GetFinish() == nil {
		t.Fatal("invalid run did not send a completion marker")
	}
	master.commands <- &proto.MasterCmd{Cmd: &proto.MasterCmd_Quit{Quit: &proto.QuitCmd{}}}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client did not exit")
	}
}

func TestClientsReplayPOSTBodyThroughGRPC(t *testing.T) {
	const payload = `{"message":"温度","value":42}`
	var requests, active, peak atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); current > old; old = peak.Load() {
			if peak.CompareAndSwap(old, current) {
				break
			}
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || r.Method != http.MethodPost || string(body) != payload || r.ContentLength != int64(len(payload)) || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Test") != "grpc" {
			t.Errorf("POST changed on the wire: method=%s body=%q length=%d headers=%v err=%v", r.Method, body, r.ContentLength, r.Header, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests.Add(1)
		time.Sleep(5 * time.Millisecond)
		w.Write([]byte("ok"))
	}))
	defer target.Close()
	master1, _, done1 := startTestClient(t)
	master2, _, done2 := startTestClient(t)
	for _, master := range []*testMaster{master1, master2} {
		master.commands <- &proto.MasterCmd{Cmd: &proto.MasterCmd_Start{Start: &proto.StartCmd{
			TargetUrl: target.URL, TotalRequests: 64, Concurrency: 8,
			Method: http.MethodPost, Body: []byte(payload),
			Headers: map[string]string{"Content-Type": "application/json", "X-Test": "grpc"},
		}}}
	}
	for _, master := range []*testMaster{master1, master2} {
		final := receiveFinal(t, master)
		if final.SuccessCount != 64 || final.FailCount != 0 {
			t.Fatalf("incorrect POST run summary: %+v", final)
		}
		master.commands <- &proto.MasterCmd{Cmd: &proto.MasterCmd_Quit{Quit: &proto.QuitCmd{}}}
	}
	for _, done := range []<-chan error{done1, done2} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("client did not exit")
		}
	}
	if requests.Load() != 128 || peak.Load() < 2 || peak.Load() > 16 {
		t.Fatalf("unexpected count or concurrency: requests=%d peak=%d", requests.Load(), peak.Load())
	}
}

func TestWorkerIDsAreUniqueWhenCreatedTogether(t *testing.T) {
	seen := make(map[string]bool)
	for range 1000 {
		id := newWorkerID()
		if seen[id] {
			t.Fatalf("duplicate worker ID %q", id)
		}
		seen[id] = true
	}
}
