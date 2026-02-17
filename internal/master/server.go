package master

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"google.golang.org/grpc"

	"github.com/ryokotaka/SwarmGo/proto"
)

// Server は SwarmService の gRPC サーバー実装。
type Server struct {
	proto.UnimplementedSwarmServiceServer

	mu      sync.Mutex
	workers map[string]proto.SwarmService_ConnectServer
}

// サーバーの初期化
func NewServer() *Server {
	return &Server{
		workers: make(map[string]proto.SwarmService_ConnectServer),
	}
}

// Connect は Worker からの双方向ストリームを処理する。
func (s *Server) Connect(stream proto.SwarmService_ConnectServer) error {
	// 1. 最初のメッセージを受信 (Register)
	// Recv() の内部で、ワイヤ上の「タグ番号＋値」が .proto の定義に従って WorkerMsg に復元される
	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	reg := msg.GetRegister()
	if reg == nil {
		return fmt.Errorf("first message must be Register")
	}
	workerID := reg.WorkerId
	log.Printf("Worker connected: %s (Arch: %s)", workerID, reg.CpuArch)

	// 2. workers に登録
	s.mu.Lock()
	s.workers[workerID] = stream
	s.mu.Unlock()
	// 3. 接続切れの時の後始末（接続切れ時に workers から削除し、ログを出力）
	defer func() {
		s.mu.Lock()
		delete(s.workers, workerID)
		s.mu.Unlock()
		log.Printf("Worker disconnected: %s", workerID)
	}()

	// 4. 受信ループ（Stats / Finish を処理）
	for {
		// 受信: バイト列がタグ番号（1=Register, 2=Stats, 3=Finish）で解釈され、msg に復元されている
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
			log.Printf("[%s] Stats: success=%d fail=%d rps=%.1f", workerID, stats.SuccessCount, stats.FailCount, stats.CurrentRps)
		case *proto.WorkerMsg_Finish:
			log.Printf("Worker %s finished task (duration_ms=%d)", workerID, m.Finish.GetTotalDurationMs())
		}
	}
}

// サーバーの起動（1 つの Server インスタンスで全 Connect が同じ workers を共有する）
func StartGRPCServer(port string) error {
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	srv := NewServer()
	proto.RegisterSwarmServiceServer(grpcServer, srv)
	log.Printf("Master server listening on port %s...", port)
	return grpcServer.Serve(lis)
}
