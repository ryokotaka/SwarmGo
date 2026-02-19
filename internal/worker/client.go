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

// GRPCClient は Master との gRPC 接続を管理する構造体。
//
// クライアントとは「要求する側」のこと。Master がサーバー（待ち受け・指示を出す側）、
// Worker がクライアント（接続して指示を受け取り結果を返す側）となる。
// この構造体は「Master と gRPC で話すための窓口」を表す。
type GRPCClient struct {
	masterAddr string // 接続先アドレス（例: "localhost:50051"）
	conn       *grpc.ClientConn
	client     proto.SwarmServiceClient // Connect() を呼びストリームを張るためのクライアント
}

// NewGRPCClient は指定アドレスの Master へ接続する gRPC クライアントを生成する。
//
// やっていること:
//   - addr に対して TLS なし（insecure）で gRPC 接続を張る。
//     TLS は通信の暗号化。開発・同一マシン内では insecure で十分なことが多い。
//   - この時点で作られるのは「接続（conn）」だけ。
//     ストリーム（WorkerMsg を送り MasterCmd を受け取る路）はまだ張られていない。
//     ストリームは Start() 内で Connect() を呼んだときに 1 本張られる。
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

// Start は Master との双方向ストリームを開始し、Register 送信後に受信ループで Start/Stop/Quit を処理する。
//
// 流れ: 接続 → ストリーム取得 → Register 送信 → ループで MasterCmd 受信・処理
func (c *GRPCClient) Start() error {
	defer c.conn.Close()

	log.Printf("Connecting to Master at %s...", c.masterAddr)

	// context.Background() は「何も設定していない・いちばんシンプルな context」。
	// キャンセルやタイムアウトを指定しないときに使う。Connect() は context を引数に取るため渡している。
	stream, err := c.client.Connect(context.Background())
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	// ここで「ストリーム」が 1 本張られた。以降 stream.Send() / stream.Recv() で WorkerMsg / MasterCmd を送受信する。

	// 1. 最初のメッセージ: RegisterMsg（Master に「このワーカーがつながった」と登録する）
	// workerID: 他ワーカーと被りにくくするため、ナノ秒 + 0..999 の乱数で一意にする（例: "worker-1739123456789012345-42"）
	// CpuArch: runtime.GOARCH（"arm64", "amd64" など）をそのまま送り、Master がどのアーキテクチャのワーカーか把握できるようにする
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

	// 2. 受信ループ（Master からの MasterCmd: Start / Stop / Quit を待って処理）
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
			// 負荷テスト実行指示。TargetUrl に TotalRequests 回 GET を、Concurrency 並列で実行する。
			log.Printf("START: target=%s requests=%d concurrency=%d",
				cmd.Start.TargetUrl, cmd.Start.TotalRequests, cmd.Start.Concurrency)

			r := NewMyRunner()
			summary, err := r.MyRun(
				context.Background(),
				cmd.Start.TargetUrl,
				int(cmd.Start.TotalRequests),
				int(cmd.Start.Concurrency),
			)
			if err != nil {
				log.Printf("Run failed: %v", err)
				continue
			}

			log.Printf("Run finished: total=%d success=%d failed=%d duration=%v",
				summary.MyTotal, summary.MySuccess, summary.MyFailed, summary.MyTotalDuration)
			if summary.MyFailed > 0 && summary.MyFirstErr != nil {
				log.Printf("First failure reason: %v", summary.MyFirstErr)
			}

			// 結果を Master へ Stats で報告する。
			// Worker から Master へ送る 1 通は常に WorkerMsg。その「中身」が register / stats / finish のいずれか。
			// StatsMsg は proto の StatsMsg（success_count, fail_count, current_rps）を詰めた WorkerMsg として送る。
			rps := 0.0
			if summary.MyTotalDuration.Seconds() > 0 {
				rps = float64(summary.MyTotal) / summary.MyTotalDuration.Seconds()
			}
			report := &proto.WorkerMsg{
				Msg: &proto.WorkerMsg_Stats{
					Stats: &proto.StatsMsg{
						SuccessCount: int32(summary.MySuccess),
						FailCount:    int32(summary.MyFailed),
						CurrentRps:   rps,
					},
				},
			}
			if err := stream.Send(report); err != nil {
				log.Printf("Failed to send stats: %v", err)
			}

			// 完了報告（FinishMsg）。Master の「Worker %s finished task.」などと対応させるため、所要時間を送る。
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
			return nil // ループを抜け、defer で conn.Close() される

		default:
			log.Printf("Unknown command: %T", cmd)
		}
	}
}
