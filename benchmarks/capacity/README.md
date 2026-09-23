# 20,000 concurrent requests, without per-container resource limits

SwarmGo completed **three million 1 KiB POSTs with zero failures in all five runs**, reaching **20,000 simultaneous requests** at the target on every run. Its median throughput was **71,548 requests/s**, compared with **44,478 for k6 v2.3.0**: **1.61×** in this workload.

Both tools completed every run. Their median peak generator memory was **1,434 MiB** for SwarmGo and **5,924 MiB** for k6. SwarmGo's controller and worker are both included.

## Conditions

- Apple M4, ARM64 Linux Docker VM with **10 CPUs and 7.65 GiB RAM**. Containers have **no CPU quota, CPU pinning or memory limit**; the generator and target share the VM's available resources.
- Each run sends **3,000,000 HTTP/1.1 POST requests**, with keep-alive, **1 KiB JSON request and response bodies**, and concurrency **20,000**.
- The target waits **200 ms** before replying. With 20,000 slots, the theoretical ceiling before overhead is 100,000 requests/s. This is high-concurrency capacity under a defined response delay, not maximum RPS for every API.
- The target validates every method, protocol and request body. Both tools drain response bodies. k6 uses `shared-iterations` with 20,000 VUs and `discardResponseBodies`.
- SwarmGo [`468ba43`](https://github.com/ryokotaka/SwarmGo/commit/468ba43cb8151e2916f8333faec4556a3a7d78e5), built with Go 1.25.7; official k6 v2.3.0 Linux ARM64 binary built with Go 1.26.8. Default runtime memory settings for both tools.
- Five repetitions, alternating order, fresh generator container per run, no warm-up or inter-run cooling period. Measurement date: 2026-09-23.
- The target and generator are connected by an **internal Docker network with no published ports**. No load leaves this computer; no cloud service is used.

## All runs

Every row completed 3,000,000 requests. Requests/s uses target handler start through final response write. Process time also includes startup and final reporting. Memory is the cgroup's `memory.peak`, not RSS. Peak simultaneous requests is measured on the target.

| Run | Tool | Requests/s | Process seconds | Peak cgroup MiB | Peak simultaneous | Failed requests |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| 1 | swarmgo | 88,147 | 34.82 | 1,391.0 | 20,000 | 0 |
| 1 | k6 | 51,965 | 60.08 | 5,899.3 | 20,000 | 0 |
| 2 | k6 | 46,495 | 66.94 | 5,906.7 | 20,000 | 0 |
| 2 | swarmgo | 72,535 | 42.20 | 1,437.1 | 20,000 | 0 |
| 3 | swarmgo | 71,548 | 42.73 | 1,433.7 | 20,000 | 0 |
| 3 | k6 | 44,478 | 70.24 | 6,066.1 | 19,172 | 0 |
| 4 | k6 | 43,998 | 70.87 | 5,923.6 | 18,359 | 0 |
| 4 | swarmgo | 68,658 | 44.53 | 1,430.6 | 20,000 | 0 |
| 5 | swarmgo | 67,125 | 45.48 | 1,507.0 | 20,000 | 0 |
| 5 | k6 | 42,279 | 73.57 | 5,978.5 | 18,697 | 0 |

Both tools slowed during the sequence. The first run is not used as a headline; the reported ratio uses all five runs' medians. The cause of that drift was not isolated. Full native reports, target counters, CPU use, memory events and build hashes are in [results.json](results.json).

These results measure the whole local test setup. Since the target shares the Docker VM with the generators, they do not establish an isolated generator ceiling or the capacity of a remote production API. The load model is closed-loop: each completed response frees a slot for the next request. A configured constant arrival rate is not tested.

## Reproduce

Requires Python 3, Git, Go **1.25.7** and ARM64 Docker with at least 6 CPUs; use 10 CPUs and approximately 8 GiB VM RAM to match this host. The test can consume most of the VM's available resources.

```bash
cd benchmarks/capacity
python3 prepare.py
python3 bench.py --out measured
```

The setup downloads the measured SwarmGo commit and the free k6 CLI, verifies the official k6 archive checksum and recorded binary hash, and builds the target locally. No account or subscription is needed.

For a short setup check:

```bash
python3 bench.py --requests 10000 --concurrency 100 --delay-ms 0 --repeats 1 --out smoke
```

The script refuses to overwrite an existing output directory and removes only the containers and network it created. Raw output goes into `results/measured/`. Failed trials are retained and cause exit status 1.

[bench-executed.py](bench-executed.py) is the measurement script. The packaged `bench.py` adds portable paths, metadata filtering and argument checks; its default workload and resource settings match the experiment. The executed script used the binary name `swarmgo-bounded` and a sibling SwarmGo checkout.

## Why this improved

An earlier worker retained only 100 idle connections. Retaining a complete concurrency-sized pool fixed the 10,000-request case. At concurrency 20,000, unfinished connection attempts could still create surplus connections after a request had already reused another one. Limiting total connections per host to the same concurrency prevented that excess.

An exploratory run before the total-connection limit completed 3,000,000 requests at 46,316 requests/s, opening 28,231 TCP connections and reaching a peak of 18,361 simultaneous requests. The corrected worker's exploratory run reached 20,000, opened 20,001 connections including the control probe, and completed at 85,543 requests/s. The table above is the subsequent five-run confirmation. The unit tests cover reuse across waves and completion when requests queue behind the connection limit.

## Ten-million-request check

The same measured SwarmGo build also completed **10,000,000 POSTs with zero failures**, reaching 20,000 simultaneous target handlers. Target-side throughput was **85,610 requests/s** over **116.81 seconds**; peak generator cgroup memory was **1.58 GiB**. This is one separate continuity check, not part of the five-run median or a long-duration stability claim. [Raw result](ten-million.json)

```bash
python3 bench.py --tools swarmgo --requests 10000000 --repeats 1 --out ten-million
```

## Scope of the comparison

The [earlier 2 CPU / 2 GiB test](../concurrency/) measures performance within a small resource budget. This test removes those per-container limits. k6 can complete the larger workload with more available memory.

[Grafana Cloud k6 uses the k6 OSS engine](https://grafana.com/docs/grafana-cloud/observe-and-act/testing/k6/introduction/). These results compare HTTP load generation with that engine. They do not establish superiority over all open-source tools or over the managed cloud platform's distributed infrastructure, scripting and analysis features.
