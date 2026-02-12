```markdown
# SwarmGo

A lightweight, high-performance load testing tool written in Go.
Designed with concurrency patterns (Worker Pool) and graceful shutdown capabilities.

![Demo](demo.gif)

## 🚀 Features

- **Concurrent Execution**: Uses Go routines and channels (Worker Pool pattern) for efficient load generation.
- **Graceful Shutdown**: Handles signals (SIGINT/SIGTERM) to safely stop ongoing requests.
- **Real-time Metrics**: Calculates RPS (Requests Per Second) and Mean Latency.
- **Resource Efficient**: Reuse TCP connections with a custom HTTP Transport configuration.

## 🛠 Architecture

```mermaid
graph TD
    User((User)) -->|Start Command| Main[Main Process]
    subgraph "SwarmGo Worker Node"
        Main -->|Init| Runner
        Runner -->|Dispatch| JobChannel[Job Channel]
        subgraph "Worker Pool"
            W1[Worker 1]
            W2[Worker 2]
            WX[Worker ...]
        end
        JobChannel --> W1 & W2 & WX
        W1 & W2 & WX -->|HTTP Req| Target[Target Service]
        W1 & W2 & WX -->|Result| ResChannel[Result Channel]
        ResChannel -->|Aggregate| Summary
    end
    Summary -->|Report| Output[Console]
📦 InstallationBashgit clone https://github.com/ryokotaka/SwarmGo.git
cd SwarmGo
go mod tidy
📖 UsageRun the worker with the target URL, total requests, and concurrency level.Bashgo run cmd/worker/main.go -url https://example.com -n 100 -c 10
OptionsFlagDescriptionDefault-urlTarget URL to test(Required)-nTotal number of requests0-cNumber of concurrent executions0📊 Output ExamplePlaintextSummary:
  Total Requests: 10
  Success:        10
  Failed:         0
  Total Duration: 116.75ms
--------------------------------------------------
  RPS:            85.65 req/s
  Mean Latency:   23.26ms
--------------------------------------------------
Status codes:
  200: 10
📜 LicenseMIT
---

