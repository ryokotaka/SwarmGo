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
        // Masterモードのフラグ解析と実行
        runMaster()
    case "worker":
        // Workerモードのフラグ解析と実行
        runWorker()
    default:

        fmt.Printf("Unknown command: %s\n", os.Args[1])
        printHelp()
        os.Exit(1)
    }
}

