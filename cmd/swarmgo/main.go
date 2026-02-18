package main

import (
	"flag"
	"fmt"
	"os"

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
	fmt.Fprintln(os.Stderr, "  master  - start the Master gRPC server")
	fmt.Fprintln(os.Stderr, "  worker  - connect to Master and run load test tasks")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "master options:")
	flag.PrintDefaults()
}

func runMaster() {
	port := flag.String("p", "50051", "gRPC listen port")
	flag.CommandLine.Parse(os.Args[2:])
	if err := master.StartGRPCServer(*port); err != nil {
		fmt.Fprintf(os.Stderr, "master: %v\n", err)
		os.Exit(1)
	}
}

func runWorker() {
	addr := os.Getenv("MASTER_ADDR")
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
