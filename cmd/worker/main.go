package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// 最初に実行される関数。
func main() {
	
	// フラグを定義
	url ;= flag.String("url,", "", "Target URL")
	totalRequests := flag.Int("n", 0, "Total number of requests executed")
	concurrency := flag.Int("c", 0, "Number of concurrent executions")
	flag.Parse() // フラグをパースし、コマンドライン引数を取得。
}