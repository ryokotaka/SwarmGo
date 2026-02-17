package master

import (
	"fmt"
	"io"
	"log"
	"net"

	"google.golang.org/grpc"

	"github.com/ryokotaka/SwarmGo/proto"
)

// Server は SwarmService の gRPC サーバー実装。
type Server struct {
	proto.UnimplementedSwarmServiceServer
}

// サーバーの初期化
func NewServer() *Server {
	return &Server{}
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
	log.Printf("Worker connected: %s (Arch: %s)", reg.WorkerId, reg.CpuArch)

    // 4. 接続維持ループ
	for {
        // 受信: バイト列がタグ番号（1=Register, 2=Stats, 3=Finish）で解釈され、msg に復元されている
		_, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// サーバーの起動
func StartGRPCServer(port string) error {
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}
	s := grpc.NewServer()
	proto.RegisterSwarmServiceServer(s, NewServer())
	log.Printf("Master server listening on port %s...", port)
	return s.Serve(lis)
}
