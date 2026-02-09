package worker

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// 1リクエストの結果を表す
type Result struct {
	StatusCode int // HTTPステータスコードを返すため
	Duration time.Duration // リクエスト開始からレスポンス受信完了までのかかった時間
	Err error // 通信そのものの失敗
}

// Run 完了後の集計結果を表す。
// スピード優先にするために、単一のゴルーチンからのみ更新する(ロック不要)。
type Summary struct {
	Total         int            // 実行したリクエスト総数
	Success       int            // エラーなく完了した回数
	Failed        int            // エラーのあった回数
	StatusCodeCnt map[int]int    // ステータスコード別の回数（[どのステータスコード]が何回出たかというペアを管理）
	TotalDuration time.Duration  // 全リクエストの Duration の合計時間（平均算出用）
}