# Apple M4 re-measurement

HTTP POST throughput of SwarmGo after the response-header fast path (`471e471`), measured against the SwarmGo commit before it, wrk, oha and k6 in one session on one machine. Six 60-second observations per variant.

- **new**: `claude/nice-albattani-rglt0t` at `1401778`
- **old**: `03a1bfd`, the commit before the fast path

| Variant | Median POST/s | Minimum | Maximum | Generator CPU µs/request (median) | Runs |
| --- | ---: | ---: | ---: | ---: | ---: |
| SwarmGo new | **640,056** | 606,644 | 655,406 | 6.72 | 6 |
| SwarmGo old | **628,076** | 592,450 | 638,149 | 7.04 | 6 |
| wrk | **682,887** | 641,842 | 693,471 | 6.67 | 6 |
| oha | **591,419** | 535,373 | 596,282 | 9.03 | 6 |
| k6 | **158,565** | 140,268 | 163,149 | 34.49 | 6 |
| SwarmGo new (runs 19–36) | **651,729** | 602,936 | 657,301 | 6.63 | 6 |

The first three rows come from runs 1–18, where new, old and wrk were rotated. oha and k6 come from runs 19–36, which also include six more SwarmGo new runs as a reference for that part of the session. All 36 observations are complete and valid; none were excluded. Aggregates are in [`summary.json`](summary.json).

## Conditions

- MacBook Air (Mac16,13), Apple M4 (4 performance and 6 efficiency cores), 24 GB memory, on AC power with Low Power Mode off. macOS 27.0 (26A428).
- Docker Desktop 4.60.1, Engine 29.2.0, Linux ARM64 VM with 10 CPUs and approximately 7.65 GiB RAM (kernel 6.12.67-linuxkit). Generator and target share the host.
- Workload as in the [throughput comparison](../): HTTP/1.1, 1 KiB POST body and response, no target delay or rate cap, internal Docker network, no published ports.
- Five seconds of warmup, then 60 seconds of measurement. Fresh containers for each run. Runs are sequential.
- Concurrency 256 for SwarmGo, wrk and oha; 64 VUs for k6, the settings selected in the earlier screening.
- new and wrk ran from the new worktree; old ran from a separate worktree at `03a1bfd`. Both SwarmGo builds used Go 1.25.7 and the same target binary. Tool binaries and the wrk image (`swarmgo-wrk-local:4.2.0`) are the pinned versions recorded in each run's manifest.
- The Mac is fanless. Temperature and background activity were not controlled beyond leaving the machine idle.

## Run order

Runs 1–18 rotate new, old and wrk in blocks of three so that session drift falls on each variant equally:

| Block | Runs | Order |
| ---: | --- | --- |
| 1 | 1–3 | new, old, wrk |
| 2 | 4–6 | old, wrk, new |
| 3 | 7–9 | wrk, new, old |
| 4 | 10–12 | new, old, wrk |
| 5 | 13–15 | old, wrk, new |
| 6 | 16–18 | wrk, new, old |

Runs 19–36 rotate oha, k6 and new the same way: oha, k6, new / k6, new, oha / new, oha, k6, twice.

Throughput rose during runs 1–18 for all three variants (wrk 648k to 692k, new 630k to 654k, old 608k to 638k). Runs 32–36 were lower for every tool that ran in them (new 603k–606k against 651k–657k for runs 21–30; oha 535k–538k against 591k–596k; k6 140k). The reason was not recorded. These runs are included.

## Measurement

Throughput is `results[0].target_rps`: target-validated POST counter delta divided by the target's snapshot-clock delta over the 60-second window. A run counts only when `completed_observation` and `target_valid` are both true.

Generator CPU per request is the generator container's `usage_usec` delta divided by the target's request delta, between the first and last samples of the window (about 5 s to 60 s). The window's starting snapshot has no generator counter, so the first five seconds are not included.

Exit codes: SwarmGo is deadline-stopped after the observation and exits 1 by design. oha exited 137 in all six runs after reaching its 6 GiB memory limit after the window; peak memory during the window was below the limit. As in the [repeated series](../repeated/), those 60-second rates are included and the native records are retained.

## Every run

| Run | Variant | POST/s | CPU µs/request | Exit | Record |
| ---: | --- | ---: | ---: | ---: | --- |
| 1 | new | 630,301 | 6.79 | 1 | [JSON](runs/01-new/results.json) |
| 2 | old | 608,085 | 7.20 | 1 | [JSON](runs/02-old/results.json) |
| 3 | wrk | 648,469 | 6.82 | 0 | [JSON](runs/03-wrk/results.json) |
| 4 | old | 625,052 | 7.06 | 1 | [JSON](runs/04-old/results.json) |
| 5 | wrk | 674,149 | 6.74 | 0 | [JSON](runs/05-wrk/results.json) |
| 6 | new | 606,644 | 7.00 | 1 | [JSON](runs/06-new/results.json) |
| 7 | wrk | 641,842 | 6.97 | 0 | [JSON](runs/07-wrk/results.json) |
| 8 | new | 618,326 | 6.93 | 1 | [JSON](runs/08-new/results.json) |
| 9 | old | 592,450 | 7.28 | 1 | [JSON](runs/09-old/results.json) |
| 10 | new | 649,811 | 6.65 | 1 | [JSON](runs/10-new/results.json) |
| 11 | old | 632,494 | 7.03 | 1 | [JSON](runs/11-old/results.json) |
| 12 | wrk | 693,471 | 6.60 | 0 | [JSON](runs/12-wrk/results.json) |
| 13 | old | 631,100 | 6.97 | 1 | [JSON](runs/13-old/results.json) |
| 14 | wrk | 691,751 | 6.55 | 0 | [JSON](runs/14-wrk/results.json) |
| 15 | new | 655,406 | 6.60 | 1 | [JSON](runs/15-new/results.json) |
| 16 | wrk | 691,625 | 6.58 | 0 | [JSON](runs/16-wrk/results.json) |
| 17 | new | 653,702 | 6.60 | 1 | [JSON](runs/17-new/results.json) |
| 18 | old | 638,149 | 6.96 | 1 | [JSON](runs/18-old/results.json) |
| 19 | oha | 591,078 | 9.07 | 137 | [JSON](runs/19-oha/results.json) |
| 20 | k6 | 157,273 | 34.17 | 0 | [JSON](runs/20-k6/results.json) |
| 21 | new | 657,301 | 6.59 | 1 | [JSON](runs/21-new/results.json) |
| 22 | k6 | 159,858 | 34.29 | 0 | [JSON](runs/22-k6/results.json) |
| 23 | new | 655,014 | 6.60 | 1 | [JSON](runs/23-new/results.json) |
| 24 | oha | 596,282 | 8.97 | 137 | [JSON](runs/24-oha/results.json) |
| 25 | new | 650,931 | 6.62 | 1 | [JSON](runs/25-new/results.json) |
| 26 | oha | 591,761 | 8.96 | 137 | [JSON](runs/26-oha/results.json) |
| 27 | k6 | 161,824 | 34.67 | 0 | [JSON](runs/27-k6/results.json) |
| 28 | oha | 593,860 | 8.99 | 137 | [JSON](runs/28-oha/results.json) |
| 29 | k6 | 163,149 | 34.31 | 0 | [JSON](runs/29-k6/results.json) |
| 30 | new | 652,527 | 6.64 | 1 | [JSON](runs/30-new/results.json) |
| 31 | k6 | 152,832 | 34.85 | 0 | [JSON](runs/31-k6/results.json) |
| 32 | new | 605,529 | 7.06 | 1 | [JSON](runs/32-new/results.json) |
| 33 | oha | 538,401 | 9.74 | 137 | [JSON](runs/33-oha/results.json) |
| 34 | new | 602,936 | 7.16 | 1 | [JSON](runs/34-new/results.json) |
| 35 | oha | 535,373 | 9.74 | 137 | [JSON](runs/35-oha/results.json) |
| 36 | k6 | 140,268 | 36.83 | 0 | [JSON](runs/36-k6/results.json) |

## Incomplete runs

None. Every run reached the full 60-second window with zero invalid requests and valid final target counters.

An earlier session on the same day was stopped after run 10 and is not part of the summary. Its records are kept in [`attempt1/`](attempt1/); all ten runs there were also complete and valid:

| Run | Variant | POST/s | CPU µs/request |
| ---: | --- | ---: | ---: |
| 1 | new | 605,973 | 6.95 |
| 2 | old | 577,059 | 7.53 |
| 3 | wrk | 620,287 | 7.17 |
| 4 | old | 587,322 | 7.44 |
| 5 | wrk | 617,760 | 7.15 |
| 6 | new | 552,056 | 7.67 |
| 7 | wrk | 567,363 | 7.68 |
| 8 | new | 518,356 | 8.16 |
| 9 | old | 493,201 | 8.75 |
| 10 | new | 516,427 | 8.21 |

## Microbenchmark

`go test ./internal/worker -run '^$' -bench DirectRequest -benchmem -count 10` on the macOS host, in each worktree. `03a1bfd` has no `internal/worker/direct_bench_test.go`, so the file from new was copied in for the run and not committed there. Raw output is in [`micro/`](micro/).

```
goos: darwin
goarch: arm64
pkg: github.com/ryokotaka/SwarmGo/internal/worker
cpu: Apple M4
                              │ micro-old.txt │            micro-new.txt            │
                              │    sec/op     │   sec/op     vs base                │
DirectRequestContentLength-10     391.4n ± 1%   173.6n ± 0%  -55.64% (p=0.000 n=10)
DirectRequestChunked-10           374.3n ± 0%   217.8n ± 1%  -41.81% (p=0.000 n=10)
geomean                           382.7n        194.4n       -49.19%

                              │ micro-old.txt │              micro-new.txt              │
                              │     B/op      │    B/op     vs base                     │
DirectRequestContentLength-10      8.000 ± 0%   0.000 ± 0%  -100.00% (p=0.000 n=10)
DirectRequestChunked-10            64.00 ± 0%   48.00 ± 0%   -25.00% (p=0.000 n=10)
geomean                            22.63                    ?                       ¹ ²
¹ summaries must be >0 to compute geomean
² ratios must be >0 to compute geomean

                              │ micro-old.txt │              micro-new.txt              │
                              │   allocs/op   │ allocs/op   vs base                     │
DirectRequestContentLength-10      1.000 ± 0%   0.000 ± 0%  -100.00% (p=0.000 n=10)
DirectRequestChunked-10            3.000 ± 0%   1.000 ± 0%   -66.67% (p=0.000 n=10)
geomean                            1.732                    ?                       ¹ ²
¹ summaries must be >0 to compute geomean
² ratios must be >0 to compute geomean
```

The micro benchmark isolates the worker's request/response path. End to end, the generator also shares the machine with the target and the Docker VM, so the throughput difference between new and old is much smaller than the per-request time difference here.

## Figures

`python3 benchmarks/throughput/plot_repeated.py --data rerun-m4` renders the README figures `assets/throughput-m4*.svg` from [`summary.json`](summary.json) with matplotlib 3.11.2. They plot SwarmGo new from runs 1–18, wrk from runs 1–18, and oha and k6 from runs 19–36; filled dots are each tool's first three runs.
