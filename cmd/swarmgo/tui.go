// Master 用 Bubble Tea TUI（ダッシュボード表示）
//
// === このファイルの役割 ===
//   - Master の状態（Worker 数・RPS・ログ）をターミナルに表示する
//   - キー入力（s=開始, q=終了）を Master に伝え、全 Worker へ命令をブロードキャストする
//
// === データの流れ ===
//   - internal/master が uiChan に StatsUpdate / WorkerListChanged / LogLine を送る → Update で受信
//   - ユーザーがキーを押す → Update で m.server.BroadcastCommand(cmd) を呼ぶ → Master が全 Worker に送信
//
// === 使用ライブラリの本質 ===
//
// 【Bubble Tea】https://github.com/charmbracelet/bubbletea
//   TUI（ターミナル UI）を「 Elm アーキテクチャ」風に書くためのフレームワーク。
//   本質は「状態（Model）とメッセージ（Msg）で画面を駆動する」こと。
//
//   ・Model … 画面に必要な状態をすべて持つ型。ここでは model 構造体。
//   ・Msg   … キー入力・タイマー・外部チャネルなど「何か起きた」という知らせ。Update に渡る。
//   ・Cmd   … 「あとで 1 回、メッセージを送ってください」という依頼。tea.Tick / 自作の待機など。
//
//   ループの流れ:
//     1. Init() が最初の Cmd を返す（例: チャネル待ち + タイマー）
//     2. その Cmd が完了すると Msg が届く → Update(msg) が呼ばれる
//     3. Update は (新しい model, 次の Cmd) を返す
//     4. View() が呼ばれ、model から表示用文字列を生成 → ターミナルに描画
//     5. 2 に戻る（次の Msg を待つ）。tea.Quit を返すとループ終了。
//
// 【Lipgloss】https://github.com/charmbracelet/lipgloss
//   ターミナル出力の「見た目」を宣言的に指定するライブラリ。
//   本質は「Style を組み立て、Render(text) で文字列に適用する」こと。
//
//   ・NewStyle() で Style を作り、Bold / Foreground / Border / Padding などをチェーンで指定
//   ・Render(s) で、そのスタイルを文字列 s に適用した結果を返す（ANSI エスケープ付き）
//   ・色は lipgloss.Color("86") のように文字列で指定（256 色または "red" など）
//
package main

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ryokotaka/SwarmGo/internal/master"
	"github.com/ryokotaka/SwarmGo/proto"
)

// --- 定数（表示の上限・グラフの大きさ）---
const (
	maxRPSHistory = 40 // RPS グラフに表示する履歴の最大本数（横方向の棒の数）
	graphHeight   = 8  // グラフの縦方向の行数（文字の高さ）
	maxLogLines   = 10 // ログ枠に表示する最大行数（超えた分は古い行から捨てる）
)

// --- Bubble Tea の Model を構成する型 ---
// workerStats は 1 台の Worker の集計値（成功数・失敗数・現在の RPS）
type workerStats struct {
	success, fail int32
	rps           float64
}

// model は Bubble Tea が要求する「画面の状態」を表す型。
// tea.Model インターフェースを満たすため、Init / Update / View のレシーバになる。
// フィールドは「View で表示するために必要なもの」と「Update でキー入力時に Master を呼ぶための参照」だけ持つ。
type model struct {
	server      *master.Server   // Master 本体。ListWorkers() と BroadcastCommand(cmd) に使う
	uiChan      chan interface{} // Master から TUI へイベントを送る管（main で SetUIChan に渡したチャネル）
	workerStats map[string]workerStats // Worker ID → その Worker の統計（StatsUpdate で更新）
	rpsHistory  []float64        // 直近の合計 RPS の履歴。renderRPSGraph で棒グラフにする
	logs        []string         // ログ枠に表示する行のリスト（LogLine で追加、古い行は捨てる）
	startTime   time.Time        // TUI 起動時刻。View で Uptime 表示に使う
	width       int              // ターミナル幅（WindowSizeMsg でセット。将来レイアウトに使う）
	height      int              // ターミナル高さ（同上）
}

// --- Bubble Tea の Cmd（「あとで 1 回メッセージを送る」依頼）---
//
// Cmd は「非同期で何かが起きたら、その結果を Msg として Update に渡す」ための型。
// ここでは (1) Master からのイベント用チャネル待ち と (2) 1 秒タイマー の 2 種類を使う。

// waitForUI は「uiChan から 1 件届くまでブロックし、届いた値をそのまま tea.Msg として返す」Cmd。
// Bubble Tea はこの Cmd を別 goroutine で実行する。値が返ると Update(msg) が呼ばれる。
// Update では msg を master.StatsUpdate / WorkerListChanged / LogLine などに型アサートして処理する。
func waitForUI(ch chan interface{}) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// tickCmd は「1 秒ごとに time.Time を Msg として送る」Cmd。Bubble Tea 標準の tea.Tick を使う。
// 目的は「定期的に再描画する」こと。Update では model を変えず nextCmd だけ返し、View が呼ばれて
// Uptime や RPS の表示が最新になる。
func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return t
	})
}

// --- 初期状態の作成 ---
// newModel は TUI の初期 model を作る。main で tea.NewProgram(newModel(srv, uiChan), tea.WithAltScreen()) に渡す。
func newModel(srv *master.Server, ch chan interface{}) model {
	return model{
		server:      srv,
		uiChan:      ch,
		workerStats: make(map[string]workerStats),
		rpsHistory:  make([]float64, 0, maxRPSHistory),
		logs:        make([]string, 0, maxLogLines),
		startTime:   time.Now(),
	}
}

// --- tea.Model インターフェース: Init ---
// Init は TUI 起動時に 1 回だけ呼ばれる。「次にどんなイベントを待つか」を Cmd で返す。
// tea.Batch は「複数の Cmd をまとめた Cmd」で、どれか 1 つが完了するとその Msg が 1 回 Update に届く。
// ここでは「uiChan から 1 件」と「1 秒タイマー」の両方を待つので、Master からの通知かタイマーのどちらかで Update が動く。
func (m model) Init() tea.Cmd {
	return tea.Batch(waitForUI(m.uiChan), tickCmd())
}

// --- tea.Model インターフェース: Update ---
// Update は「何かメッセージが届いたとき」に呼ばれる。msg の型に応じて model を更新し、(model, 次の Cmd) を返す。
// 返す Cmd で毎回「また uiChan と tick を待つ」ようにしているので、常に次のイベントを受け続ける。
// 注意: model は値レシーバなので、フィールドを書き換えた m を返さないと変更が次の View に反映されない。
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// どの case でも「次も同じように待つ」ことが多いので、共通の Cmd を返す関数にしておく
	nextCmd := func() tea.Cmd { return tea.Batch(waitForUI(m.uiChan), tickCmd()) }

	switch msg := msg.(type) {
	case master.StatsUpdate:
		// Worker から Master に送られた統計を TUI 用に保持。全 Worker の RPS を合計して履歴に追加
		m.workerStats[msg.WorkerID] = workerStats{
			success: msg.SuccessCount,
			fail:    msg.FailCount,
			rps:     msg.CurrentRps,
		}
		var totalRPS float64
		for _, ws := range m.workerStats {
			totalRPS += ws.rps
		}
		m.rpsHistory = append(m.rpsHistory, totalRPS)
		if len(m.rpsHistory) > maxRPSHistory {
			m.rpsHistory = m.rpsHistory[1:]
		}
		return m, nextCmd()

	case master.WorkerListChanged:
		// Worker の接続/切断。model には持たず、View で m.server.ListWorkers() を都度呼ぶので、再描画で一覧が更新される
		return m, nextCmd()

	case master.LogLine:
		// Master が logOrSendToUI で送った 1 行をログ枠に追加。maxLogLines を超えたら古い行を先頭から削る
		m.logs = append(m.logs, msg.Message)
		if len(m.logs) > maxLogLines {
			m.logs = m.logs[1:]
		}
		return m, nextCmd()

	case time.Time:
		// tickCmd から届く 1 秒ごとのメッセージ。model は変えず、nextCmd で再描画を促す（View で Uptime 等が更新される）
		return m, nextCmd()

	case tea.KeyMsg:
		// ユーザーが押したキー。Bubble Tea がキー入力を tea.KeyMsg として渡してくれる
		switch msg.String() {
		case "s":
			// 負荷テスト開始: 全 Worker に Start 命令をブロードキャスト（proto の MasterCmd / StartCmd）
			cmd := &proto.MasterCmd{
				Cmd: &proto.MasterCmd_Start{
					Start: &proto.StartCmd{
						TargetUrl:     "https://google.com",
						TotalRequests: 5,
						Concurrency:   1,
					},
				},
			}
			m.server.BroadcastCommand(cmd)
			return m, nextCmd()
		case "q", "ctrl+c":
			// 終了: 全 Worker に Quit を送ってから tea.Quit を返す。Quit は「TUI ループを終了する」という特別な Cmd
			quitCmd := &proto.MasterCmd{
				Cmd: &proto.MasterCmd_Quit{Quit: &proto.QuitCmd{}},
			}
			m.server.BroadcastCommand(quitCmd)
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		// ターミナルリサイズ時に Bubble Tea が送る。幅・高さを model に保存（将来レイアウトに使う）
		m.width = msg.Width
		m.height = msg.Height
		return m, nextCmd()
	}

	return m, nextCmd()
}

// --- tea.Model インターフェース: View ---
// View は「今の model から、画面に出す文字列を 1 つに組み立てて返す」関数。Bubble Tea がこの戻り値をターミナルに描画する。
// 毎回呼ばれるので、スタイルは View 内で都度 NewStyle() している（キャッシュしてもよいが、ここではシンプルに）。
func (m model) View() string {
	// Lipgloss: NewStyle() でスタイルを作り、Bold / Foreground / Border などをメソッドチェーンで指定。
	// Render(s) で文字列 s にそのスタイル（ANSI エスケープ）を適用した結果を返す。
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("86")). // 256 色の水色系
		MarginBottom(1)
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("241")). // グレー
		Padding(0, 1).
		MarginBottom(1)
	footerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		MarginTop(1)

	// ヘッダー: タイトル + 起動からの経過時間（startTime は newModel で time.Now()）
	uptime := time.Since(m.startTime).Round(time.Second)
	uptimeStr := fmt.Sprintf("Uptime: %02d:%02d:%02d",
		int(uptime.Hours()), int(uptime.Minutes())%60, int(uptime.Seconds())%60)
	header := headerStyle.Render("SwarmGo  Master") + "  " + uptimeStr

	// メイン枠: Worker 数（Master に接続中の一覧）・RPS グラフ・成功/失敗の合計
	workers := m.server.ListWorkers()
	workerCount := len(workers)

	var totalSuccess, totalFail int32
	for _, ws := range m.workerStats {
		totalSuccess += ws.success
		totalFail += ws.fail
	}

	mainContent := fmt.Sprintf("Workers: %d\n\n", workerCount)
	mainContent += "Total RPS (realtime)\n"
	mainContent += m.renderRPSGraph() + "\n\n"
	mainContent += fmt.Sprintf("Success: %d   Fail: %d", totalSuccess, totalFail)

	mainBox := boxStyle.Render(mainContent) // 枠線・パディングを付けて 1 つのブロックに

	// ログ枠: Master が uiChan 経由で送った LogLine を時系列で表示（最大 maxLogLines 行）
	logContent := "Log:\n"
	for _, line := range m.logs {
		logContent += "  " + line + "\n"
	}
	if len(m.logs) == 0 {
		logContent += "  (no events yet)\n"
	}
	logBox := boxStyle.Render(logContent)

	footer := footerStyle.Render("Press 's' to start attack, 'q' to quit")

	// 全体を 1 つの文字列にして返す。Bubble Tea がこれをそのままターミナルに出力する
	return header + "\n" + mainBox + "\n" + logBox + "\n" + footer
}

// renderRPSGraph は rpsHistory（直近の合計 RPS の時系列）を ASCII の棒グラフにして返す。
// 縦軸 = RPS の高さ、横軸 = 時間（左が古い、右が新しい）。最大値で正規化し、█ で棒・░ で空白を表現する。
func (m model) renderRPSGraph() string {
	if len(m.rpsHistory) == 0 {
		return "  (no data yet)"
	}
	maxVal := 0.0
	for _, v := range m.rpsHistory {
		if v > maxVal {
			maxVal = v
		}
	}
	if maxVal <= 0 {
		maxVal = 1
	}

	// 上から下へ graphHeight 行を生成。行 i は「各時点の RPS が、その高さ i を超えていれば █、そうでなければ ░」
	lines := make([]string, graphHeight)
	for i := graphHeight - 1; i >= 0; i-- {
		row := ""
		for _, rps := range m.rpsHistory {
			norm := rps / maxVal
			barHeight := int(norm * float64(graphHeight))
			if barHeight > i {
				row += "█"
			} else {
				row += "░"
			}
		}
		lines[graphHeight-1-i] = row
	}
	out := ""
	for _, line := range lines {
		out += line + "\n"
	}
	lastRPS := m.rpsHistory[len(m.rpsHistory)-1]
	out += fmt.Sprintf(" %.1f RPS (total)", lastRPS)
	return out
}
