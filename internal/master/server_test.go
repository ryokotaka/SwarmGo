package master

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/ryokotaka/SwarmGo/proto"
	"google.golang.org/grpc"
)

type testStream struct {
	grpc.ServerStream
	incoming chan *proto.WorkerMsg
	outgoing chan *proto.MasterCmd
}

func (s *testStream) Context() context.Context { return context.Background() }
func (s *testStream) Send(cmd *proto.MasterCmd) error {
	s.outgoing <- cmd
	return nil
}
func (s *testStream) Recv() (*proto.WorkerMsg, error) {
	msg, ok := <-s.incoming
	if !ok {
		return nil, io.EOF
	}
	return msg, nil
}

func connectTestWorker(t *testing.T, server *Server, id string) *testStream {
	t.Helper()
	stream := &testStream{incoming: make(chan *proto.WorkerMsg, 10), outgoing: make(chan *proto.MasterCmd, 10)}
	stream.incoming <- &proto.WorkerMsg{Msg: &proto.WorkerMsg_Register{Register: &proto.RegisterMsg{WorkerId: id}}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.Connect(stream)
	}()
	t.Cleanup(func() { close(stream.incoming); <-done })
	waitUntil(t, func() bool {
		for _, worker := range server.ListWorkers() {
			if worker.ID == id {
				return true
			}
		}
		return false
	})
	return stream
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatal("timed out waiting for server state")
		}
	}
}

func TestRunStateSurvivesDroppedUINotifications(t *testing.T) {
	server := NewServer()
	ui := make(chan interface{}, 1)
	ui <- "keep queue full"
	server.SetUIChan(ui)
	stream := connectTestWorker(t, server, "worker-1")
	start := &proto.StartCmd{TargetUrl: "http://127.0.0.1", TotalRequests: 10, Concurrency: 1}
	if !server.StartRun(start) || server.StartRun(start) {
		t.Fatal("START must succeed once and reject overlap")
	}
	// Joining during a run must not change its planned request count.
	connectTestWorker(t, server, "late-worker")
	stream.incoming <- &proto.WorkerMsg{Msg: &proto.WorkerMsg_Stats{Stats: &proto.StatsMsg{SuccessCount: 10, CurrentRps: 25}}}
	stream.incoming <- &proto.WorkerMsg{Msg: &proto.WorkerMsg_Finish{Finish: &proto.FinishMsg{}}}
	waitUntil(t, func() bool { return !server.SnapshotRun().Running })
	snapshot := server.SnapshotRun()
	if snapshot.ExpectedRequests != 10 || snapshot.Stats["worker-1"].SuccessCount != 10 {
		t.Fatalf("lost final result or changed denominator: %+v", snapshot)
	}
	delete(snapshot.Stats, "worker-1")
	if server.SnapshotRun().Stats["worker-1"].SuccessCount != 10 {
		t.Fatal("snapshot mutations changed server state")
	}
	if got := <-ui; got != "keep queue full" {
		t.Fatalf("test queue was not saturated: %v", got)
	}
	if !server.StartRun(start) {
		t.Fatal("dropped finish notification prevented restart")
	}
	if server.SnapshotRun().ExpectedRequests != 20 {
		t.Fatal("next run did not include newly connected worker")
	}
}
