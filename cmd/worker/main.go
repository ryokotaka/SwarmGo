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
	
    // フラグが正しく設定されていない場合、エラーを表示して終了する。
	// F = 出力先の指定
	// os.Stderr = 標準エラー(result.txtには保存せず、ターミナルにエラーと表示する)
	if *url == "" || *totalRequests <= 0 || *concurrency <= 0 {

		// 使い方の一行サンプルをエラー出力に出す。
		fmt.Fprintln(os.Stderr, "usage: worker -url <URL> -n <totalRequests> -c <concurrency>")

		// 3つのオプションの説明とデフォルト値をエラー出力に出す。
		flag.PrintDefaults()
		os.Exit(1)
	}
