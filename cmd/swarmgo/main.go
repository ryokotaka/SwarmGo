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
    // os.Args holds CLI arguments: index 0 is the executable name, index 1 is the first argument
    if len(os.Args) < 2 {
        printHelp()
        os.Exit(1)
    }

    // Determine command (subcommand "master"/"worker" or -mode=master / -mode=worker)
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

// runMaster starts the Master with the TUI.
//
// Order of operations:
//  1. Set port via -p (e.g. -p 50051)
//  2. Start the gRPC endpoint in the background (do not block here)
//  3. Create a channel for Master -> TUI messages and pass it to the Master
//  4. Show the dashboard in the terminal (s to start test, q to quit)
func runMaster() {
	masterCmd := flag.NewFlagSet("master", flag.ExitOnError)
	port := masterCmd.String("p", "50051", "Port to listen on")
	// Defaults for -url / -n / -c come from TARGET_URL / TOTAL_REQUESTS / CONCURRENCY (easy to override in Docker etc.)
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
		// Headless: start only the gRPC server; logs go to stdout via log.Printf
		if err := master.StartGRPCServer(*port); err != nil {
			fmt.Fprintf(os.Stderr, "master: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// With TUI: start gRPC in a separate goroutine and show the dashboard
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
