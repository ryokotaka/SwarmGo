

Output demo-docker.gif

# Appearance settings (Larger FontSize for better readability of typing and results)
Set FontSize 18
Set Width 1280
Set Height 720
Set Padding 24
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful

# Slow down typing speed for better readability (Default is ~50ms)
Set TypingSpeed 120ms

# Start recording (assuming project root)
Type "clear" Enter
Sleep 0.5s


Sleep 1.5s
Type "docker compose up -d --build" Enter
# Wait for build and container startup (expected within 15 seconds)
Sleep 10s

# 2. Briefly show that containers are running (Optional)
Type "docker compose ps" Enter
Sleep 4s


Type "clear && docker attach $(docker compose ps -q master)" Enter
# Wait for TUI to start and Worker connection logs to appear
Sleep 4s

# 4. Start load test with 's' (Pause to let viewers recognize the screen)
# * Tip: Showing a static screen before pressing 's' makes it easier to follow
Sleep 2s
Type "s"
Sleep 2s    # Wait for 2 seconds while the input is displayed
Enter
# Wait until the test finishes and logs/RPS are displayed
Sleep 8s

# 5. Exit TUI with 'q' (Detaches and returns to shell)
Sleep 2s
Type "q"
Sleep 2s

# 6. Cleanup (Stop containers) — clear screen so TUI output and command don't overlap
Type "clear" Enter
Sleep 0.5s
Type "docker compose down" Enter
Sleep 3s