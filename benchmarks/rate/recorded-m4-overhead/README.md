# Apple M4 check after cutting the paced engine's overhead

`7105a9b` cuts two costs in SwarmGo's constant-rate engine: short waits sleep instead of resetting a timer and selecting on it, and lanes claim slots at most 250 µs early instead of up to 1 ms. This run measures the result under the same conditions and procedure as [recorded-m4-claim](../recorded-m4-claim/), with wrk2 at 600k before and after as a baseline. Method and verdict: [benchmarks/rate](../README.md).

## Result

| | [recorded-m4-partial](../recorded-m4-partial/) | [recorded-m4-claim](../recorded-m4-claim/) | This run |
| --- | ---: | ---: | ---: |
| SwarmGo build | `e281d6e` | `a9997b6` | `7105a9b` |
| SwarmGo, highest confirmed rate | 100k | 300k | **450k** |
| SwarmGo generator µs/request at 400k | – | 8.55 | **6.81–6.88** |
| wrk2 at 600k, before / after | held (step A) | held / held | held / held |

SwarmGo held 200k, 300k and 400k on the ladder; 500k (99.55%) and 600k (94.41%) did not hold, which ended it. 400k then held twice more, confirming it, and 450k held in all three runs, confirming 450k. For comparison, the stopped comparison confirmed oha at 450k and wrk2 at 600k under the same conditions.

## Observations

- **Generator CPU per request fell 17–20%** against `recorded-m4-claim` at the same rates: 300k 8.55 → 7.12 µs, 400k 8.55 → 6.81–6.88 µs. wrk2 at 600k used 6.14 and 6.36 µs.
- **The limit is still busy misses, not CPU.** At 500k SwarmGo used 3.30 of its four generator CPUs; 0.48% of planned requests were busy misses (every in-flight slot taken) and none were late. At 450k busy misses were 0.11–0.16%. The target's response latency rises in brief bursts, and SwarmGo does not start a request more than 1 ms late when every lane is busy; wrk2 instead sends such requests late and catches up.
- **SwarmGo exits with 2** ("inconclusive") whenever at least one planned request was missed; it exited with 0 at 200k, where none were. The verdict uses only the target's counter.
- **Diagnostics** at 500k, the first rate that did not hold, are kept but not counted: `cpu.pprof` (42 KB) and `exec.trace` (28 MB). No file exceeded 50 MB, so nothing was left out.
- **No wall-clock steps:** the largest difference between the target's wall and monotonic clocks in any sample was 1.1 ms.

## Conditions

- MacBook Air (Mac16,13), Apple M4, 24 GB memory, on AC power with Low Power Mode off, with an external fan under the machine. macOS 27.0.
- Docker Desktop 4.60.1, Engine 29.2.0, Linux ARM64 VM with 10 CPUs and approximately 7.65 GiB RAM.
- Harness defaults: generator on CPUs 0–3, target on CPUs 4–9, 1,024 requests in flight, five seconds of warmup after the first request, then 60 measured seconds, 0.1% tolerance. Generator memory 6 GiB, target 512 MiB.
- SwarmGo built with PGO at `7105a9b` by the throughput preparation. wrk2 `44a94c1`, image built by `--prepare` at the same checkout.
- Runs were sequential, in this order: A (wrk2 600k), B (SwarmGo ladder from 200k, stopping after two consecutive rates that did not hold), C (confirmation of the highest held rate, then 50k above it), D (diagnostics), E (wrk2 600k). Record numbers 12 and 13 were used by the diagnostic runs, which are named `o-diag-*`.

## Every run

Ratio is delivered ÷ requested; "lowest 5 s" is the lowest five-second interval relative to the requested rate. CPU figures use the first and last samples of the window, with elapsed time from the target's monotonic clock. Started, busy missed and late missed come from SwarmGo's own report (`native.json`, `load`).

| # | Step | Record | Requested | Delivered | Ratio | Lowest 5 s | held | Exit | Gen µs/req | Gen cores | Target cores | Started | Busy missed | Late missed |
| ---: | :---: | --- | ---: | ---: | ---: | ---: | :---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | A | [o-01-600k-wrk2](runs/o-01-600k-wrk2/results.json) | 600k | 600.0k | 1.0000 | 1.000 | ✓ | 0 | 6.14 | 3.69 | 5.08 | – | – | – |
| 2 | B | [o-02-200k-swarmgo](runs/o-02-200k-swarmgo/results.json) | 200k | 200.0k | 1.0000 | 1.000 | ✓ | 0 | 7.81 | 1.56 | 2.14 | 14,000,000 | 0 | 0 |
| 3 | B | [o-03-300k-swarmgo](runs/o-03-300k-swarmgo/results.json) | 300k | 300.0k | 0.9999 | 0.999 | ✓ | 2 | 7.12 | 2.14 | 2.83 | 20,998,027 | 1,973 | 0 |
| 4 | B | [o-04-400k-swarmgo](runs/o-04-400k-swarmgo/results.json) | 400k | 399.8k | 0.9996 | 0.998 | ✓ | 2 | 6.81 | 2.72 | 3.80 | 27,976,605 | 23,395 | 0 |
| 5 | B | [o-05-500k-swarmgo](runs/o-05-500k-swarmgo/results.json) | 500k | 497.8k | 0.9955 | 0.982 | ✗ | 2 | 6.62 | 3.30 | 4.65 | 34,832,339 | 167,661 | 0 |
| 6 | B | [o-06-600k-swarmgo](runs/o-06-600k-swarmgo/results.json) | 600k | 566.4k | 0.9441 | 0.911 | ✗ | 2 | 6.57 | 3.71 | 5.40 | 39,670,460 | 2,323,202 | 6,338 |
| 7 | C | [o-07-400k-swarmgo](runs/o-07-400k-swarmgo/results.json) | 400k | 400.0k | 0.9999 | 0.999 | ✓ | 2 | 6.88 | 2.75 | 3.84 | 27,979,455 | 20,545 | 0 |
| 8 | C | [o-08-400k-swarmgo](runs/o-08-400k-swarmgo/results.json) | 400k | 399.9k | 0.9997 | 0.998 | ✓ | 2 | 6.85 | 2.74 | 3.82 | 27,973,631 | 26,369 | 0 |
| 9 | C | [o-09-450k-swarmgo](runs/o-09-450k-swarmgo/results.json) | 450k | 449.7k | 0.9993 | 0.997 | ✓ | 2 | 6.75 | 3.03 | 4.42 | 31,447,841 | 50,786 | 1,373 |
| 10 | C | [o-10-450k-swarmgo](runs/o-10-450k-swarmgo/results.json) | 450k | 449.8k | 0.9996 | 0.999 | ✓ | 2 | 6.75 | 3.04 | 4.41 | 31,461,863 | 34,641 | 3,496 |
| 11 | C | [o-11-450k-swarmgo](runs/o-11-450k-swarmgo/results.json) | 450k | 449.7k | 0.9993 | 0.998 | ✓ | 2 | 6.72 | 3.02 | 4.40 | 31,465,768 | 34,232 | 0 |
| 12 | D | [o-diag-500k-cpu](runs/o-diag-500k-cpu/results.json) | 500k | 498.2k | 0.9963 | 0.992 | ✗ (diag.) | 2 | 6.73 | 3.35 | 4.69 | 34,847,479 | 152,521 | 0 |
| 13 | D | [o-diag-500k-trace](runs/o-diag-500k-trace/results.json) | 500k | 498.1k | 0.9962 | 0.988 | ✗ (diag.) | 2 | 6.70 | 3.33 | 4.67 | 34,839,432 | 149,894 | 10,674 |
| 14 | E | [o-14-600k-wrk2](runs/o-14-600k-wrk2/results.json) | 600k | 600.0k | 1.0000 | 0.997 | ✓ | 0 | 6.36 | 3.81 | 5.30 | – | – | – |
