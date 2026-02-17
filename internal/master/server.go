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

// TUI 用型（窓口から表示板へ送る伝言の種類）
// SetUIChan でチャネルが渡されていれば sendToUI / logOrSendToUI で送信、未設定なら log.Printf のみ。

// StatsUpdate は Worker から受信した StatsMsg を TUI に転送するための型
type StatsUpdate struct {
	WorkerID     string
	SuccessCount int32
	FailCount    int32
	CurrentRps   float64
}

// WorkerListChanged は Worker の接続/切断時に TUI へ通知するイベント（一覧の再描画を促す）
type WorkerListChanged struct{}

// LogLine は TUI のログウィンドウに表示する 1 行メッセージ
type LogLine struct {
	Message string
}

// Server は SwarmService の gRPC サーバー実装。
type Server struct {
	proto.UnimplementedSwarmServiceServer

	mu       sync.Mutex
	workers  map[string]proto.SwarmService_ConnectServer
	uiChan   chan interface{}
	uiChanMu sync.Mutex
}

// サーバーの初期化
func NewServer() *Server {
	return &Server{
		workers: make(map[string]proto.SwarmService_ConnectServer),
	}
}

// sendToUI は TUI 用チャネルに伝言 v を送る。チャネル未設定またはバッファ満杯時は何もしない（ブロックしない）。
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

// logOrSendToUI は TUI 用チャネルが設定されていれば LogLine で送信、なければ log.Printf。
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

// SetUIChan は TUI 用チャネルを渡す。main で TUI 起動時に呼ぶ。未呼び出しの場合は log.Printf のみ使用。
func (s *Server) SetUIChan(ch chan interface{}) {
	s.uiChanMu.Lock()
	defer s.uiChanMu.Unlock()
	s.uiChan = ch
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
	s.logOrSendToUI("Worker connected: %s (Arch: %s)", workerID, reg.CpuArch)

	// 2. workers に登録
	s.mu.Lock()
	s.workers[workerID] = stream
	s.mu.Unlock()
	s.sendToUI(WorkerListChanged{})

	// 3. 接続切れの時の後始末
	defer func() {
		s.mu.Lock()
		delete(s.workers, workerID)
		s.mu.Unlock()
		s.logOrSendToUI("Worker disconnected: %s", workerID)
		s.sendToUI(WorkerListChanged{})
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
			s.sendToUI(StatsUpdate{
				WorkerID:     workerID,
				SuccessCount: stats.SuccessCount,
				FailCount:    stats.FailCount,
				CurrentRps:   stats.CurrentRps,
			})
		case *proto.WorkerMsg_Finish:
			s.logOrSendToUI("Worker %s finished task.", workerID)
		default:
			s.logOrSendToUI("Unknown message type: %T", m)
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
