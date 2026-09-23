# wrk screening

This check adds a tool focused on raw HTTP throughput, so beating k6 is not mistaken for beating all open-source load generators.

**SwarmGo has not beaten wrk on the high-concurrency workload.** An exploratory wrk 4.2.0 run with 20,000 connections and a 200 ms target delay reported **93,738 completed requests/s with zero reported errors**. It reached 20,000 simultaneous handlers and used 259.7 MiB peak generator cgroup memory. SwarmGo's five-run median on the corresponding fixed-count workload was 71,548 requests/s.

| Concurrency | Target delay | Client requests/s | Completed at client | Reported errors |
| ---: | ---: | ---: | ---: | --- |
| 20,000 | 200 ms | 93,738 | 3,288,744 | 0 |
| 1,024 | 0 ms | 190,212 | 6,674,951 | 70 timeouts |

The faster no-delay run **does not meet the zero-error criterion**. The full output is retained in [results.json](results.json).

Both runs used 8 threads, 35 seconds, HTTP/1.1, one 1 KiB JSON POST per request, a 1 KiB response, and a 30-second timeout. The Lua script sets a fixed request body once; there is no per-response callback. Containers had no CPU or memory limits in the same 10-CPU / 7.65 GiB ARM64 Docker VM on Apple M4. Traffic stayed on an internal Docker network, and the target found no invalid methods, protocols or request bodies.

These are screening runs, one per setting. wrk uses a fixed duration and reports client completions; the SwarmGo/k6 comparison uses a fixed total count and target-side timing. The target can complete requests after wrk stops reading, so its final count is higher than the client's. These numbers identify a remaining performance gap; they are not used to claim a precise cross-tool speedup.

## Reproduce

Use Go 1.25.7, Python 3, Git and ARM64 Docker. The image is built locally from the official wrk source and Debian packages; no cloud builder or paid account is used.

```bash
cd benchmarks/wrk
git clone --depth 1 --branch 4.2.0 https://github.com/wg/wrk.git wrk-src
# Recorded commit: a211dd5a7050b1f9e8a9870b95513060e72ac4a0
mkdir -p bin
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bin/target target.go
docker build -t swarmgo-wrk-local:4.2.0 -f Dockerfile.wrk .
python3 bench.py --concurrency 20000 --delay-ms 200 --seconds 35 --out delayed
python3 bench.py --concurrency 1024 --delay-ms 0 --seconds 35 --out immediate
```

The second recorded run exited unsuccessfully because the script rejects any reported errors. It saves the record before checking that condition. Dependencies come from Debian bookworm; installed package revisions may change. The raw records retain the measured image and binary hashes.

[wrk's source and usage documentation](https://github.com/wg/wrk) describe its multithreaded event-loop design and its fixed-duration execution model.
