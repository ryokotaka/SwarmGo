# Apple M4 constant-rate comparison (stopped)

The first full constant-rate run on Apple M4 with the method in [benchmarks/rate](../README.md). It was stopped by the user during step B (confirmation), before every tool's top rate was confirmed, so this is a partial record. All 37 completed runs are included; one interrupted run is kept without a verdict.

Source: `claude/nice-albattani-rglt0t` at `e281d6e`, before the constant-rate engine was rebuilt in `a9997b6`.

## Highest confirmed rate

A rate is confirmed when three runs at it all held.

| Tool | Confirmed | When stopped |
| --- | ---: | --- |
| wrk2 | 600k | 650k held 2 of 3 |
| oha | **450k** | finished: 450k held 3 of 3 |
| SwarmGo | 100k | 150k held 2 of 3; the third run was interrupted |
| vegeta | **100k** | finished: 150k did not hold (95.5%) |
| k6 | none | did not hold 100k (90.7%) or 200k (46.6%) |

## Ladder (step A, one run per rate)

| Rate | SwarmGo | wrk2 | vegeta | k6 | oha |
| ---: | :---: | :---: | :---: | :---: | :---: |
| 100k | ✓ | ✓ | ✓ | ✗ 90.7% | ✓ |
| 200k | ✗ 99.85% | ✓ | ✗ 71.3% | ✗ 46.6% | ✓ |
| 300k | ✗ 96.4% | ✓ | ✗ 47.0% | – | ✓ |
| 400k | – | ✓ | – | – | ✓ |
| 500k | – | ✓ | – | – | ✗ 94.6% |
| 600k | – | ✓ | – | – | ✗ 78.8% |
| 700k | – | ✗ 95.1% | – | – | – |

A tool stopped after two consecutive rates that did not hold.

## CPU for the same rate

No rate was held by all five tools, because k6 held none. At 100k, the highest rate the other four all held, the generator's CPU per request was:

| Tool | Generator µs/request | Generator cores |
| --- | ---: | ---: |
| wrk2 | 13.50 | 1.35 |
| SwarmGo | 16.67 | 1.67 |
| oha | 17.69 | 1.77 |
| vegeta | 25.57 | 2.56 |
| k6 (did not hold) | 39.81 | 3.63 |

## Observations

- **SwarmGo was not CPU-bound.** At 200k and 300k it used 1.9–2.1 of its four generator CPUs. At 300k the target received a steady 288–290k/s; SwarmGo's own report shows 774,339 of 21,000,000 planned requests (3.7%) missed. At 200k, 51,920 (0.37%) were missed, leaving it at 99.85%, just below the 99.9% verdict.
- **wrk2 at 700k declined through the window,** from 698k/s in the first five seconds to 620k/s at the end, with the target using 5.66 of its six CPUs.
- **oha exited with 137 in every run from 300k up.** In each case the memory-limit kill came after the measured window; no run was killed inside it.
- **SwarmGo exits with 2** ("inconclusive") whenever any planned request was missed. The verdict uses only the target's counter.
- **No wall-clock steps:** the largest difference between the target's wall and monotonic clocks in any sample was 2.7 ms.
- An earlier start of this comparison, on the build before `e281d6e`, stopped when wrk2 aborted on a negative latency 36 s into its 100k run. That led to the monotonic-clock change in `e281d6e`; the earlier runs are not included. Two later starts were stopped by the user and restarted from the beginning; their runs are not included either.

## Conditions

- MacBook Air (Mac16,13), Apple M4, 24 GB memory, on AC power with Low Power Mode off, with an external fan under the machine. macOS 27.0.
- Docker Desktop 4.60.1, Engine 29.2.0, Linux ARM64 VM with 10 CPUs and approximately 7.65 GiB RAM.
- Generator on CPUs 0–3, target on CPUs 4–9. 1,024 requests in flight for every tool. Five seconds of warmup after the first request, then 60 measured seconds. Generator memory 6 GiB, target 512 MiB.
- Tools: SwarmGo built from `e281d6e` with PGO, wrk2 `44a94c1` (image built by `--prepare` at `e281d6e`), vegeta v12.13.0, k6 v2.3.0, oha v1.16.0.
- Runs were sequential. Step A went up the ladder with the tool order rotated by one per rate; step B interleaved the tools one run at a time.

## Every run

Ratio is delivered ÷ requested; "lowest 5 s" is the lowest five-second interval relative to the requested rate. CPU figures use the first and last samples of the window, with elapsed time from the target's monotonic clock. `b-17-150k-swarmgo` was interrupted by the stop and has no verdict.

| Record | Tool | Requested | Delivered | Ratio | Lowest 5 s | held | Exit | Gen µs/req | Gen cores | Target cores |
| --- | --- | ---: | ---: | ---: | ---: | :---: | ---: | ---: | ---: | ---: |
| [a-100k-k6](runs/a-100k-k6/results.json) | k6 | 100k | 90.7k | 0.9065 | 0.848 | ✗ | 0 | 39.81 | 3.63 | 2.04 |
| [a-100k-oha](runs/a-100k-oha/results.json) | oha | 100k | 100.0k | 1.0000 | 1.000 | ✓ | 0 | 17.69 | 1.77 | 2.19 |
| [a-100k-swarmgo](runs/a-100k-swarmgo/results.json) | swarmgo | 100k | 100.0k | 1.0000 | 0.999 | ✓ | 2 | 16.67 | 1.67 | 2.18 |
| [a-100k-vegeta](runs/a-100k-vegeta/results.json) | vegeta | 100k | 100.0k | 1.0000 | 1.000 | ✓ | 0 | 25.57 | 2.56 | 1.50 |
| [a-100k-wrk2](runs/a-100k-wrk2/results.json) | wrk2 | 100k | 100.0k | 1.0000 | 1.000 | ✓ | 0 | 13.50 | 1.35 | 1.43 |
| [a-200k-k6](runs/a-200k-k6/results.json) | k6 | 200k | 93.1k | 0.4657 | 0.409 | ✗ | 0 | 42.23 | 3.98 | 2.04 |
| [a-200k-oha](runs/a-200k-oha/results.json) | oha | 200k | 200.0k | 1.0001 | 1.000 | ✓ | 0 | 9.68 | 1.94 | 2.58 |
| [a-200k-swarmgo](runs/a-200k-swarmgo/results.json) | swarmgo | 200k | 199.7k | 0.9985 | 0.989 | ✗ | 2 | 9.43 | 1.88 | 2.56 |
| [a-200k-vegeta](runs/a-200k-vegeta/results.json) | vegeta | 200k | 142.6k | 0.7128 | 0.693 | ✗ | 0 | 25.82 | 3.67 | 2.47 |
| [a-200k-wrk2](runs/a-200k-wrk2/results.json) | wrk2 | 200k | 200.0k | 1.0000 | 1.000 | ✓ | 0 | 7.62 | 1.52 | 2.04 |
| [a-300k-oha](runs/a-300k-oha/results.json) | oha | 300k | 300.0k | 1.0000 | 1.000 | ✓ | 137 | 7.92 | 2.37 | 3.36 |
| [a-300k-swarmgo](runs/a-300k-swarmgo/results.json) | swarmgo | 300k | 289.2k | 0.9639 | 0.959 | ✗ | 2 | 7.24 | 2.09 | 3.10 |
| [a-300k-vegeta](runs/a-300k-vegeta/results.json) | vegeta | 300k | 140.9k | 0.4696 | 0.459 | ✗ | 0 | 26.09 | 3.67 | 2.46 |
| [a-300k-wrk2](runs/a-300k-wrk2/results.json) | wrk2 | 300k | 300.0k | 1.0000 | 1.000 | ✓ | 0 | 6.69 | 2.01 | 3.00 |
| [a-400k-oha](runs/a-400k-oha/results.json) | oha | 400k | 400.1k | 1.0002 | 1.000 | ✓ | 137 | 7.79 | 3.11 | 4.33 |
| [a-400k-wrk2](runs/a-400k-wrk2/results.json) | wrk2 | 400k | 400.0k | 1.0000 | 1.000 | ✓ | 0 | 6.36 | 2.54 | 3.97 |
| [a-500k-oha](runs/a-500k-oha/results.json) | oha | 500k | 472.8k | 0.9456 | 0.923 | ✗ | 137 | 7.90 | 3.72 | 5.08 |
| [a-500k-wrk2](runs/a-500k-wrk2/results.json) | wrk2 | 500k | 500.0k | 1.0000 | 0.989 | ✓ | 0 | 6.32 | 3.16 | 4.63 |
| [a-600k-oha](runs/a-600k-oha/results.json) | oha | 600k | 472.5k | 0.7875 | 0.768 | ✗ | 137 | 7.90 | 3.73 | 5.09 |
| [a-600k-wrk2](runs/a-600k-wrk2/results.json) | wrk2 | 600k | 600.0k | 0.9999 | 0.999 | ✓ | 0 | 6.31 | 3.78 | 5.24 |
| [a-700k-wrk2](runs/a-700k-wrk2/results.json) | wrk2 | 700k | 665.8k | 0.9512 | 0.886 | ✗ | 0 | 5.91 | 3.92 | 5.66 |
| [b-01-100k-swarmgo](runs/b-01-100k-swarmgo/results.json) | swarmgo | 100k | 99.9k | 0.9990 | 0.992 | ✓ | 2 | 17.16 | 1.71 | 2.19 |
| [b-02-600k-wrk2](runs/b-02-600k-wrk2/results.json) | wrk2 | 600k | 600.0k | 1.0000 | 0.998 | ✓ | 0 | 6.36 | 3.81 | 5.27 |
| [b-03-100k-vegeta](runs/b-03-100k-vegeta/results.json) | vegeta | 100k | 100.0k | 1.0000 | 1.000 | ✓ | 0 | 25.87 | 2.59 | 1.50 |
| [b-04-400k-oha](runs/b-04-400k-oha/results.json) | oha | 400k | 400.1k | 1.0002 | 1.000 | ✓ | 137 | 8.06 | 3.22 | 4.47 |
| [b-05-600k-wrk2](runs/b-05-600k-wrk2/results.json) | wrk2 | 600k | 600.0k | 1.0000 | 0.999 | ✓ | 0 | 6.42 | 3.85 | 5.33 |
| [b-06-100k-vegeta](runs/b-06-100k-vegeta/results.json) | vegeta | 100k | 100.0k | 1.0000 | 1.000 | ✓ | 0 | 25.97 | 2.60 | 1.52 |
| [b-07-400k-oha](runs/b-07-400k-oha/results.json) | oha | 400k | 400.1k | 1.0001 | 0.999 | ✓ | 137 | 8.13 | 3.25 | 4.51 |
| [b-08-100k-swarmgo](runs/b-08-100k-swarmgo/results.json) | swarmgo | 100k | 100.0k | 1.0000 | 1.000 | ✓ | 2 | 16.86 | 1.69 | 2.16 |
| [b-09-150k-vegeta](runs/b-09-150k-vegeta/results.json) | vegeta | 150k | 143.2k | 0.9548 | 0.922 | ✗ | 0 | 25.77 | 3.67 | 2.49 |
| [b-10-450k-oha](runs/b-10-450k-oha/results.json) | oha | 450k | 450.0k | 1.0000 | 0.999 | ✓ | 137 | 8.11 | 3.65 | 4.83 |
| [b-11-150k-swarmgo](runs/b-11-150k-swarmgo/results.json) | swarmgo | 150k | 150.0k | 1.0000 | 1.000 | ✓ | 2 | 12.63 | 1.89 | 2.46 |
| [b-12-650k-wrk2](runs/b-12-650k-wrk2/results.json) | wrk2 | 650k | 649.9k | 0.9999 | 0.999 | ✓ | 0 | 6.01 | 3.90 | 5.53 |
| [b-13-450k-oha](runs/b-13-450k-oha/results.json) | oha | 450k | 450.0k | 0.9999 | 0.999 | ✓ | 137 | 8.07 | 3.63 | 4.82 |
| [b-14-150k-swarmgo](runs/b-14-150k-swarmgo/results.json) | swarmgo | 150k | 150.0k | 1.0000 | 1.000 | ✓ | 2 | 12.17 | 1.83 | 2.39 |
| [b-15-650k-wrk2](runs/b-15-650k-wrk2/results.json) | wrk2 | 650k | 650.0k | 0.9999 | 0.998 | ✓ | 0 | 6.00 | 3.90 | 5.53 |
| [b-16-450k-oha](runs/b-16-450k-oha/results.json) | oha | 450k | 450.0k | 0.9999 | 0.994 | ✓ | 137 | 8.15 | 3.67 | 4.84 |
| [b-17-150k-swarmgo](runs/b-17-150k-swarmgo/results.json) | swarmgo | 150k | – | – | – | – | – | 12.39 | 1.85 | 2.40 |
