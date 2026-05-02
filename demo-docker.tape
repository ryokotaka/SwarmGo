

Output demo-docker.gif

Require docker

# README demo: show a local-only higher-load run and a local-only error reason.
Set FontSize 18
Set Width 1280
Set Height 800
Set Padding 22
Set Framerate 18
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful

# Fast enough for a README, slow enough to read command intent.
Set TypingSpeed 45ms

# Start from a known state and warm the image build without recording noisy logs.
Hide
Type "docker compose down --remove-orphans >/dev/null 2>&1 || true" Enter
Sleep 2s
Type "docker compose build >/dev/null 2>&1" Enter
Sleep 20s
Type "clear" Enter
Sleep 0.5s
Show

# Run a higher-load success case against the local target-server only.
Type "TOTAL_REQUESTS=5000 CONCURRENCY=20 docker compose up -d --build --scale worker=5" Enter
Hide
Sleep 8s
Type "printf '\033[?25l'" Enter
Sleep 0.2s
Type "clear" Enter
Sleep 0.5s
Show
Sleep 0.5s

# Attach to the Master TUI.
Type "docker attach $(docker compose ps -q master)" Enter
Sleep 5s

# Start the load test once viewers can recognize the dashboard.
Sleep 1s
Type "s"
Sleep 11s

# End the first run and clean up before the failure example.
Hide
Type "q"
Sleep 1s
Type "docker compose down --remove-orphans >/dev/null 2>&1 || true" Enter
Sleep 4s
Type "clear" Enter
Sleep 0.5s
Show

# Run a local-only mixed-success/failure case to show grouped error reasons.
Type "TARGET_URL='http://target-server/?echo_code=200-200-404-500' TOTAL_REQUESTS=300 CONCURRENCY=10 docker compose up -d --build --scale worker=3" Enter
Hide
Sleep 8s
Type "printf '\033[?25l'" Enter
Sleep 0.2s
Type "clear" Enter
Sleep 0.5s
Show
Sleep 0.5s

Type "docker attach $(docker compose ps -q master)" Enter
Sleep 4s
Sleep 1s
Type "s"
Sleep 5s

# Leave the error summary on screen; cleanup is hidden.
Hide
Type "q"
Sleep 1s
Type "docker compose down --remove-orphans >/dev/null 2>&1 || true" Enter
Sleep 3s
