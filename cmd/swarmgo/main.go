package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/charmbracelet/bubbletea"
	"github.com/ryokotaka/SwarmGo/internal/master"
	"github.com/ryokotaka/SwarmGo/internal/worker"
)

func main() {
    // 「os.Args」はCLI引数を取得し、0番目が実行ファイル名、1番目が引数
    if len(os.Args) < 2 {
        printHelp()
        os.Exit(1)
    }

    // コマンドを決定する（サブコマンド "master"/"worker" または -mode=master / -mode=worker）
    cmd := os.Args[1]
    if cmd == "-mode=master" || cmd == "--mode=master" {
        cmd = "master"
    } else if cmd == "-mode=worker" || cmd == "--mode=worker" {
        cmd = "worker"
    }

	switch cmd {
	case "master":
		runMaster()
	case "worker":
		runWorker()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Fprintln(os.Stderr, "usage: swarmgo <master|worker> [options]")
	fmt.Fprintln(os.Stderr, "  master  - start the Master gRPC server. Options: -p port, -url target URL, -n total requests, -c concurrency, -no-tui (headless)")
	fmt.Fprintln(os.Stderr, "  worker  - connect to Master and run load test tasks. Option: -addr (or MASTER_ADDR, default localhost:50051)")
}

// runMaster は「親」の Master を、画面付きで起動する。
//
// やることの順番:
//  1. オプション -p でポート番号を決める（例: -p 50051）
//  2. Worker がつながってくる gRPC の窓口を、裏で開いておく（ここでは待たずに次へ進む）
//  3. 「Master から画面へ伝言を届ける管」を用意して、Master に渡す
//  4. ターミナルにダッシュボードを表示する（s でテスト開始、q で終了）
func runMaster() {
	masterCmd := flag.NewFlagSet("master", flag.ExitOnError)
	port := masterCmd.String("p", "50051", "Port to listen on")
	// -url / -n / -c のデフォルトは環境変数 TARGET_URL / TOTAL_REQUESTS / CONCURRENCY を参照（Docker 等で上書きしやすい）
	urlDefault := os.Getenv("TARGET_URL")
	if urlDefault == "" {
		urlDefault = "https://example.com"
	}
	nDefault := 5
	if s := os.Getenv("TOTAL_REQUESTS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			nDefault = v
		}
	}
	cDefault := 1
	if s := os.Getenv("CONCURRENCY"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			cDefault = v
		}
	}
	url := masterCmd.String("url", urlDefault, "Target URL for load test (default: TARGET_URL or https://example.com)")
	n := masterCmd.Int("n", nDefault, "Total requests per run per Worker (default: TOTAL_REQUESTS or 5)")
	c := masterCmd.Int("c", cDefault, "Concurrency per Worker (default: CONCURRENCY or 1)")
	noTUI := masterCmd.Bool("no-tui", false, "Run without TUI (headless); gRPC only, log to stdout")
	masterCmd.Parse(os.Args[2:])

	if *noTUI {
		// ヘッドレス: gRPC サーバーだけ起動し、ログは log.Printf で標準出力へ
		if err := master.StartGRPCServer(*port); err != nil {
			fmt.Fprintf(os.Stderr, "master: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// TUI 付き: gRPC を別 goroutine で起動し、ダッシュボードを表示
	srv, err := master.RunGRPCServer(*port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "master: %v\n", err)
		os.Exit(1)
	}

	uiChan := make(chan interface{}, 300)
	srv.SetUIChan(uiChan)

	p := tea.NewProgram(newModel(srv, uiChan, *url, *n, *c), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI: %v\n", err)
		os.Exit(1)
	}
}

func runWorker() {
	workerCmd := flag.NewFlagSet("worker", flag.ExitOnError)
	addrFlag := workerCmd.String("addr", "", "Master address (host:port). Overrides MASTER_ADDR.")
	workerCmd.Parse(os.Args[2:])

	addr := *addrFlag
	if addr == "" {
		addr = os.Getenv("MASTER_ADDR")
	}
	if addr == "" {
		addr = "localhost:50051"
	}

	client, err := worker.NewGRPCClient(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker: %v\n", err)
		os.Exit(1)
	}
	if err := client.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "worker: %v\n", err)
		os.Exit(1)
	}
}
