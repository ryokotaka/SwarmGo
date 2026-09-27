# Apple M4 check of the rebuilt constant-rate engine

SwarmGo's constant-rate engine was rebuilt in `a9997b6` (paced lanes claim their own slots), with the recording adjustments in `310fbf5`. This run measures how far it holds a requested rate on Apple M4 under the same conditions as the [stopped comparison](../recorded-m4-partial/), with wrk2 at 600k before and after as a baseline. Method and verdict: [benchmarks/rate](../README.md).

## Result

| | Previous engine ([recorded-m4-partial](../recorded-m4-partial/)) | Rebuilt engine |
| --- | ---: | ---: |
| SwarmGo, highest confirmed rate | 100k | **300k** |
| wrk2 at 600k, before | – | held (100.00%) |
| wrk2 at 600k, after | – | held (100.00%) |

SwarmGo held 200k and 300k on the ladder, then 300k twice more, confirming it. 350k held twice and missed on the third run (99.86%), so 350k is not confirmed. 400k (99.86%) and 500k (99.48%) did not hold, which ended the ladder. wrk2 held 600k in both baseline runs, so conditions did not drift across the session.

## Observations

- **SwarmGo's misses are almost all busy misses.** Across the counted runs, requests SwarmGo could not start were `busy_missed` (every in-flight slot taken): 0.18–0.31% at 350k, 0.22% at 400k and 0.70% at 500k of planned requests. `late_missed` was 0 in every run up to 400k and 4,187 at 500k. The shortfall comes from the 1,024 in-flight ceiling filling, not from the schedule falling behind.
- **CPU:** SwarmGo used 1.70 generator cores at 200k, 2.57 at 300k, 3.42 at 400k and 3.80 of 4 at 500k. The previous engine used about 2 cores at 300k and stopped near 290k. SwarmGo's generator CPU per request was 7.6–8.9 µs, against 6.2–6.4 µs for wrk2 at 600k.
- **SwarmGo exits with 2** ("inconclusive") in every run, because at least one planned request was missed each time, including runs that held. The verdict uses only the target's counter.
- **Diagnostics** at 400k, the first rate that did not hold, are kept but not counted: a CPU profile (`cpu.pprof`, 45 KB) and an execution trace (`exec.trace`, 24 MB). Both were within the size limit, so no file was left out. The profiled runs delivered 99.82% and 99.86%, close to the counted 400k run.
- **No wall-clock steps:** the largest difference between the target's wall and monotonic clocks in any sample was 5.2 ms (the trace run); every other run was at most 1.1 ms.

## Conditions

- MacBook Air (Mac16,13), Apple M4, 24 GB memory, on AC power with Low Power Mode off, with an external fan under the machine. macOS 27.0.
- Docker Desktop 4.60.1, Engine 29.2.0, Linux ARM64 VM with 10 CPUs and approximately 7.65 GiB RAM.
- Harness defaults: generator on CPUs 0–3, target on CPUs 4–9, 1,024 requests in flight, five seconds of warmup after the first request, then 60 measured seconds, 0.1% tolerance. Generator memory 6 GiB, target 512 MiB.
- SwarmGo built with PGO at `9dd8960`, which contains `a9997b6` and `310fbf5`. wrk2 `44a94c1`, image built by `--prepare` at the same checkout.
- Runs were sequential, in this order: A (wrk2 600k), B (SwarmGo ladder from 200k, stopping after two consecutive rates that did not hold), C (confirmation), D (diagnostics), E (wrk2 600k). Record numbers 11 and 12 were used by the diagnostic runs, which are named `c-diag-*`.

## Every run

Ratio is delivered ÷ requested; "lowest 5 s" is the lowest five-second interval relative to the requested rate. CPU figures use the first and last samples of the window, with elapsed time from the target's monotonic clock. Started, busy missed and late missed come from SwarmGo's own report (`native.json`, `load`).

| # | Step | Record | Requested | Delivered | Ratio | Lowest 5 s | held | Exit | Gen µs/req | Gen cores | Target cores | Started | Busy missed | Late missed |
| ---: | :---: | --- | ---: | ---: | ---: | ---: | :---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | A | [c-01-600k-wrk2](runs/c-01-600k-wrk2/results.json) | 600k | 600.0k | 1.0000 | 0.999 | ✓ | 0 | 6.20 | 3.72 | 5.15 | – | – | – |
| 2 | B | [c-02-200k-swarmgo](runs/c-02-200k-swarmgo/results.json) | 200k | 200.0k | 1.0000 | 1.000 | ✓ | 2 | 8.51 | 1.70 | 2.11 | 13,997,533 | 2,467 | 0 |
| 3 | B | [c-03-300k-swarmgo](runs/c-03-300k-swarmgo/results.json) | 300k | 299.9k | 0.9998 | 0.999 | ✓ | 2 | 8.55 | 2.56 | 3.07 | 20,961,951 | 38,049 | 0 |
| 4 | B | [c-04-400k-swarmgo](runs/c-04-400k-swarmgo/results.json) | 400k | 399.5k | 0.9986 | 0.997 | ✗ | 2 | 8.55 | 3.42 | 4.34 | 27,937,881 | 62,119 | 0 |
| 5 | B | [c-05-500k-swarmgo](runs/c-05-500k-swarmgo/results.json) | 500k | 497.4k | 0.9947 | 0.981 | ✗ | 2 | 7.64 | 3.80 | 4.76 | 34,752,019 | 243,794 | 4,187 |
| 6 | C | [c-06-300k-swarmgo](runs/c-06-300k-swarmgo/results.json) | 300k | 299.9k | 0.9998 | 1.000 | ✓ | 2 | 8.57 | 2.57 | 3.06 | 20,969,212 | 30,788 | 0 |
| 7 | C | [c-07-300k-swarmgo](runs/c-07-300k-swarmgo/results.json) | 300k | 299.9k | 0.9996 | 0.998 | ✓ | 2 | 8.58 | 2.57 | 3.07 | 20,980,919 | 19,081 | 0 |
| 8 | C | [c-08-350k-swarmgo](runs/c-08-350k-swarmgo/results.json) | 350k | 349.7k | 0.9991 | 0.999 | ✓ | 2 | 8.83 | 3.09 | 3.67 | 24,442,625 | 57,375 | 0 |
| 9 | C | [c-09-350k-swarmgo](runs/c-09-350k-swarmgo/results.json) | 350k | 349.7k | 0.9992 | 0.998 | ✓ | 2 | 8.93 | 3.12 | 3.67 | 24,457,092 | 42,908 | 0 |
| 10 | C | [c-10-350k-swarmgo](runs/c-10-350k-swarmgo/results.json) | 350k | 349.5k | 0.9986 | 0.995 | ✗ | 2 | 8.72 | 3.05 | 3.69 | 24,423,712 | 76,288 | 0 |
| 11 | D | [c-diag-400k-cpu](runs/c-diag-400k-cpu/results.json) | 400k | 399.3k | 0.9982 | 0.997 | ✗ (diag.) | 2 | 8.64 | 3.45 | 4.37 | 27,923,649 | 76,351 | 0 |
| 12 | D | [c-diag-400k-trace](runs/c-diag-400k-trace/results.json) | 400k | 399.4k | 0.9986 | 0.998 | ✗ (diag.) | 2 | 8.81 | 3.52 | 4.37 | 27,908,498 | 91,502 | 0 |
| 13 | E | [c-13-600k-wrk2](runs/c-13-600k-wrk2/results.json) | 600k | 600.0k | 1.0000 | 0.998 | ✓ | 0 | 6.40 | 3.84 | 5.29 | – | – | – |
