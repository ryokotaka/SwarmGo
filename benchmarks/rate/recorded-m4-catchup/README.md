# Apple M4 check with catch-up

`fb19876` adds a catch-up mode to SwarmGo's constant-rate engine (`swarmgo resilience -catch-up`, `--catch-up` in the harness). By default a request that comes due while every connection is busy is missed; with catch-up it starts as soon as a connection frees, provided that is within `-max-start-delay` (50 ms), and only later requests are missed as late. wrk2 also sends late requests, without such a limit. This run measures SwarmGo with catch-up under the procedure of [recorded-m4-overhead](../recorded-m4-overhead/), with wrk2 at 600k before and after as a baseline. Method and verdict: [benchmarks/rate](../README.md).

## Result

| | [recorded-m4-overhead](../recorded-m4-overhead/) | This run |
| --- | ---: | ---: |
| SwarmGo build | `7105a9b` | `fb19876` |
| Catch-up | off | **on** |
| External fan | yes | **no** |
| SwarmGo, highest confirmed rate | 450k | **500k** |
| SwarmGo generator µs/request at 500k | 6.62 (did not hold) | 6.51–6.57 |
| wrk2 at 600k, before / after | held / held | held / held |

SwarmGo held 200k–500k on the ladder; 600k (93.86%) and 700k (79.88%) did not, which ended it. 500k then held twice more, confirming it. 550k held twice (99.95%, 99.995%) and missed on the third run (99.86%), so 550k is not confirmed. Under the same harness settings, the stopped comparison confirmed oha at 450k and wrk2 at 600k, and wrk2 held 600k in both baseline runs here.

The two SwarmGo records differ in both catch-up and cooling, so the step from 450k to 500k is not attributed to catch-up alone. The mechanism is visible in the counters below: with catch-up there were no busy misses at any rate.

## Observations

- **No busy misses.** Every SwarmGo run reported 0 busy misses. At 200k–400k no request was missed at all and SwarmGo exited with 0; from 500k up, the misses were late misses (requests that could not start within 50 ms of their due time).
- **The limit is now generator CPU.** At 550k SwarmGo used 3.68–3.69 of its four generator CPUs and at 600k 3.69, with the 99th-percentile start delay at the 50 ms limit. Its generator CPU per request was 6.5–6.7 µs from 500k up, against 6.24–6.27 µs for wrk2 at 600k.
- **Late starts are recorded.** With catch-up the 99th-percentile start delay was about 2 ms up to 400k, 8 ms at the first 500k run, 21–25 ms at the confirmation runs, and 36–50 ms at 550k.
- **SwarmGo exits with 2** ("inconclusive") whenever at least one planned request was missed. The verdict uses only the target's counter.
- **Diagnostics** at 600k, the first rate that did not hold, are kept but not counted: `cpu.pprof` (30 KB) and `exec.trace` (22 MB). No file exceeded 50 MB, so nothing was left out.
- **No wall-clock steps:** the largest difference between the target's wall and monotonic clocks in any sample was 9.5 ms.
- An earlier start of this run was stopped after the ladder because the external fan used in the earlier records was not attached; it was restarted from the beginning and its runs are not included.

## Conditions

- MacBook Air (Mac16,13), Apple M4, 24 GB memory, on AC power with Low Power Mode off, **without an external fan** (the three earlier M4 records used one). macOS 27.0.
- Docker Desktop 4.60.1, Engine 29.2.0, Linux ARM64 VM with 10 CPUs and approximately 7.65 GiB RAM.
- Harness defaults: generator on CPUs 0–3, target on CPUs 4–9, 1,024 requests in flight, five seconds of warmup after the first request, then 60 measured seconds, 0.1% tolerance. Generator memory 6 GiB, target 512 MiB. SwarmGo runs used `--catch-up` with the default 50 ms `-max-start-delay`.
- SwarmGo built with PGO at `fb19876` by the throughput preparation. wrk2 `44a94c1`, image built by `--prepare` at the same checkout.
- Runs were sequential, in this order: A (wrk2 600k), B (SwarmGo ladder from 200k, stopping after two consecutive rates that did not hold), C (confirmation of the highest held rate, then 50k above it), D (diagnostics), E (wrk2 600k). Record numbers 13 and 14 were used by the diagnostic runs, which are named `k-diag-*`.

## Every run

Ratio is delivered ÷ requested; "lowest 5 s" is the lowest five-second interval relative to the requested rate. CPU figures use the first and last samples of the window, with elapsed time from the target's monotonic clock. Busy missed, late missed and start delay come from SwarmGo's own report (`native.json`, `load`).

| # | Step | Record | Requested | Delivered | Ratio | Lowest 5 s | held | Exit | Gen µs/req | Gen cores | Target cores | Busy missed | Late missed | Start delay p99 |
| ---: | :---: | --- | ---: | ---: | ---: | ---: | :---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | A | [k-01-600k-wrk2](runs/k-01-600k-wrk2/results.json) | 600k | 599.9k | 0.9999 | 0.998 | ✓ | 0 | 6.27 | 3.76 | 5.22 | – | – | – |
| 2 | B | [k-02-200k-swarmgo](runs/k-02-200k-swarmgo/results.json) | 200k | 200.0k | 1.0000 | 1.000 | ✓ | 0 | 7.88 | 1.58 | 2.14 | 0 | 0 | 2.3 ms |
| 3 | B | [k-03-300k-swarmgo](runs/k-03-300k-swarmgo/results.json) | 300k | 300.0k | 1.0000 | 1.000 | ✓ | 0 | 7.15 | 2.15 | 2.83 | 0 | 0 | 2.1 ms |
| 4 | B | [k-04-400k-swarmgo](runs/k-04-400k-swarmgo/results.json) | 400k | 400.0k | 1.0000 | 1.000 | ✓ | 0 | 6.81 | 2.72 | 3.81 | 0 | 0 | 1.9 ms |
| 5 | B | [k-05-500k-swarmgo](runs/k-05-500k-swarmgo/results.json) | 500k | 500.0k | 1.0000 | 0.999 | ✓ | 0 | 6.57 | 3.28 | 4.66 | 0 | 0 | 8.0 ms |
| 6 | B | [k-06-600k-swarmgo](runs/k-06-600k-swarmgo/results.json) | 600k | 563.2k | 0.9386 | 0.917 | ✗ | 2 | 6.57 | 3.69 | 5.37 | 0 | 2,552,899 | 50.0 ms |
| 7 | B | [k-07-700k-swarmgo](runs/k-07-700k-swarmgo/results.json) | 700k | 559.2k | 0.7988 | 0.781 | ✗ | 2 | 6.47 | 3.61 | 5.32 | 0 | 9,813,973 | 50.0 ms |
| 8 | C | [k-08-500k-swarmgo](runs/k-08-500k-swarmgo/results.json) | 500k | 500.0k | 1.0000 | 0.999 | ✓ | 2 | 6.51 | 3.25 | 4.63 | 0 | 12,593 | 25.5 ms |
| 9 | C | [k-09-500k-swarmgo](runs/k-09-500k-swarmgo/results.json) | 500k | 500.0k | 1.0000 | 1.000 | ✓ | 2 | 6.54 | 3.27 | 4.64 | 0 | 453 | 20.7 ms |
| 10 | C | [k-10-550k-swarmgo](runs/k-10-550k-swarmgo/results.json) | 550k | 549.7k | 0.9995 | 0.994 | ✓ | 2 | 6.69 | 3.68 | 5.13 | 0 | 15,626 | 49.3 ms |
| 11 | C | [k-11-550k-swarmgo](runs/k-11-550k-swarmgo/results.json) | 550k | 550.0k | 0.9999 | 0.999 | ✓ | 2 | 6.71 | 3.69 | 5.13 | 0 | 3,914 | 36.5 ms |
| 12 | C | [k-12-550k-swarmgo](runs/k-12-550k-swarmgo/results.json) | 550k | 549.2k | 0.9986 | 0.967 | ✗ | 2 | 6.71 | 3.68 | 5.14 | 0 | 29,722 | 49.5 ms |
| 13 | D | [k-diag-600k-cpu](runs/k-diag-600k-cpu/results.json) | 600k | 568.1k | 0.9469 | 0.912 | ✗ (diag.) | 2 | 6.50 | 3.68 | 5.39 | 0 | 2,219,249 | 50.0 ms |
| 14 | D | [k-diag-600k-trace](runs/k-diag-600k-trace/results.json) | 600k | 567.3k | 0.9455 | 0.920 | ✗ (diag.) | 2 | 6.51 | 3.68 | 5.36 | 0 | 2,244,171 | 50.0 ms |
| 15 | E | [k-15-600k-wrk2](runs/k-15-600k-wrk2/results.json) | 600k | 600.0k | 1.0000 | 0.997 | ✓ | 0 | 6.24 | 3.75 | 5.17 | – | – | – |
