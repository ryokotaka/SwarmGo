# SwarmGo

A lightweight, high-performance load testing tool written in Go.
Designed with concurrency patterns (Worker Pool) and graceful shutdown capabilities.

![Demo](demo.gif)

## 🚀 Features

- **Concurrent Execution**: Uses Go routines and channels (Worker Pool pattern) for efficient load generation.
- **Graceful Shutdown**: Handles signals (SIGINT/SIGTERM) to safely stop ongoing requests.
- **Real-time Metrics**: Calculates RPS (Requests Per Second) and Mean Latency.

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

## 📦 Installation

git clone [https://github.com/ryokotaka/SwarmGo.git](https://github.com/ryokotaka/SwarmGo.git)
cd SwarmGo
go mod tidy

## 📖 Usage
# Syntax
worker -url <Target_URL> -n <Total_Requests> -c <Concurrency>

# Example: Send 100 requests to example.com with 10 concurrent workers
worker -url [https://example.com](https://example.com) -n 100 -c 10

### Options
Flag,Description,Default
-url,Target URL to test,(Required)
-n,Total number of requests,0
-c,Number of concurrent executions,0

## 📊 Output Example
Summary:
  Total Requests: 100
  Success:        100
  Failed:         0
  Total Duration: 5.2s
--------------------------------------------------
  RPS:            85.65 req/s
  Mean Latency:   23.26ms
--------------------------------------------------
Status codes:
  200: 100


## 📜 License
MIT


