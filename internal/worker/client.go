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

	// ↓ご自身の環境に合わせてパスを確認してください
	"github.com/ryokotaka/SwarmGo/proto"
)

// GRPCClient はMasterとの接続を管理する構造体です
type GRPCClient struct {
	masterAddr string
	conn       *grpc.ClientConn
	client     proto.SwarmServiceClient
}

// NewGRPCClient は新しいクライアントインスタンスを作成します
func NewGRPCClient(addr string) (*GRPCClient, error) {
	// 接続の確立
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

// Start はMasterとの通信ループを開始します
func (c *GRPCClient) Start() error {
	defer c.conn.Close()

	log.Printf("Connecting to Master at %s...", c.masterAddr)

	stream, err := c.client.Connect(context.Background())
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}

	// 1. 最初の挨拶: RegisterMsg
	// 乱数のシードを現在時刻で初期化（これをしないと毎回同じ乱数になる）
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	// IDにランダムな数字(0-999)を足して被りを防ぐ
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

	// 2. 受信ループ
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
			log.Printf("🔥 START ATTACK! Target: %s, Requests: %d, Concurrency: %d",
				cmd.Start.TargetUrl, cmd.Start.TotalRequests, cmd.Start.Concurrency)

			// Runner実行
			r := NewMyRunner()
			summary, err := r.MyRun(
				context.Background(),
				cmd.Start.TargetUrl,
				int(cmd.Start.TotalRequests),
				int(cmd.Start.Concurrency),
			)

			if err != nil {
				log.Printf("❌ Attack failed: %v", err)
				continue
			}

			log.Printf("✅ Attack Finished!")
			log.Printf("   Total: %d, Success: %d, Failed: %d", summary.MyTotal, summary.MySuccess, summary.MyFailed)
			log.Printf("   Total Duration: %v", summary.MyTotalDuration)

			// ★★★ ここが今回の修正ポイント ★★★
			log.Println("Reporting results to Master...")

			rps := 0.0
			if summary.MyTotalDuration.Seconds() > 0 {
				rps = float64(summary.MyTotal) / summary.MyTotalDuration.Seconds()
			}

			// 正しい StatsMsg を作成して送信
			report := &proto.WorkerMsg{
				Msg: &proto.WorkerMsg_Stats{ // ← ここが以前は Register になっていたはずです
					Stats: &proto.StatsMsg{
						SuccessCount: int32(summary.MySuccess),
						FailCount:    int32(summary.MyFailed),
						CurrentRps:   rps,
					},
				},
			}

			if err := stream.Send(report); err != nil {
				log.Printf("❌ Failed to report stats: %v", err)
			} else {
				log.Println("📤 Report sent successfully.")
			}

		case *proto.MasterCmd_Stop:
			log.Println("🛑 STOP COMMAND received")

		case *proto.MasterCmd_Quit:
			log.Println("👋 QUIT COMMAND received")
			return nil

		default:
			log.Printf("Unknown command received: %v", cmd)
		}
	}
}