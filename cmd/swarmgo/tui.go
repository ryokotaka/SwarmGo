// Bubble Tea TUI for the Master (dashboard display).
//
// === Role of this file ===
//   - Display Master state (Worker count, RPS, logs) in the terminal
//   - Forward key input (s=start, q=quit) to the Master, which broadcasts commands to all Workers
//
// === Data flow ===
//   - internal/master sends StatsUpdate / WorkerListChanged / LogLine on uiChan → received in Update
//   - User presses a key → Update calls m.server.BroadcastCommand(cmd) → Master sends to all Workers
//
// === Libraries in use ===
//
// [Bubble Tea] https://github.com/charmbracelet/bubbletea
//   Framework for writing TUIs in an Elm-like architecture.
//   Core idea: drive the UI with state (Model) and messages (Msg).
//
//   - Model: type holding all state needed for the UI; here, the model struct.
//   - Msg: notification that something happened (key input, timer, external channel); passed to Update.
//   - Cmd: request to "send one message later" (e.g. tea.Tick, custom channel wait).
//
//   Loop:
//     1. Init() returns the initial Cmd (e.g. channel wait + timer)
//     2. When that Cmd completes, a Msg is delivered → Update(msg) is called
//     3. Update returns (new model, next Cmd)
//     4. View() is called and builds the display string from the model → drawn to terminal
//     5. Back to 2 (wait for next Msg). Returning tea.Quit ends the loop.
//
// [Lipgloss] https://github.com/charmbracelet/lipgloss
//   Library for declarative terminal styling.
//   Core idea: build a Style and apply it with Render(text).
//
//   - NewStyle() creates a Style; chain Bold, Foreground, Border, Padding, etc.
//   - Render(s) returns string s with that style applied (ANSI escape codes)
//   - Colors are specified as strings, e.g. lipgloss.Color("86") (256-color or names like "red")
//
package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ryokotaka/SwarmGo/internal/master"
	"github.com/ryokotaka/SwarmGo/proto"
)

// --- Constants (display limits, graph size) ---
const (
	maxRPSHistory     = 40 // max number of RPS values in the graph (horizontal bars)
	graphHeight       = 8  // graph height in lines (character rows)
	maxLogLines       = 10 // max lines in the log panel (older lines dropped when exceeded)
	topErrorReasons   = 5  // number of top error reasons to show
	maxErrorReasonLen = 60 // max display length for error reason strings (truncated if longer)
)

// --- Types that make up the Bubble Tea Model ---
// workerStats holds aggregate values for one Worker (success/fail counts, current RPS, latency percentiles).
type workerStats struct {
	success, fail   int32
	rps             float64
	latencyP50Ms    int32
	latencyP90Ms    int32
	latencyP99Ms    int32
}

// model is the Bubble Tea "UI state" type.
// It implements the tea.Model interface as the receiver for Init, Update, and View.
// Fields are only what View needs to render and what Update needs to call the Master on key input.
type model struct {
	server               *master.Server   // Master instance; used for ListWorkers() and BroadcastCommand(cmd)
	uiChan               chan interface{} // channel for Master -> TUI events (same one passed to SetUIChan in main)
	workerStats          map[string]workerStats // Worker ID -> that Worker's stats (updated by StatsUpdate)
	rpsHistory           []float64        // recent total RPS history; rendered as bar graph in renderRPSGraph
	logs                 []string         // lines shown in the log panel (LogLine adds; old lines dropped)
	startTime            time.Time        // TUI start time; used in View for Uptime
	width                int              // terminal width (set by WindowSizeMsg; for future layout)
	height               int              // terminal height (same)
	defaultTargetURL     string           // target URL for load test when 's' is pressed (from -url at startup)
	defaultTotalRequests int              // total requests per run (from -n)
	defaultConcurrency   int              // concurrency (from -c)
}

// --- Bubble Tea Cmd ("send one message later") ---
//
// Cmd is the type for "when something happens asynchronously, pass the result to Update as a Msg".
// Here we use two kinds: (1) wait for one event from the Master's channel, (2) 1-second timer.

// waitForUI is a Cmd that blocks until one value is received on uiChan and returns it as a tea.Msg.
// Bubble Tea runs this Cmd in a separate goroutine; when a value is received, Update(msg) is called.
// In Update, msg is type-asserted to master.StatsUpdate, WorkerListChanged, LogLine, etc.
func waitForUI(ch chan interface{}) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// tickCmd is a Cmd that sends time.Time as a Msg every second (Bubble Tea's tea.Tick).
// Purpose: trigger periodic redraws. Update returns nextCmd without changing model, so View runs and Uptime/RPS stay up to date.
func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return t
	})
}

// --- Initial state ---
// newModel creates the initial TUI model. Passed from main as tea.NewProgram(newModel(srv, uiChan, url, n, c), ...).
func newModel(srv *master.Server, ch chan interface{}, targetURL string, totalRequests, concurrency int) model {
	return model{
		server:               srv,
		uiChan:               ch,
		workerStats:          make(map[string]workerStats),
		rpsHistory:           make([]float64, 0, maxRPSHistory),
		logs:                 make([]string, 0, maxLogLines),
		startTime:             time.Now(),
		defaultTargetURL:     targetURL,
		defaultTotalRequests: totalRequests,
		defaultConcurrency:   concurrency,
	}
}

// --- tea.Model interface: Init ---
// Init is called once when the TUI starts. It returns a Cmd that describes what events to wait for next.
// tea.Batch combines multiple Cmds; when any one completes, its Msg is delivered once to Update.
// Here we wait for both "one from uiChan" and "1s timer", so Update runs on either Master events or the tick.
func (m model) Init() tea.Cmd {
	return tea.Batch(waitForUI(m.uiChan), tickCmd())
}

// --- tea.Model interface: Update ---
// Update is called when a message is received. It updates the model based on msg type and returns (model, next Cmd).
// The returned Cmd always waits for uiChan and tick again, so we keep receiving the next event.
// Note: model is a value receiver; you must return the modified m or changes won't appear in the next View.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Most cases return the same "wait again" Cmd, so we use a shared helper
	nextCmd := func() tea.Cmd { return tea.Batch(waitForUI(m.uiChan), tickCmd()) }

	switch msg := msg.(type) {
	case master.StatsUpdate:
		// Store stats sent from Worker to Master for the TUI; sum RPS across Workers and append to history
		m.workerStats[msg.WorkerID] = workerStats{
			success:       msg.SuccessCount,
			fail:          msg.FailCount,
			rps:           msg.CurrentRps,
			latencyP50Ms:  msg.LatencyP50Ms,
			latencyP90Ms:  msg.LatencyP90Ms,
			latencyP99Ms:  msg.LatencyP99Ms,
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
		// Worker connected or disconnected; we don't store the list in model, View calls m.server.ListWorkers() each time, so redraw updates the list
		return m, nextCmd()

	case master.LogLine:
		// Append one line sent by Master via logOrSendToUI to the log panel; drop oldest lines when over maxLogLines
		m.logs = append(m.logs, msg.Message)
		if len(m.logs) > maxLogLines {
			m.logs = m.logs[1:]
		}
		return m, nextCmd()

	case time.Time:
		// Periodic message from tickCmd; no model change, nextCmd triggers redraw (View updates Uptime etc.)
		return m, nextCmd()

	case tea.KeyMsg:
		// User key press; Bubble Tea delivers key input as tea.KeyMsg
		switch msg.String() {
		case "s":
			// Start load test: reset workerStats, RPS history, and error reasons so progress shows from 0, then broadcast
			m.workerStats = make(map[string]workerStats)
			m.rpsHistory = m.rpsHistory[:0]
			m.server.ResetErrorReasons()
			cmd := &proto.MasterCmd{
				Cmd: &proto.MasterCmd_Start{
					Start: &proto.StartCmd{
						TargetUrl:     m.defaultTargetURL,
						TotalRequests: int32(m.defaultTotalRequests),
						Concurrency:   int32(m.defaultConcurrency),
					},
				},
			}
			m.server.BroadcastCommand(cmd)
			return m, nextCmd()
		case "q", "ctrl+c":
			// Quit: send Quit to all Workers, then return tea.Quit (special Cmd that ends the TUI loop)
			quitCmd := &proto.MasterCmd{
				Cmd: &proto.MasterCmd_Quit{Quit: &proto.QuitCmd{}},
			}
			m.server.BroadcastCommand(quitCmd)
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		// Sent by Bubble Tea on terminal resize; store width/height in model for future layout use
		m.width = msg.Width
		m.height = msg.Height
		return m, nextCmd()
	}

	return m, nextCmd()
}

// --- tea.Model interface: View ---
// View builds a single string from the current model for display. Bubble Tea draws this to the terminal.
// Called every time; styles are created with NewStyle() inside View (could cache, but kept simple here).
func (m model) View() string {
	// Lipgloss: NewStyle() creates a style; chain Bold, Foreground, Border, etc. Render(s) applies it (ANSI escapes).
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("86")). // 256-color cyan-ish
		MarginBottom(1)
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("241")). // gray
		Padding(0, 1).
		MarginBottom(1)
	footerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		MarginTop(1)

	// Header: title + elapsed time since start (startTime set to time.Now() in newModel)
	uptime := time.Since(m.startTime).Round(time.Second)
	uptimeStr := fmt.Sprintf("Uptime: %02d:%02d:%02d",
		int(uptime.Hours()), int(uptime.Minutes())%60, int(uptime.Seconds())%60)
	header := headerStyle.Render("SwarmGo  Master") + "  " + uptimeStr

	// Main panel: Worker count (list of those connected to Master), RPS graph, total success/fail
	workers := m.server.ListWorkers()
	workerCount := len(workers)

	var totalSuccess, totalFail int32
	var maxP99, repP50, repP90 int32 // for display: use values from the Worker with max P99
	for _, ws := range m.workerStats {
		totalSuccess += ws.success
		totalFail += ws.fail
		if ws.latencyP99Ms > maxP99 {
			maxP99 = ws.latencyP99Ms
			repP50 = ws.latencyP50Ms
			repP90 = ws.latencyP90Ms
		}
	}

	completed := totalSuccess + totalFail
	totalExpected := m.defaultTotalRequests * workerCount
	progressStr := "Progress: -"
	if workerCount > 0 && totalExpected > 0 {
		pct := 0.0
		if totalExpected > 0 {
			pct = 100 * float64(completed) / float64(totalExpected)
		}
		progressStr = fmt.Sprintf("Progress: %d / %d (%.0f%%)", completed, totalExpected, pct)
	}

	latencyStr := "  |  Latency P50: -  P90: -  P99: -"
	if maxP99 > 0 || repP50 > 0 || repP90 > 0 {
		latencyStr = fmt.Sprintf("  |  Latency P50: %d ms  P90: %d ms  P99: %d ms", repP50, repP90, maxP99)
	}

	mainContent := fmt.Sprintf("Workers: %d\n\n", workerCount)
	mainContent += "Total RPS (realtime)\n"
	mainContent += m.renderRPSGraph() + "\n\n"
	mainContent += fmt.Sprintf("Success: %d   Fail: %d   %s%s", totalSuccess, totalFail, progressStr, latencyStr)
	mainContent += m.renderErrorReasons()

	mainBox := boxStyle.Render(mainContent) // one block with border and padding

	// Log panel: show LogLines sent by Master via uiChan in order (up to maxLogLines)
	logContent := "Log:\n"
	for _, line := range m.logs {
		logContent += "  " + line + "\n"
	}
	if len(m.logs) == 0 {
		logContent += "  (no events yet)\n"
		if workerCount == 0 {
			logContent += "  → Start Worker in another terminal: ./swarmgo worker\n"
		}
	}
	logBox := boxStyle.Render(logContent)

	footer := footerStyle.Render(fmt.Sprintf("Target: %s (n=%d, c=%d) | Press 's' to start, 'q' to quit",
		m.defaultTargetURL, m.defaultTotalRequests, m.defaultConcurrency))

	// Return the full screen as one string; Bubble Tea outputs it to the terminal
	return header + "\n" + mainBox + "\n" + logBox + "\n" + footer
}

// renderErrorReasons shows the top topErrorReasons error reasons by count from the Master aggregate; "Errors: None" when zero.
func (m model) renderErrorReasons() string {
	reasons := m.server.GetErrorReasons()
	if len(reasons) == 0 {
		return "\nErrors: None"
	}
	type pair struct {
		msg   string
		count int
	}
	var list []pair
	for msg, count := range reasons {
		list = append(list, pair{msg, count})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].count > list[j].count })
	n := topErrorReasons
	if n > len(list) {
		n = len(list)
	}
	var b strings.Builder
	b.WriteString("\nErrors:\n")
	for i := 0; i < n; i++ {
		msg := list[i].msg
		if len(msg) > maxErrorReasonLen {
			msg = msg[:maxErrorReasonLen] + "..."
		}
		b.WriteString(fmt.Sprintf("  - %s: %d\n", msg, list[i].count))
	}
	return b.String()
}

// renderRPSGraph turns rpsHistory (recent total RPS over time) into an ASCII bar graph.
// Y-axis = RPS magnitude, X-axis = time (oldest left, newest right). Normalized to max; █ = bar, ░ = empty.
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

	// Build graphHeight rows top to bottom; row i: █ if RPS at that time exceeds height i, else ░
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
