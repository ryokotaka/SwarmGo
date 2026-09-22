package worker

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryokotaka/SwarmGo/proto"
	"google.golang.org/grpc"
)

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
