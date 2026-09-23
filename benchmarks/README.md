# HTTP load-generation benchmarks

| Workload | SwarmGo | k6 v2.3.0 | Results |
| --- | --- | --- | --- |
| 20,000 concurrency, no per-container CPU/memory limits | **71,548 req/s**, 5/5 runs completed | 44,478 req/s, 5/5 completed | [Capacity test](capacity/) |
| 10,000 concurrency, 2 CPU / 2 GiB generator | **47,129 req/s**, 5/5 runs completed | Memory limit reached in 5/5 runs | [Resource-budget test](concurrency/) |

Both workloads send 1 KiB JSON POSTs to a local HTTP/1.1 server that waits 200 ms before replying. The capacity test uses 3,000,000 requests per run; the smaller-budget test uses 1,000,000. Reported rates are target-side medians, and every completed run has zero failed requests. Each report includes every trial, CPU/memory settings, source revisions and reproduction scripts.

The tests ran on an Apple M4 in a 10-CPU, 7.65 GiB ARM64 Docker VM. The target is also local. These are defined workload measurements, not a claim to outperform every load-testing tool or reproduce all features of a hosted service.

## A faster raw-HTTP comparator

[wrk screening](wrk/) reached 93,738 completed requests/s with zero reported errors at 20,000 connections and a 200 ms target delay. This is above SwarmGo’s observed result. Its fixed-duration timing differs from the fixed-count comparison above; the report gives the raw data and remaining gap. **SwarmGo has not demonstrated a throughput advantage over wrk.**
