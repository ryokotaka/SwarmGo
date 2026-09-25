package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"runtime"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/ryokotaka/SwarmGo/proto"
)

// GRPCClient manages the gRPC connection to the Master.
//
// The client is the "requester" side: Master is the server (listens and issues commands),
// Worker is the client (connects, receives commands, and returns results).
// This struct represents the interface for talking to the Master over gRPC.
type GRPCClient struct {
	masterAddr string // address to connect to (e.g. "localhost:50051")
	conn       *grpc.ClientConn
	client     proto.SwarmServiceClient // client used to call Connect() and establish the stream
}

// NewGRPCClient creates a gRPC client that connects to the Master at the given address.
//
// What it does:
//   - Establishes a gRPC connection to addr without TLS (insecure).
//     TLS encrypts the connection; for dev or same-machine use, insecure is often enough.
//   - Only the connection (conn) is created here.
//     The stream (path for sending WorkerMsg and receiving MasterCmd) is not opened yet;
//     it is opened in Start() when Connect() is called.
func NewGRPCClient(addr string) (*GRPCClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to master: %w", err)
	}
	return &GRPCClient{
		masterAddr: addr,
		conn:       conn,
		client:     proto.NewSwarmServiceClient(conn),
	}, nil
}

// newWorkerID returns a random ID. The controller rejects duplicate IDs, so it
// must stay unique even when several workers start in the same clock tick.
func newWorkerID() string {
	var b [8]byte
	rand.Read(b[:])
	return "worker-" + hex.EncodeToString(b[:])
}

// Start keeps receiving commands while a run is active. Only this event loop
// writes to the gRPC stream, so progress, final stats, and finish stay ordered.
func (c *GRPCClient) Start() error {
	defer c.conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Printf("Connecting to Master at %s...", c.masterAddr)
	stream, err := c.client.Connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	workerID := newWorkerID()
	if err := stream.Send(&proto.WorkerMsg{
		Msg: &proto.WorkerMsg_Register{Register: &proto.RegisterMsg{
			WorkerId: workerID, CpuArch: runtime.GOARCH,
		}},
	}); err != nil {
		return fmt.Errorf("failed to send register: %w", err)
	}
	log.Printf("Successfully registered as %s", workerID)

	type receivedCommand struct {
		msg *proto.MasterCmd
		err error
	}
	commands := make(chan receivedCommand, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			select {
			case commands <- receivedCommand{msg: msg, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	events := make(chan runEvent, 20)
	var runCancel context.CancelFunc
	var runDone <-chan struct{}
	defer func() {
		cancel()
		if runCancel != nil {
			runCancel()
		}
		if runDone != nil {
			<-runDone
		}
	}()

	for {
		select {
		case received := <-commands:
			if received.err == io.EOF {
				return nil
			}
			if received.err != nil {
				return fmt.Errorf("stream error: %w", received.err)
			}
			switch cmd := received.msg.Cmd.(type) {
			case *proto.MasterCmd_Start:
				if runCancel != nil {
					log.Println("Ignoring START: a run is already active")
					continue
				}
				runCtx, stop := context.WithCancel(ctx)
				runCancel = stop
				done := make(chan struct{})
				runDone = done
				go func() {
					defer close(done)
					runAndReport(ctx, runCtx, cmd.Start, events)
				}()
			case *proto.MasterCmd_Stop:
				if runCancel != nil {
					runCancel()
				}
			case *proto.MasterCmd_Quit:
				return nil
			}
		case event := <-events:
			if event.err != nil {
				log.Printf("Run failed: %v", event.err)
			}
			if event.msg != nil {
				if err := stream.Send(event.msg); err != nil {
					return fmt.Errorf("failed to send run report: %w", err)
				}
			}
			if event.done {
				runCancel()
				<-runDone
				runCancel, runDone = nil, nil
			}
		}
	}
}

type runEvent struct {
	msg  *proto.WorkerMsg
	err  error
	done bool
}

func runAndReport(sessionCtx, runCtx context.Context, cmd *proto.StartCmd, events chan<- runEvent) {
	// Final reports use the session context so STOP can still report partial work.
	emit := func(event runEvent) bool {
		select {
		case events <- event:
			return true
		case <-sessionCtx.Done():
			return false
		}
	}
	runner := NewMyRunnerWithConcurrency(min(int(cmd.Concurrency), int(cmd.TotalRequests)))
	defer runner.MyClient.CloseIdleConnections()
	log.Printf("START: method=%s target=%s requests=%d concurrency=%d", cmd.Method, cmd.TargetUrl, cmd.TotalRequests, cmd.Concurrency)
	options := RequestOptions{Method: cmd.Method, Body: cmd.Body, Headers: cmd.Headers}
	summary, err := runner.MyRunWithOptions(runCtx, cmd.TargetUrl, int(cmd.TotalRequests), int(cmd.Concurrency), options,
		func(completed, success, failed int, elapsed time.Duration) {
			stats := &proto.StatsMsg{SuccessCount: int32(success), FailCount: int32(failed)}
			if elapsed > 0 {
				stats.CurrentRps = float64(completed) / elapsed.Seconds()
			}
			select {
			case events <- runEvent{msg: &proto.WorkerMsg{Msg: &proto.WorkerMsg_Stats{Stats: stats}}}:
			default: // Intermediate progress can be skipped; final reports cannot.
			}
		})
	if err != nil {
		emit(runEvent{err: err, done: true, msg: &proto.WorkerMsg{Msg: &proto.WorkerMsg_Finish{Finish: &proto.FinishMsg{}}}})
		return
	}
	log.Printf("Run finished: total=%d success=%d failed=%d duration=%v",
		summary.MyTotal, summary.MySuccess, summary.MyFailed, summary.Elapsed)
	if summary.MySuccess > 0 {
		log.Printf("Latency (successful requests): p50_us=%d p90_us=%d p99_us=%d",
			summary.LatencyP50.Microseconds(), summary.LatencyP90.Microseconds(), summary.LatencyP99.Microseconds())
	}
	if !emit(runEvent{msg: &proto.WorkerMsg{Msg: &proto.WorkerMsg_Stats{Stats: summaryStats(summary)}}}) {
		return
	}
	emit(runEvent{msg: &proto.WorkerMsg{Msg: &proto.WorkerMsg_Finish{Finish: &proto.FinishMsg{
		TotalDurationMs: int32(summary.Elapsed.Milliseconds()),
	}}}, done: true})
}

func summaryStats(summary *MySummary) *proto.StatsMsg {
	stats := &proto.StatsMsg{
		SuccessCount: int32(summary.MySuccess),
		FailCount:    int32(summary.MyFailed),
		LatencyP50Ms: int32(summary.LatencyP50.Milliseconds()),
		LatencyP90Ms: int32(summary.LatencyP90.Milliseconds()),
		LatencyP99Ms: int32(summary.LatencyP99.Milliseconds()),
	}
	if summary.MySuccess > 0 {
		stats.LatencyUs = &proto.LatencyMicros{
			P50: summary.LatencyP50.Microseconds(),
			P90: summary.LatencyP90.Microseconds(),
			P99: summary.LatencyP99.Microseconds(),
		}
	}
	if summary.Elapsed > 0 {
		stats.CurrentRps = float64(summary.MyTotal) / summary.Elapsed.Seconds()
	}
	for message, count := range summary.MyErrorReasons {
		stats.ErrorReasons = append(stats.ErrorReasons, &proto.ErrorReason{Message: message, Count: int32(count)})
	}
	return stats
}
