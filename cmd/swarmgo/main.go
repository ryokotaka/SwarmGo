package main

import (
    "flag"
    "fmt"
    "log"
    "os"

    "github.com/charmbracelet/bubbletea"
    "github.com/ryokotaka/SwarmGo/internal/master"
    "github.com/ryokotaka/SwarmGo/internal/worker"
)

func main() {
    // 引数が足りない場合は、ヘルプを表示して終了するか、デフォルト動作にする
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
        // Masterモードのフラグ解析と実行
        runMaster()
    case "worker":
        // Workerモードのフラグ解析と実行
        runWorker()
    default:
        // 従来のスタンドアローンモード（引数がURLなどの場合）として扱うか、
        // あるいは "standalone" というサブコマンドを作るか。
        // ここでは互換性のため、サブコマンドなしはスタンドアローンとみなす実装も可能ですが、
        // 明示的に分けたほうが設計としては綺麗です。今回はヘルプを出します。
        fmt.Printf("Unknown command: %s\n", os.Args[1])
        printHelp()
        os.Exit(1)
    }
}

// cmd/swarmgo/main.go の runMaster 部分（Bubble Tea TUI で起動）

func runMaster() {
    masterCmd := flag.NewFlagSet("master", flag.ExitOnError)
    port := masterCmd.String("p", "50051", "Port to listen on")
    masterCmd.Parse(os.Args[2:])

    // gRPC サーバーを別 Goroutine で起動し、*Server を取得
    srv, err := master.RunGRPCServer(*port)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Master error: %v\n", err)
        os.Exit(1)
    }

    // TUI へイベント（Stats / Worker接続・切断 / ログ）を送るチャネル（バッファで gRPC をブロックさせない）
    uiChan := make(chan interface{}, 256)
    srv.SetUIChan(uiChan)

    // Bubble Tea TUI 起動
    p := tea.NewProgram(newModel(srv, uiChan), tea.WithAltScreen())
    if _, err := p.Run(); err != nil {
        fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
        os.Exit(1)
    }
}

func runWorker() {
    // 環境変数 "MASTER_ADDR" があればそれを使い、なければ localhost を使う
    masterAddr := os.Getenv("MASTER_ADDR")
    if masterAddr == "" {
        masterAddr = "localhost:50051"
    }

    log.Printf("Connecting to Master at %s...", masterAddr)
    client, err := worker.NewGRPCClient(masterAddr)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Worker error: %v\n", err)
        os.Exit(1)
    }
    if err := client.Start(); err != nil {
        fmt.Fprintf(os.Stderr, "Worker error: %v\n", err)
        os.Exit(1)
    }
}

func printHelp() {
    fmt.Println("Usage: swarmgo <command> [options]")
    fmt.Println("Commands:")
    fmt.Println("  master    Start in Master mode (Commander)")
    fmt.Println("  worker    Start in Worker mode (Load Generator)")
    fmt.Println("  help      Show this help message")
}