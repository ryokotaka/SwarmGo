Output demo.gif
Set FontSize 18
Set Width 1200
Set Height 600
Set Padding 20
Set Theme "Monokai Vivid"

# コマンドを入力する様子をシミュレート
Type "go run cmd/worker/main.go -url https://google.com -n 50 -c 5"
Sleep 1s
Enter

# 実行完了を待つ（5秒間）
Sleep 5s
