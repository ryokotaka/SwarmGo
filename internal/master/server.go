package master

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"

	"github.com/ryokotaka/SwarmGo/proto"
)

// Types for TUI: messages sent from the server to the display.
// If a channel is set via SetUIChan, messages are sent via sendToUI / logOrSendToUI; otherwise only log.Printf is used.

// StatsUpdate carries Worker stats (StatsMsg) received from Workers to the TUI.
type StatsUpdate struct {
	WorkerID      string
	SuccessCount  int32
	FailCount     int32
	CurrentRps    float64
	LatencyP50Ms  int32
	LatencyP90Ms  int32
	LatencyP99Ms  int32
}

// WorkerListChanged notifies the TUI when a Worker connects or disconnects (triggers list redraw).
type WorkerListChanged struct{}

// LogLine is a single-line message displayed in the TUI log window.
type LogLine struct {
	Message string
}

// Server is the Master entity for this process (one per process).
// It coordinates Workers, sends commands, and exposes the gRPC endpoint.
//
// Fields:
//   - workers: roster of connected Workers (ID -> stream to each Worker)
//   - mu: mutex protecting the roster (prevents concurrent access)
//   - uiChan: channel to send log messages to the TUI; set via SetUIChan
//   - uiChanMu: mutex protecting uiChan
type Server struct {
	proto.UnimplementedSwarmServiceServer

	mu             sync.Mutex
	workers        map[string]proto.SwarmService_ConnectServer
	uiChan         chan interface{}
	uiChanMu       sync.Mutex
	errorReasons   map[string]int // occurrence count per error reason (merged from Worker Stats, shown in TUI)
	errorReasonsMu sync.Mutex
}

// NewServer creates a single Master instance and returns its pointer.
// Callers share the same instance by reference, not by copy.
func NewServer() *Server {
	return &Server{
		workers:      make(map[string]proto.SwarmService_ConnectServer),
		errorReasons: make(map[string]int),
	}
}

// sendToUI sends message v to the TUI channel. No-op (non-blocking) if the channel is unset or buffer is full.
func (s *Server) sendToUI(v interface{}) {
	s.uiChanMu.Lock()
	ch := s.uiChan
	s.uiChanMu.Unlock()
	if ch != nil {
		select {
		case ch <- v:
		default:
		}
	}
}

// logOrSendToUI sends a LogLine to the TUI channel if set; otherwise logs via log.Printf.
func (s *Server) logOrSendToUI(format string, args ...interface{}) {
	s.uiChanMu.Lock()
	ch := s.uiChan
	s.uiChanMu.Unlock()
	if ch != nil {
		msg := fmt.Sprintf(format, args...)
		select {
		case ch <- LogLine{Message: msg}:
		default:
		}
	} else {
		log.Printf(format, args...)
	}
}

// SetUIChan sets the channel used for TUI. Call from main when starting the TUI. If never called, only log.Printf is used.
func (s *Server) SetUIChan(ch chan interface{}) {
	s.uiChanMu.Lock()
	defer s.uiChanMu.Unlock()
	s.uiChan = ch
}

// MergeErrorReasons merges error reasons received from Workers into the server-side aggregate. Called from Connect's receive loop.
func (s *Server) MergeErrorReasons(reasons []*proto.ErrorReason) {
	if len(reasons) == 0 {
		return
	}
	s.errorReasonsMu.Lock()
	defer s.errorReasonsMu.Unlock()
	for _, r := range reasons {
		if r == nil || r.Message == "" {
			continue
		}
		s.errorReasons[r.Message] += int(r.Count)
	}
}

// GetErrorReasons returns a copy of the aggregated error reasons. Called from the TUI View. Returns a copy to avoid concurrent write issues.
func (s *Server) GetErrorReasons() map[string]int {
	s.errorReasonsMu.Lock()
	defer s.errorReasonsMu.Unlock()
	out := make(map[string]int, len(s.errorReasons))
	for k, v := range s.errorReasons {
		out[k] = v
	}
	return out
}

// ResetErrorReasons clears the error reason aggregate. Called from the TUI when starting a load test (e.g. on 's' key).
func (s *Server) ResetErrorReasons() {
	s.errorReasonsMu.Lock()
	defer s.errorReasonsMu.Unlock()
	s.errorReasons = make(map[string]int)
}

// Connect handles the bidirectional stream from a Worker.
func (s *Server) Connect(stream proto.SwarmService_ConnectServer) error {
	// 1. Receive the first message (Register)
	// Recv() decodes the wire format (tag + value) into WorkerMsg according to the .proto definition
	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	reg := msg.GetRegister()
	if reg == nil {
		return fmt.Errorf("first message must be Register")
	}
	workerID := reg.WorkerId
	s.logOrSendToUI("Worker connected: %s (Arch: %s)", workerID, reg.CpuArch)

	// 2. Register in workers
	s.mu.Lock()
	s.workers[workerID] = stream
	s.mu.Unlock()
	s.sendToUI(WorkerListChanged{})

	// 3. Cleanup when the connection is closed
	defer func() {
		s.mu.Lock()
		delete(s.workers, workerID)
		s.mu.Unlock()
		s.logOrSendToUI("Worker disconnected: %s", workerID)
		s.sendToUI(WorkerListChanged{})
	}()

	// 4. Receive loop (handle Stats / Finish)
	for {
		// Receive: bytes are interpreted by tag (1=Register, 2=Stats, 3=Finish) and decoded into msg
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch m := msg.Msg.(type) {
		case *proto.WorkerMsg_Stats:
			stats := m.Stats
			s.MergeErrorReasons(stats.ErrorReasons)
			s.sendToUI(StatsUpdate{
				WorkerID:     workerID,
				SuccessCount: stats.SuccessCount,
				FailCount:    stats.FailCount,
				CurrentRps:   stats.CurrentRps,
				LatencyP50Ms: stats.LatencyP50Ms,
				LatencyP90Ms: stats.LatencyP90Ms,
				LatencyP99Ms: stats.LatencyP99Ms,
			})
		case *proto.WorkerMsg_Finish:
			s.logOrSendToUI("Worker %s finished task.", workerID)
		default:
			s.logOrSendToUI("Unknown message type: %T", m)
		}
	}
}

// WorkerInfo holds connection info (ID and address) for a Worker, used by the list command.
type WorkerInfo struct {
	ID   string
	Addr string
}

// BroadcastCommand sends the same command to all connected Workers.
//
//   - Purpose: in distributed load testing, the Master issues the same instruction to all Workers at once.
//   - Role: API to send one command (start/stop/exit) to every connected Worker.
//   - Implementation: take a snapshot of s.workers under the lock, then Send to each stream outside the lock to avoid holding the lock during I/O.
func (s *Server) BroadcastCommand(cmd *proto.MasterCmd) {
	// Lock the mutex before touching shared s.workers; other goroutines block here.
	s.mu.Lock()
	// Create a new map: key=WorkerID (string), value=stream. Second arg is capacity hint to reduce reallocation.
	snapshot := make(map[string]proto.SwarmService_ConnectServer, len(s.workers))
	for id, stream := range s.workers {
		snapshot[id] = stream
	}
	s.mu.Unlock()
	// Lock released here. We only use snapshot below, so slow operations like stream.Send() run outside the lock.

	for id, stream := range snapshot {
		if err := stream.Send(cmd); err != nil {
			s.logOrSendToUI("Failed to send command to %s: %v", id, err)
		}
	}
}

// ListWorkers returns the list of currently connected Workers (ID and address).
//
//   - Purpose: obtain a list for TUI/CLI to show which Workers are connected.
//   - Role: safely read the roster (s.workers) and return it as []WorkerInfo.
//   - Implementation: read-only, so the list is built under the lock (no snapshot needed since there is no heavy I/O like Send).
func (s *Server) ListWorkers() []WorkerInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]WorkerInfo, 0, len(s.workers)) // Slice for return: length 0, capacity pre-allocated for Worker count.
	for id, stream := range s.workers {
		addr := "" // This Worker's remote address; empty string if unavailable.
		if p, ok := peer.FromContext(stream.Context()); ok && p.Addr != nil {
			addr = p.Addr.String() // peer = connection peer (Worker), Addr = its network address (IP:port)
		}
		list = append(list, WorkerInfo{ID: id, Addr: addr})
	}
	return list
}

// StartGRPCServer runs a single Server instance so all Connect streams share the same workers. Blocking.
//
// Startup flow:
//  1. Listen: open the port with net.Listen and accept incoming connections.
//  2. Create server: grpc.NewServer sets up gRPC handling; Register registers the RPC.
//  3. Serve: grpcServer.Serve(lis) accepts connections on that listener (does not return).
func StartGRPCServer(port string) error {
	// 1. Listen: wait for TCP connections on this port (stdlib net)
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}
	// 2. Create server: build gRPC server and register SwarmService handler (official grpc library)
	grpcServer := grpc.NewServer()
	srv := NewServer()
	proto.RegisterSwarmServiceServer(grpcServer, srv)
	log.Printf("Master server listening on port %s...", port)
	// 3. Serve: accept connections on the listener (blocks)
	return grpcServer.Serve(lis)
}

// RunGRPCServer runs gRPC in the background and returns the Master's *Server immediately.
//
// Why return *Server: the TUI needs it to show the worker list and to broadcast commands (e.g. start to all).
// Callers receive the pointer, not a copy, so srv.ListWorkers() and srv.BroadcastCommand() operate on the same Master instance.
//
// Same three steps as StartGRPCServer; the only difference is that Serve runs in a separate goroutine, so this function can return *Server right away.
func RunGRPCServer(port string) (*Server, error) {
	// 1. Open the port and prepare to accept connections
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %v", err)
	}
	// 2. Create gRPC server and register this srv as the SwarmService implementation
	grpcServer := grpc.NewServer()
	srv := NewServer()
	proto.RegisterSwarmServiceServer(grpcServer, srv)
	log.Printf("Master server listening on port %s...", port)

	// 3. Serve blocks forever accepting connections, so run it in a separate goroutine and return here
	go func() {
		_ = grpcServer.Serve(lis)
	}()

	// Return the pointer to this single Master instance; the TUI uses it to operate the same Master
	return srv, nil
}
