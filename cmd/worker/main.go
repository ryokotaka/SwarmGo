package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Entry point of the program.
func main() {
	
	// Define flags.
	url ;= flag.String("url,", "", "Target URL")
	totalRequests := flag.Int("n", 0, "Total number of requests executed")
	concurrency := flag.Int("c", 0, "Number of concurrent executions")
	flag.Parse() // Parse flags and get command line arguments.
	
    // If flags are not set correctly, print an error and exit.
	// os.Stderr: Standard Error (outputs to terminal instead of saving to a file)
	if *url == "" || *totalRequests <= 0 || *concurrency <= 0 {

		// Print usage example to standard error.
		fmt.Fprintln(os.Stderr, "usage: worker -url <URL> -n <totalRequests> -c <concurrency>")

		// Print description and default values of options to standard error.
		flag.PrintDefaults()
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // main が終わるときに必ず cancel を呼び、子の Goroutine に終了を伝える


}
