# Current-engine check

SwarmGo `607ff795fd8f05436e5875656a5e8c3b406c6bdc` completed the same fixed-budget workload after the HTTP engine changes: **1,000,000 successful POSTs, zero failures, and 10,000 simultaneous requests observed at the target**.

| Metric | This run |
| --- | ---: |
| Controller + worker limit | 2 CPU / 2 GiB |
| Target throughput | 48,022 requests/s |
| Peak generator cgroup memory | 244.6 MiB |
| Process duration | 21.06 s |
| Invalid requests at target | 0 |

This is **one verification run**, not a new paired comparison or a five-run median. The [five-pair k6 comparison](../) remains the comparison evidence. Conditions match that workload: HTTP/1.1, one million 1 KiB POSTs, concurrency 10,000, 200 ms target delay, and separate target CPUs 2–5 on the same Apple M4 Docker VM.

[Recorded counters, hashes and build metadata](results.json) · [Native report](post-1-swarmgo.json)

Measured on 2026-09-23 with the unchanged [original harness](../bench-executed.py), using `--requests 1000000 --repeats 1 --tools swarmgo --swarmgo-binary swarmgo-resilience --out current-budget-check`. The binary name refers to the complete SwarmGo CLI built from the revision above; this run uses the fixed-count `run` command. Machine-local build paths and Docker identity fields are omitted from the published metadata.
