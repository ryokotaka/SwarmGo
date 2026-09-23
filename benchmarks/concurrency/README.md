# 10,000 concurrent HTTP requests

SwarmGo completed **one million POST requests with zero failures in each of five runs**, reaching 10,000 simultaneous requests at the target. Median throughput was **47,129 requests/s** with **605.5 MiB** peak cgroup memory. The controller and worker shared a **2 CPU / 2 GiB** limit.

Under the same limit, **k6 v2.3.0 was killed by the kernel for exceeding the memory limit in all five runs**. Its default Go memory settings were used; memory tuning was not tested. It reached 10,000 concurrent requests before stopping. This compares a fixed HTTP workload within a resource budget; it does not measure the maximum capacity of k6 with more memory.

[The follow-up capacity test](../capacity/) removes the per-container limits and increases concurrency to 20,000.

## Workload and environment

- HTTP/1.1 keep-alive; one million POSTs, concurrency 10,000; 1 KiB JSON request and response bodies.
- The local target waits **200 ms** before replying. At this concurrency and delay, the theoretical ceiling before other overhead is 50,000 requests/s. This is a concurrency test, not an unrestricted maximum-RPS test.
- Both clients read the response to completion without retaining the body. The target validates every request's method, protocol and full body.
- Fixed concurrency: each completed request frees a slot for the next. SwarmGo uses 10,000 goroutines; k6 uses 10,000 VUs with its `shared-iterations` executor. There is no think time or arrival-rate schedule.
- Generator: CPUs 0–1, quota 2 CPU, 2 GiB memory, no extra swap. SwarmGo's controller and one worker run in the same cgroup.
- Target: CPUs 2–5, quota 4 CPU, 2 GiB memory, separate container on the same host. Its average CPU use was about 1.2 cores during the SwarmGo runs.
- Apple M4, ARM64 Docker VM: 10 CPUs, 7.65 GiB RAM; Docker 29.2.0, Linux 6.12.67-linuxkit. Debian image digest and complete build metadata are in [results.json](results.json).
- SwarmGo [`413400f`](https://github.com/ryokotaka/SwarmGo/commit/413400f7294a9a55277783440149cab037a3fceb), Go 1.25.7. Official k6 v2.3.0 Linux ARM64 binary, built with Go 1.26.8.
- Measured 2026-09-23. Five repetitions, alternating tool order, a fresh generator container for every run, no warm-up. Each successful run took about 21.5 seconds including process startup and report generation.

## Every run

Requests below are counted by the target. SwarmGo's final reports independently confirm one million successes and zero failures for each completed run. k6's killed processes could not write their final summaries; the kernel reported `oom_kill=1` and exit status 137 on every run.

| Repetition | Tool | Completed at target | Requests/s | Peak cgroup MiB | Peak concurrent | Outcome |
| --- | --- | ---: | ---: | ---: | ---: | --- |
| 1 | swarmgo | 1,000,000 | 46,936 | 605.5 | 10,000 | completed |
| 1 | k6 | 37,190 | — | 2,048.3 | 10,000 | OOM killed |
| 2 | k6 | 36,413 | — | 2,048.2 | 10,000 | OOM killed |
| 2 | swarmgo | 1,000,000 | 47,375 | 669.6 | 10,000 | completed |
| 3 | swarmgo | 1,000,000 | 47,260 | 605.0 | 10,000 | completed |
| 3 | k6 | 37,816 | — | 2,048.5 | 10,000 | OOM killed |
| 4 | k6 | 60,837 | — | 2,048.3 | 10,000 | OOM killed |
| 4 | swarmgo | 1,000,000 | 46,831 | 607.3 | 10,000 | completed |
| 5 | swarmgo | 1,000,000 | 47,129 | 604.6 | 10,000 | completed |
| 5 | k6 | 38,685 | — | 2,048.6 | 10,000 | OOM killed |

Requests/s uses the interval from the first request handler starting to the last response write. This excludes process startup; `process_seconds` in the raw data includes it. Peak concurrency is observed on the server, not inferred from command-line settings. Peak cgroup memory includes page cache and kernel accounting, so it is not process RSS. Small kernel accounting overshoots can appear above the 2 GiB limit.

The raw results include all five native SwarmGo reports, target counters, CPU accounting and memory events. Failed runs have no comparable throughput figure. Latency percentiles are not compared because the tools' default measurement intervals differ.

## Reproduce locally

Requires Python 3, Git, Go **1.25.7**, and ARM64 Docker with at least 6 CPUs and roughly 8 GiB RAM available to its VM. Results on other hosts may differ.

```bash
cd benchmarks/concurrency
python3 prepare.py
python3 bench.py --out measured
```

`prepare.py` checks out the measured SwarmGo revision, builds it and the target, and downloads the free k6 CLI from its official release. It verifies the release checksum and the recorded k6 binary hash. No cloud account is required.

`bench.py` creates an **internal Docker network with no published ports**. Load stays between the local generator and target containers. It records results in `results/measured/`, then removes only the containers and network it created. An existing result directory is never overwritten. Exit status 1 means at least one trial failed; that is the expected result when reproducing the recorded k6 memory failures. Inspect `results.json` for each tool's outcome.

For a quick setup check where both tools can complete:

```bash
python3 bench.py --requests 10000 --concurrency 100 --repeats 1 --out smoke
```

The measurement originally used [bench-executed.py](bench-executed.py). The packaged `bench.py` adds portable source paths, metadata filtering, argument checks and smoke-test options; its default workload and resource limits are unchanged. The original script expects a sibling SwarmGo checkout and a binary named `swarmgo-improved`.

## Why the connection pool changed

The worker previously retained at most 100 idle connections, even at concurrency 10,000. As responses arrived together, it closed reusable connections and opened replacements. A diagnostic run failed before finishing one million requests, spending most of its CPU time in the kernel.

The pool now retains up to the configured concurrency. A regression test sends two overlapping waves of 150 requests and verifies that the second wave reuses the first wave's connections. The old limit opens 200 connections for that test; the corrected pool needs 150. The five runs above measure the corrected implementation.

## What this comparison covers

The useful result is that a two-core load generator can keep 10,000 HTTP requests in flight and complete this million-request workload within a modest memory budget. It is one workload on one host, lasting about 21 seconds per run. It does not establish long-duration stability, distributed scaling, HTTPS performance or arbitrary application workflows.

[k6 is the engine used by Grafana Cloud k6](https://grafana.com/docs/grafana-cloud/observe-and-act/testing/k6/introduction/), including its paid service. That makes it a useful engine comparison. Cloud scheduling, analysis, browser testing and other platform features are outside this benchmark. k6 also provides scripting capabilities beyond SwarmGo's fixed method/body/header requests.
