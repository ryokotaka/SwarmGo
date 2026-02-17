package master

import (
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
