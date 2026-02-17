package master

import (
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"

	"github.com/ryokotaka/SwarmGo/proto"
)

// Server は SwarmService の gRPC サーバー実装。
type Server struct {
	proto.UnimplementedSwarmServiceServer
}

// NewServer は新しい Server を返す。
func NewServer() *Server {
	return &Server{}
}

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
