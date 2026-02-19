# SwarmGo 究極のデモ用台本
# 実行: vhs demo.tape

Output demo.gif

# --- 基本設定 ---
Set FontSize 16       # 少し大きくして読みやすく
Set Width 1280
Set Height 800        # 少し高さを出して余裕を持たせる
Set Padding 30
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful
Set TypingSpeed 80ms  # 人間味のあるタイピング速度
Set CursorBlink true

# --- 録画開始前の隠しセットアップ ---
Hide
Type "tmux new -s swarmgo_demo" Enter
Type "clear" Enter
Show

# --- シーン 1: イントロダクション ---
Type "echo '🚀 Starting SwarmGo: Distributed Load Testing Demo...'" Enter
Sleep 2s
Type "clear" Enter

# --- シーン 2: ビルド工程 ---
Type "echo '# Step 1: Building the binary...'" Enter
Sleep 1s
# 特定のコマンドだけゆっくり打つことで重要性を強調
Type@150ms "go build -o swarmgo ./cmd/swarmgo/" Enter
Sleep 3s
Type "clear" Enter

# --- シーン 3: Masterの起動とレイアウト構築 ---
# 画面分割
Type "tmux split-window -h -p 35" Enter
Sleep 1s
Type "tmux select-pane -L" Enter

Type "echo '# 2. Start Master Node (TUI Mode)'" Enter
Sleep 1s
Type "./swarmgo master -p 50051" Enter
# Masterが立ち上がるまでしっかり待機
Sleep 4s

# --- シーン 4: Workerの接続（ここが分散システムの醍醐味） ---
Type "tmux select-pane -R" Enter
Type "echo '# 3. Start Worker Node'" Enter
Sleep 1.5s
Type "./swarmgo worker" Enter
# Master TUIに "Worker connected" と表示されるのを待つ
Sleep 4s

# --- シーン 5: 負荷テスト開始（ハイライトシーン） ---
Type "tmux select-pane -L" Enter
Sleep 1s

# 視聴者に「今からsを押すよ」と意識させるメッセージ
# 画面の下部に表示されるイメージ
Type "echo 'READY TO START... (Pressing [s])'" Sleep 2s
Type "clear" Enter # TUIが崩れないように必要なら調整

# 's' キーをゆっくり打つことで「押した感」を出す
Type@500ms "s"
Sleep 1s

# --- シーン 6: モニタリング（じっくり見せる） ---
# 数値が動くところを視聴者が把握できるよう長めに確保
Sleep 12s

# --- シーン 7: 終了とクリーンアップ ---
# 最後に「q」でエレガントに終了
Type "echo 'FINISHING... (Pressing [q])'" Sleep 1.5s
Type@500ms "q"
Sleep 2s

# 完了メッセージ
Type "clear" Enter
Type "echo '✨ Load test complete. All workers disconnected safely.'" Enter
Sleep 3s

# --- 片付け ---
Hide
Type "tmux kill-session -t swarmgo_demo" Enter