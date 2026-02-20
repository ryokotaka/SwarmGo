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

// TUI 用型（窓口から表示板へ送る伝言の種類）
// SetUIChan でチャネルが渡されていれば sendToUI / logOrSendToUI で送信、未設定なら log.Printf のみ。

// StatsUpdate は Worker から受信した StatsMsg を TUI に転送するための型
type StatsUpdate struct {
	WorkerID      string
	SuccessCount  int32
	FailCount     int32
	CurrentRps    float64
	LatencyP50Ms  int32
	LatencyP90Ms  int32
	LatencyP99Ms  int32
}

// WorkerListChanged は Worker の接続/切断時に TUI へ通知するイベント（一覧の再描画を促す）
type WorkerListChanged struct{}

// LogLine は TUI のログウィンドウに表示する 1 行メッセージ
type LogLine struct {
	Message string
}

// Server = このプログラムの「Master」の正体。1 プロセスに 1 つだけ。
// Worker を束ねて命令を送る側。gRPC の窓口でもある。
//
// 中身（この構造体が持っているもの）:
//   - workers … 今つながっている Worker の名簿（ID → その子との通信路）
//   - mu … 名簿を触るときの鍵（同時に触らないようにする）
//   - uiChan … 画面（TUI）にログを送るための管。SetUIChan で渡す。
//   - uiChanMu … その管の設定を守る鍵
type Server struct {
	proto.UnimplementedSwarmServiceServer

	mu             sync.Mutex
	workers        map[string]proto.SwarmService_ConnectServer
	uiChan         chan interface{}
	uiChanMu       sync.Mutex
	errorReasons   map[string]int // エラー要因ごとの発生回数（Worker の Stats からマージ、TUI で表示）
	errorReasonsMu sync.Mutex
}

// NewServer は Master の「実体」を 1 個作り、その「住所」（*Server）を返す。
// 住所を渡すので、もらった人みんなが同じ 1 個を触れる。コピーではない。
func NewServer() *Server {
	return &Server{
		workers:      make(map[string]proto.SwarmService_ConnectServer),
		errorReasons: make(map[string]int),
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

// MergeErrorReasons は Worker から受信したエラー要因をサーバー側の集計にマージする。Connect の受信ループから呼ぶ。
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

// GetErrorReasons は集計済みのエラー要因のコピーを返す。TUI の View から呼ぶ。同時書き込みを避けるためコピーを返す。
func (s *Server) GetErrorReasons() map[string]int {
	s.errorReasonsMu.Lock()
	defer s.errorReasonsMu.Unlock()
	out := make(map[string]int, len(s.errorReasons))
	for k, v := range s.errorReasons {
		out[k] = v
	}
	return out
}

// ResetErrorReasons はエラー要因集計をクリアする。負荷テスト開始（'s' 押下）時に TUI から呼ぶ。
func (s *Server) ResetErrorReasons() {
	s.errorReasonsMu.Lock()
	defer s.errorReasonsMu.Unlock()
	s.errorReasons = make(map[string]int)
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

// WorkerInfo は list コマンド用の接続中 Worker の情報（ID とアドレス）
type WorkerInfo struct {
	ID   string
	Addr string
}

// BroadcastCommand は接続中の全 Worker に同じ命令を送信する。
//
// 
//   - 目的: 分散負荷テストで、Master が複数 Worker に同じ指示を一斉に出してまとめて制御する。
//   - この関数の役割: 「今つながっている全 Worker に、同じ 1 個の命令（開始/停止/終了）を一斉に送る」API。
//   - 実装の工夫: 送信先リストは共有の s.workers。ロックを長く持ちたくないので、
//     ロック中は「写し（snapshot）」を取るだけにして、ロックを外してからその写しに対して Send する。
func (s *Server) BroadcastCommand(cmd *proto.MasterCmd) {
	// 共有データ s.workers（map）を触る前に Mutex をロック。他 goroutine はここで待たされる。
	s.mu.Lock()
	// 新しい map を作成。キー=WorkerID(string)、値=ストリーム。第2引数は容量ヒントで、len(s.workers) ぶん確保し追加時の再確保を減らす。
	snapshot := make(map[string]proto.SwarmService_ConnectServer, len(s.workers))
	for id, stream := range s.workers {
		snapshot[id] = stream
	}
	s.mu.Unlock()
	// ロックはここまで。以降は snapshot だけを触るので、stream.Send() のように時間がかかる処理をロック外で実行できる。

	for id, stream := range snapshot {
		if err := stream.Send(cmd); err != nil {
			s.logOrSendToUI("Failed to send command to %s: %v", id, err)
		}
	}
}

// ListWorkers は現在接続中の Worker 一覧（ID, アドレス）を返す。
//
// 
//   - 目的: TUI/CLI で「今どの Worker がつながっているか」を表示するために一覧を取得する。
//   - この関数の役割: 名簿（s.workers）を安全に読んで、呼び出し元が使いやすい形（[]WorkerInfo）で返す API。
//   - 実装の工夫: 読むだけなのでロック中に list を組み立てて返す（Send のような重い処理はないため写しは取らない）。
func (s *Server) ListWorkers() []WorkerInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]WorkerInfo, 0, len(s.workers)) // 返却用スライス。長さ0、容量は Worker 数で事前確保。
	for id, stream := range s.workers {
		addr := "" // この Worker の接続元アドレス。取れなければ空文字列のまま。
		if p, ok := peer.FromContext(stream.Context()); ok && p.Addr != nil {
			addr = p.Addr.String() // peer=接続の相手（Worker）、Addr=そのネットワーク上の住所（IP:ポート）
		}
		list = append(list, WorkerInfo{ID: id, Addr: addr})
	}
	return list
}

// StartGRPCServer は 1 つの Server インスタンスで全 Connect が同じ workers を共有する。ブロッキング。
//
// 起動の流れ:
//  1. リッスン … net.Listen でポートを開き、窓口で接続を待ち受ける準備をする。
//  2. サーバーを用意 … grpc.NewServer で窓口での処理（gRPC の受け方）を決め、Register で RPC を登録する。
//  3. サーブ … grpcServer.Serve(lis) でその窓口で接続を受け付け続ける（戻ってこない）。
func StartGRPCServer(port string) error {
	// 1. リッスン: このポートで TCP 接続を待ち受ける（標準ライブラリ net）
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}
	// 2. サーバーを用意: gRPC 用のサーバーを作り、SwarmService の処理を登録する（公式 grpc ライブラリ）
	grpcServer := grpc.NewServer()
	srv := NewServer()
	proto.RegisterSwarmServiceServer(grpcServer, srv)
	log.Printf("Master server listening on port %s...", port)
	// 3. サーブ: 窓口で接続を受け付け続ける（ブロック）
	return grpcServer.Serve(lis)
}

// RunGRPCServer は「gRPC を裏で動かしつつ、すぐに Master の住所（*Server）を返す」関数。
//
// なぜ返すか:
//  画面TUIが一覧表示、全員に開始をやりたいとき、
//  「その Master の住所」を渡す。
//
// 返すのは「コピー」じゃなく「住所」。もらった人は srv.ListWorkers() や
// srv.BroadcastCommand() で、裏で動いているあの 1 個の Master を触れる。
//
// やっていることは StartGRPCServer と同じ 3 段階。違うのは「待ち受け（Serve）」を
// 別 goroutine でやるだけ。だからこっちはすぐ return して、*Server を渡せる。
func RunGRPCServer(port string) (*Server, error) {
	// 1. ポートを開いて、接続を待つ準備
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %v", err)
	}
	// 2. gRPC のサーバーを作り、この srv を「SwarmService の実装」として登録
	grpcServer := grpc.NewServer()
	srv := NewServer()
	proto.RegisterSwarmServiceServer(grpcServer, srv)
	log.Printf("Master server listening on port %s...", port)

	// 3. 接続の待ち受けは「ずっとブロックする」ので、別 goroutine で実行。ここでは return する。
	go func() {
		_ = grpcServer.Serve(lis)
	}()

	// この 1 個の Master の住所を返す。TUI はこれで同じ Master を操作する。
	return srv, nil
}
