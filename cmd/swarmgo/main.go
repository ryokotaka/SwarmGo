package main

import (
	"flag"
	"fmt"
	"os"

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
	fmt.Fprintln(os.Stderr, "  master  - start the Master gRPC server (TUI). Options: -p port (default 50051)")
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
	// "swarmgo master -p 50051" の「-p 50051」の部分だけをここで解釈する。
	// master 用と worker 用でオプションを分けて、取り違えないようにしている。
	masterCmd := flag.NewFlagSet("master", flag.ExitOnError)
	port := masterCmd.String("p", "50051", "Port to listen on")
	masterCmd.Parse(os.Args[2:])

	// gRPC の「接続待ち」は別の処理として動かし、ここではすぐに Master の窓口（srv）を受け取る。
	// そうしないと、この先の「画面を表示する」処理にたどり着けない。
	srv, err := master.RunGRPCServer(*port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "master: %v\n", err)
		os.Exit(1)
	}

	// Worker がつながった・結果を送った・ログを出した、などの出来事を
	// Master がこの「管」に流す。画面側がその管から読んで、表示を更新する。
	uiChan := make(chan interface{}, 300)
	srv.SetUIChan(uiChan)

	// 画面（TUI）を起動する。srv と uiChan を渡すので、画面から「開始」を押すと
	// 同じ Master が全 Worker に命令を送れる。p.Run() の間、ここでずっと画面を表示し続ける。
	p := tea.NewProgram(newModel(srv, uiChan), tea.WithAltScreen())
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
