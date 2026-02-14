package master

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"

	// 自身のプロジェクトパスに合わせてください
	"github.com/ryokotaka/SwarmGo/proto"
)

// StatsUpdate は TUI 用に Worker から受信した StatsMsg を転送するための型
type StatsUpdate struct {
	WorkerID     string
	SuccessCount int32
	FailCount    int32
	CurrentRps   float64
}

// WorkerListChanged は Worker の接続/切断時に TUI へ通知するためのイベント
type WorkerListChanged struct{}

// LogLine は TUI のログウィンドウに表示するためのメッセージ
type LogLine struct {
	Message string
}

type Server struct {
	proto.UnimplementedSwarmServiceServer

	mu        sync.Mutex
	workers   map[string]proto.SwarmService_ConnectServer
	uiChan    chan interface{}
	uiChanMu  sync.Mutex
}

// サーバーの初期化
func NewServer() *Server {
	return &Server{
		workers: make(map[string]proto.SwarmService_ConnectServer),
	}
}

// サーバー起動（ブロッキング）。互換用。
func StartGRPCServer(port string) error {
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	srv := NewServer()

	proto.RegisterSwarmServiceServer(s, srv)

	log.Printf("Master server listening on port %s...", port)

	return s.Serve(lis)
}

// RunGRPCServer は gRPC サーバーを別 Goroutine で起動し、*Server を返す。
// 呼び出し元は返された Server で BroadcastCommand / ListWorkers などを呼べる。
func RunGRPCServer(port string) (*Server, error) {
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	srv := NewServer()

	proto.RegisterSwarmServiceServer(grpcServer, srv)

	log.Printf("Master server listening on port %s...", port)

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	return srv, nil
}

// sendToUI は TUI 用チャネルへ非ブロッキングで送信する（チャネル未設定時は何もしない）
func (s *Server) sendToUI(v interface{}) {
	s.uiChanMu.Lock()
	ch := s.uiChan
	s.uiChanMu.Unlock()
	if ch != nil {
		select {
		case ch <- v:
		default:
			// バッファ満杯時は捨てる（ブロックしない）
		}
	}
}

// logOrSendToUI は TUI 用チャネルが設定されていれば LogLine を送信、なければ log.Printf
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

// Worker接続処理
func (s *Server) Connect(stream proto.SwarmService_ConnectServer) error {
	// 1. 最初のメッセージを受信 (Register)
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

	// 2. マップに登録
	s.mu.Lock()
	s.workers[workerID] = stream
	s.mu.Unlock()

	// TUI に接続数更新を通知（Worker 数が View に反映される）
	s.sendToUI(WorkerListChanged{})

	// 3. 切断時の後始末
	defer func() {
		s.mu.Lock()
		delete(s.workers, workerID)
		s.mu.Unlock()
		s.logOrSendToUI("Worker disconnected: %s", workerID)
		s.sendToUI(WorkerListChanged{})
	}()

	// 4. 接続維持ループ
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		switch m := msg.Msg.(type) {
		case *proto.WorkerMsg_Stats:
			// 報告が届いた！TUI 用チャネルへ通知
			stats := m.Stats
			s.sendToUI(StatsUpdate{
				WorkerID:     workerID,
				SuccessCount: stats.SuccessCount,
				FailCount:    stats.FailCount,
				CurrentRps:   stats.CurrentRps,
			})

		case *proto.WorkerMsg_Finish:
			s.logOrSendToUI("🏁 Worker %s finished task.", workerID)

		default:
			s.logOrSendToUI("Unknown message type: %T", m)
		}
	}
}

// WorkerInfo は list コマンド用の接続中 Worker の情報
type WorkerInfo struct {
	ID   string
	Addr string
}

// BroadcastCommand は接続中の全 Worker に同じ命令を送信する。
func (s *Server) BroadcastCommand(cmd *proto.MasterCmd) {
	s.mu.Lock()
	snapshot := make(map[string]proto.SwarmService_ConnectServer, len(s.workers))
	for id, stream := range s.workers {
		snapshot[id] = stream
	}
	s.mu.Unlock()

	for id, stream := range snapshot {
		if err := stream.Send(cmd); err != nil {
			s.logOrSendToUI("Failed to send command to %s: %v", id, err)
		}
	}
}

// SetUIChan は TUI へイベント（StatsUpdate / WorkerListChanged / LogLine）を送るチャネルを設定する
func (s *Server) SetUIChan(ch chan interface{}) {
	s.uiChanMu.Lock()
	defer s.uiChanMu.Unlock()
	s.uiChan = ch
}

// ListWorkers は現在接続中の Worker 一覧（ID, アドレス）を返す。
func (s *Server) ListWorkers() []WorkerInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]WorkerInfo, 0, len(s.workers))
	for id, stream := range s.workers {
		addr := ""
		if p, ok := peer.FromContext(stream.Context()); ok && p.Addr != nil {
			addr = p.Addr.String()
		}
		list = append(list, WorkerInfo{ID: id, Addr: addr})
	}
	return list
}