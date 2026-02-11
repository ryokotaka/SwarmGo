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

	// 「やめ」の合図を受け取れるようにする（Ctrl+C や kill で止めるとき用）		
	// cancel を呼ぶと、まだ始まっていない処理はやめて、今やっている処理は終わるまで待つ
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // main が終わるときに必ず cancel を呼び、子の Goroutine に終了を伝える

	// Ctrl+C（SIGINT）や kill（SIGTERM）が来たら cancel を呼ぶようにする
	// 受け取る goroutine が <-sigCh に到達するより先にシグナルが来る可能性があるので、バッファ1をつける
	sigCh := make(chan os.Signal, 1)

	// signal.Notify(チャネル, シグナル...) で、チャネルにシグナルが来たらチャネルに値を送る。
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// メインの処理の裏で、シグナルが来たら終了するための goroutine を起動する。
	go func() {
		sig := <-sigCh                    // シグナルが sigCh に送られるまでここで止まり、届いたらその値を sig に入れる
		fmt.Fprintf(os.Stderr, "received %v, shutting down gracefully...\n", sig)
		cancel()                          // 終了の合図を送る（Run 側で ctx.Done() が閉じる）
	}()
	
	// 負荷試験を実行する MyRunner を作り、MyRun で実際にリクエストを送る
	myRunner := worker.NewMyRunner()
	sum, err := myRunner.MyRun(ctx, *url, *totalRequests, *concurrency)

	// runnner.go 54-56行目のエラー時の処理(引数チェック)
	if err != nil {
		fmt.Fprintln(os.Stderr, "run error:", err) // Run がエラーを返したらメッセージを出して異常終了
		os.Exit(1)
	}
	
	


}
