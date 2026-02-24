package worker

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
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

// Start opens the bidirectional stream to the Master, sends Register, then runs a receive loop handling Start/Stop/Quit.
//
// Flow: connect → get stream → send Register → loop receiving and handling MasterCmd
func (c *GRPCClient) Start() error {
	defer c.conn.Close()

	log.Printf("Connecting to Master at %s...", c.masterAddr)

	// context.Background() is the simplest context with no cancellation or timeout.
	// Connect() takes a context, so we pass it here.
	stream, err := c.client.Connect(context.Background())
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	// The stream is now open; use stream.Send() / stream.Recv() to send WorkerMsg and receive MasterCmd.

	// 1. First message: RegisterMsg (tell the Master this worker has connected)
	// workerID: unique per worker using nanosecond timestamp + 0..999 random (e.g. "worker-1739123456789012345-42")
	// CpuArch: send runtime.GOARCH ("arm64", "amd64", etc.) so the Master knows the worker's architecture
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	workerID := fmt.Sprintf("worker-%d-%d", time.Now().UnixNano(), rng.Intn(1000))
	req := &proto.WorkerMsg{
		Msg: &proto.WorkerMsg_Register{
			Register: &proto.RegisterMsg{
				WorkerId: workerID,
				CpuArch:  runtime.GOARCH,
			},
		},
	}
	if err := stream.Send(req); err != nil {
		return fmt.Errorf("failed to send register: %w", err)
	}
	log.Printf("Successfully registered as %s", workerID)

	// 2. Receive loop: wait for MasterCmd (Start / Stop / Quit) from Master and handle each
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			log.Println("Connection closed by Master")
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream error: %w", err)
		}

		switch cmd := msg.Cmd.(type) {
		case *proto.MasterCmd_Start:
			// Load test: run TotalRequests GETs to TargetUrl with Concurrency parallelism
			log.Printf("START: target=%s requests=%d concurrency=%d",
				cmd.Start.TargetUrl, cmd.Start.TotalRequests, cmd.Start.Concurrency)

			r := NewMyRunner()
			progressCh := make(chan struct {
				success int
				failed  int
				rps     float64
			}, 20)
			runDone := make(chan struct{})

			var summary *MySummary
			var runErr error
			go func() {
				defer close(runDone)
				defer close(progressCh)
				summary, runErr = r.MyRun(
					context.Background(),
					cmd.Start.TargetUrl,
					int(cmd.Start.TotalRequests),
					int(cmd.Start.Concurrency),
					func(completed, success, failed int, elapsed time.Duration) {
						rps := 0.0
						if elapsed.Seconds() > 0 {
							rps = float64(completed) / elapsed.Seconds()
						}
						select {
						case progressCh <- struct {
							success int
							failed  int
							rps     float64
						}{success: success, failed: failed, rps: rps}:
						default:
						}
					},
				)
			}()

			go func() {
				for s := range progressCh {
					report := &proto.WorkerMsg{
						Msg: &proto.WorkerMsg_Stats{
							Stats: &proto.StatsMsg{
								SuccessCount:  int32(s.success),
								FailCount:     int32(s.failed),
								CurrentRps:    s.rps,
								LatencyP50Ms:  0,
								LatencyP90Ms:  0,
								LatencyP99Ms:  0,
							},
						},
					}
					if err := stream.Send(report); err != nil {
						log.Printf("Failed to send progress stats: %v", err)
						return
					}
				}
			}()

			<-runDone
			if runErr != nil {
				log.Printf("Run failed: %v", runErr)
				continue
			}

			log.Printf("Run finished: total=%d success=%d failed=%d duration=%v",
				summary.MyTotal, summary.MySuccess, summary.MyFailed, summary.MyTotalDuration)
			if summary.MyFailed > 0 && summary.MyFirstErr != nil {
				log.Printf("First failure reason: %v", summary.MyFirstErr)
			}

			// Report final result to Master via Stats (including percentiles and error reasons)
			rps := 0.0
			if summary.MyTotalDuration.Seconds() > 0 {
				rps = float64(summary.MyTotal) / summary.MyTotalDuration.Seconds()
			}
			errorReasons := make([]*proto.ErrorReason, 0, len(summary.MyErrorReasons))
			for msg, cnt := range summary.MyErrorReasons {
				errorReasons = append(errorReasons, &proto.ErrorReason{Message: msg, Count: int32(cnt)})
			}
			report := &proto.WorkerMsg{
				Msg: &proto.WorkerMsg_Stats{
					Stats: &proto.StatsMsg{
						SuccessCount:   int32(summary.MySuccess),
						FailCount:      int32(summary.MyFailed),
						CurrentRps:     rps,
						LatencyP50Ms:  int32(summary.LatencyP50.Milliseconds()),
						LatencyP90Ms:  int32(summary.LatencyP90.Milliseconds()),
						LatencyP99Ms:  int32(summary.LatencyP99.Milliseconds()),
						ErrorReasons:   errorReasons,
					},
				},
			}
			if err := stream.Send(report); err != nil {
				log.Printf("Failed to send stats: %v", err)
			}

			// Completion report (FinishMsg); send total duration so Master can log "Worker %s finished task." etc.
			finish := &proto.WorkerMsg{
				Msg: &proto.WorkerMsg_Finish{
					Finish: &proto.FinishMsg{
						TotalDurationMs: int32(summary.MyTotalDuration.Milliseconds()),
					},
				},
			}
			if err := stream.Send(finish); err != nil {
				log.Printf("Failed to send finish: %v", err)
			}

		case *proto.MasterCmd_Stop:
			log.Println("STOP command received")

		case *proto.MasterCmd_Quit:
			log.Println("QUIT command received")
			return nil // exit loop; defer runs conn.Close()

		default:
			log.Printf("Unknown command: %T", cmd)
		}
	}
}
