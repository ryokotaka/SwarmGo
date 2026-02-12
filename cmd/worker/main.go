package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ryokotaka/SwarmGo/internal/worker" 

)

// Entry point of the program.
func main() {
	
	// Define flags.
	url := flag.String("url", "", "Target URL")
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

	// Set up to receive a shutdown signal (e.g. Ctrl+C or kill).
	// Calling cancel stops any not-yet-started work and waits for in-flight work to finish.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure cancel is called when main exits so child goroutines are notified to stop.

	// When Ctrl+C (SIGINT) or kill (SIGTERM) is received, call cancel.
	// Use a buffer of 1 so that a signal can be sent before the receiving goroutine reaches <-sigCh.
	sigCh := make(chan os.Signal, 1)

	// signal.Notify(channel, signals...) sends to the channel when the given signals are received.
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start a goroutine that shuts down when a signal is received, in parallel with the main work.
	go func() {
		sig := <-sigCh                    // Block until a signal is sent on sigCh, then assign it to sig.
		fmt.Fprintf(os.Stderr, "received %v, shutting down gracefully...\n", sig)
		cancel()                          // Send the shutdown signal (Run will see ctx.Done() closed).
	}()
	
	// Create a MyRunner to run the load test and send requests via MyRun.
	myRunner := worker.NewMyRunner()
	mySum, myErr := myRunner.MyRun(ctx, *url, *totalRequests, *concurrency)

	// Error handling for invalid arguments (see runner.go lines 54-56).
	if myErr != nil {
		fmt.Fprintln(os.Stderr, "run error:", myErr) // On Run error, print message and exit with failure.
		os.Exit(1)
	}
	
	// Print results (success count, failure count, status code breakdown, etc.).
	fmt.Printf("Total: %d, Success: %d, Failed: %d, TotalDuration: %s\n",
		mySum.MyTotal, mySum.MySuccess, mySum.MyFailed, mySum.MyTotalDuration)
	if len(mySum.MyStatusCodeCnt) > 0 { // Only show status code breakdown when there is any.
		fmt.Println("Status codes:")
		for code, cnt := range mySum.MyStatusCodeCnt {
			fmt.Printf("  %d: %d\n", code, cnt)
		}
	}
}	