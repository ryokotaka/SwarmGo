// Master 用 Bubble Tea TUI（ダッシュボード表示）

package main

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ryokotaka/SwarmGo/internal/master"
	"github.com/ryokotaka/SwarmGo/proto"
)

const (
	maxRPSHistory = 40
	graphHeight   = 8
	maxLogLines   = 10
)

type workerStats struct {
	success, fail int32
	rps           float64
}

type model struct {
	server      *master.Server
	uiChan      chan interface{}
	workerStats map[string]workerStats
	rpsHistory  []float64
	logs        []string
	startTime   time.Time
	width       int
	height      int
}

// waitForUI は gRPC/Server からイベントが届いたときに Model へ渡す Cmd
func waitForUI(ch chan interface{}) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// tickCmd は毎秒 Uptime 更新用に再描画を促す Cmd
func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return t
	})
}

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

func (m model) Init() tea.Cmd {
	return tea.Batch(waitForUI(m.uiChan), tickCmd())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// 毎回再登録する Cmd（Uptime 更新用 Tick と UI イベント待ち）
	nextCmd := func() tea.Cmd { return tea.Batch(waitForUI(m.uiChan), tickCmd()) }

	switch msg := msg.(type) {
	case master.StatsUpdate:
		// Worker から届いた Stats で集計を更新し、合計 RPS をグラフ用に追加
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
		// 接続数が変わっただけなので再描画する（View で ListWorkers() を呼ぶ）
		return m, nextCmd()

	case master.LogLine:
		m.logs = append(m.logs, msg.Message)
		if len(m.logs) > maxLogLines {
			m.logs = m.logs[1:]
		}
		return m, nextCmd()

	case time.Time:
		// Tick: Uptime を更新するために再描画
		return m, nextCmd()

	case tea.KeyMsg:
		switch msg.String() {
		case "s":
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
			quitCmd := &proto.MasterCmd{
				Cmd: &proto.MasterCmd_Quit{Quit: &proto.QuitCmd{}},
			}
			m.server.BroadcastCommand(quitCmd)
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nextCmd()
	}

	return m, nextCmd()
}

func (m model) View() string {
	// スタイル
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("86")).
		MarginBottom(1)
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("241")).
		Padding(0, 1).
		MarginBottom(1)
	footerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		MarginTop(1)

	// ヘッダー: ロゴ + 稼働時間
	uptime := time.Since(m.startTime).Round(time.Second)
	uptimeStr := fmt.Sprintf("Uptime: %02d:%02d:%02d",
		int(uptime.Hours()), int(uptime.Minutes())%60, int(uptime.Seconds())%60)
	header := headerStyle.Render("🐝 SwarmGo  Master") + "  " + uptimeStr

	// メイン: 接続数 / RPS グラフ / 成功・失敗カウンター
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

	mainBox := boxStyle.Render(mainContent)

	// ログウィンドウ（サーバーからの接続/切断等を TUI 内に表示し、log.Printf でレイアウトを崩さない）
	logContent := "Log:\n"
	for _, line := range m.logs {
		logContent += "  " + line + "\n"
	}
	if len(m.logs) == 0 {
		logContent += "  (no events yet)\n"
	}
	logBox := boxStyle.Render(logContent)

	// フッター
	footer := footerStyle.Render("Press 's' to start attack, 'q' to quit")

	return header + "\n" + mainBox + "\n" + logBox + "\n" + footer
}

// renderRPSGraph は直近の合計 RPS をテキストの棒グラフで描画する
func (m model) renderRPSGraph() string {
	if len(m.rpsHistory) == 0 {
		return "  (no data yet)"
	}
	// 最大値で正規化（0 除算回避）
	maxVal := 0.0
	for _, v := range m.rpsHistory {
		if v > maxVal {
			maxVal = v
		}
	}
	if maxVal <= 0 {
		maxVal = 1
	}

	// 縦棒: 下から上に graphHeight 段。各サンプルを 1 段の高さで表現
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
	// 最新の合計 RPS を表示
	lastRPS := m.rpsHistory[len(m.rpsHistory)-1]
	out += fmt.Sprintf(" %.1f RPS (total)", lastRPS)
	return out
}
