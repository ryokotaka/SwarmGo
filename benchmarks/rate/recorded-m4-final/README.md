# Apple M4 constant-rate comparison

Can each generator keep delivering a requested rate of POSTs? This is the complete comparison: wrk2, oha, vegeta, k6 and SwarmGo, with SwarmGo run both in its default mode and with catch-up, all in one session on one machine under the same conditions. Method and verdict: [benchmarks/rate](../README.md).

## Highest confirmed rate

A rate is confirmed when three 60-second runs at it all held (the target received at least 99.9% of the requested rate, with no invalid requests).

| Tool | Confirmed | Next step up |
| --- | ---: | --- |
| wrk2 | **650k** | 700k did not hold (96.82%) |
| SwarmGo, catch-up | **550k** | 600k did not hold (95.62%) |
| oha | **450k** | 500k did not hold (97.93%) |
| SwarmGo, default | **400k** | 450k held 0 of 1 (99.89%) |
| vegeta | **100k** | 150k did not hold (96.11%) |
| k6 | none | 100k did not hold (91.90%) |

SwarmGo's default mode does not start a request more than 1 ms late when every connection is busy; it counts it as missed instead. With `-catch-up` it starts such requests as soon as a connection frees, up to 50 ms late. wrk2 also sends late requests, without a limit.

## Ladder

One run per tool per rate, lowest rate first, with the tool order rotated by one at each rate. A tool stopped after two consecutive rates that did not hold.

| Rate | wrk2 | SwarmGo, catch-up | oha | SwarmGo, default | vegeta | k6 |
| ---: | :---: | :---: | :---: | :---: | :---: | :---: |
| 100k | ✓ | ✓ | ✓ | ✓ | ✓ | ✗ 91.90% |
| 200k | ✓ | ✓ | ✓ | ✓ | ✗ 73.08% | ✗ 48.14% |
| 300k | ✓ | ✓ | ✓ | ✓ | ✗ 48.45% | – |
| 400k | ✓ | ✓ | ✓ | ✓ | – | – |
| 500k | ✓ | ✓ | ✗ 97.93% | ✗ 99.61% | – | – |
| 600k | ✓ | ✗ 95.62% | ✗ 81.50% | ✗ 95.76% | – | – |
| 700k | ✗ 96.82% | ✗ 80.00% | – | – | – | – |

## CPU for the same rate

Generator CPU per request, from the ladder run at the rate:

| Tool | At 100k | At 400k |
| --- | ---: | ---: |
| wrk2 | 12.77 µs | 6.12 µs |
| SwarmGo, catch-up | 10.49 µs | 6.76 µs |
| SwarmGo, default | 10.45 µs | 6.79 µs |
| oha | 17.34 µs | 7.59 µs |
| vegeta | 24.86 µs | – |

100k is the highest rate every tool but k6 held; 400k is the highest held by the top four.

## Observations

- **SwarmGo's default mode stops on busy connections, not CPU.** At 450k and 500k it used 2.96–3.19 of its four generator CPUs; the shortfall was busy misses (0.17% of planned requests at 450k, 0.40% at 500k). With catch-up there were no busy misses in any run, and 500k and 550k held with 3.2–3.6 generator cores.
- **At the top, the generators are CPU-bound.** SwarmGo with catch-up used 3.69 cores at 600k; wrk2 used 3.87–3.90 at 650k, with the target at 5.44–5.49 of its six CPUs.
- **SwarmGo's remaining gap to wrk2 is about 10% CPU per request at 400k** (6.76–6.79 µs against 6.12 µs). Counting system calls in the generator during 400k runs, SwarmGo made 2.00 read(2) calls per response and wrk2 1.00; writes were 1.00 for both. Go's `net.Conn.Read` first reads, finds no data yet and waits, then reads again; wrk2 waits for readability first. Skipping the first read through `syscall.RawConn` can miss a response that arrives before the runtime resets its readiness state, which a test exposed as a rare hang, so that change was not kept.
- **At 100k SwarmGo used the least CPU per request** of all tools (10.45–10.49 µs, against 12.77 µs for wrk2).
- **oha exited with 137 in runs from 300k up.** In each case the memory-limit kill came after the measured window; no run was killed inside it. SwarmGo exits with 2 ("inconclusive") whenever at least one planned request was missed. The verdict uses only the target's counter.
- **No wall-clock steps:** the largest difference between the target's wall and monotonic clocks in any sample was 6.6 ms.

## Earlier M4 records

[recorded-m4-partial](../recorded-m4-partial/) (stopped), [recorded-m4-claim](../recorded-m4-claim/), [recorded-m4-overhead](../recorded-m4-overhead/) and [recorded-m4-catchup](../recorded-m4-catchup/) track SwarmGo's engine from 100k to 500k confirmed. They were measured in separate sessions, the first three with an external fan, so this record is the one to use for comparing tools.

## Conditions

- MacBook Air (Mac16,13), Apple M4, 24 GB memory, on AC power with Low Power Mode off, **without an external fan**. macOS 27.0.
- Docker Desktop 4.60.1, Engine 29.2.0, Linux ARM64 VM with 10 CPUs and approximately 7.65 GiB RAM.
- Harness defaults: generator on CPUs 0–3, target on CPUs 4–9, 1,024 requests in flight for every tool, five seconds of warmup after the first request, then 60 measured seconds, 0.1% tolerance. Generator memory 6 GiB, target 512 MiB.
- Tools: SwarmGo built with PGO at `786d7ee` (catch-up runs with `--catch-up` and the default 50 ms `-max-start-delay`), wrk2 `44a94c1` (image built by `--prepare` at the same checkout), vegeta v12.13.0, k6 v2.3.0, oha v1.16.0.
- Runs were sequential. The ladder (step A) went up from 100k with the tool order rotated by one per rate; confirmation (step B) interleaved the tools one run at a time, running each tool's highest held rate until three runs held, stepping down on a miss, and then 50k above it.

## Every run

Ratio is delivered ÷ requested; "lowest 5 s" is the lowest five-second interval relative to the requested rate. CPU figures use the first and last samples of the window, with elapsed time from the target's monotonic clock. Busy and late misses come from SwarmGo's own report (`native.json`, `load`).

| # | Step | Record | Tool | Requested | Delivered | Ratio | Lowest 5 s | held | Exit | Gen µs/req | Gen cores | Target cores | Busy missed | Late missed |
| ---: | :---: | --- | --- | ---: | ---: | ---: | ---: | :---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | A | [f-a-100k-swarmgo](runs/f-a-100k-swarmgo/results.json) | SwarmGo, default | 100k | 100.0k | 1.0000 | 1.000 | ✓ | 0 | 10.45 | 1.05 | 1.51 | 0 | 0 |
| 2 | A | [f-a-100k-swarmgo-catchup](runs/f-a-100k-swarmgo-catchup/results.json) | SwarmGo, catch-up | 100k | 100.0k | 1.0000 | 1.000 | ✓ | 0 | 10.49 | 1.05 | 1.51 | 0 | 0 |
| 3 | A | [f-a-100k-wrk2](runs/f-a-100k-wrk2/results.json) | wrk2 | 100k | 100.0k | 1.0000 | 1.000 | ✓ | 0 | 12.77 | 1.28 | 1.31 | – | – |
| 4 | A | [f-a-100k-vegeta](runs/f-a-100k-vegeta/results.json) | vegeta | 100k | 100.0k | 1.0000 | 1.000 | ✓ | 0 | 24.86 | 2.49 | 1.43 | – | – |
| 5 | A | [f-a-100k-k6](runs/f-a-100k-k6/results.json) | k6 | 100k | 91.9k | 0.9190 | 0.857 | ✗ | 0 | 38.30 | 3.54 | 1.99 | – | – |
| 6 | A | [f-a-100k-oha](runs/f-a-100k-oha/results.json) | oha | 100k | 100.0k | 1.0000 | 1.000 | ✓ | 0 | 17.34 | 1.73 | 1.99 | – | – |
| 7 | A | [f-a-200k-swarmgo-catchup](runs/f-a-200k-swarmgo-catchup/results.json) | SwarmGo, catch-up | 200k | 200.0k | 1.0000 | 1.000 | ✓ | 0 | 7.92 | 1.58 | 2.14 | 0 | 0 |
| 8 | A | [f-a-200k-wrk2](runs/f-a-200k-wrk2/results.json) | wrk2 | 200k | 200.0k | 1.0000 | 0.999 | ✓ | 0 | 8.65 | 1.73 | 2.14 | – | – |
| 9 | A | [f-a-200k-vegeta](runs/f-a-200k-vegeta/results.json) | vegeta | 200k | 146.2k | 0.7308 | 0.710 | ✗ | 0 | 25.15 | 3.66 | 2.49 | – | – |
| 10 | A | [f-a-200k-k6](runs/f-a-200k-k6/results.json) | k6 | 200k | 96.3k | 0.4814 | 0.444 | ✗ | 0 | 41.00 | 3.98 | 2.08 | – | – |
| 11 | A | [f-a-200k-oha](runs/f-a-200k-oha/results.json) | oha | 200k | 200.0k | 1.0000 | 1.000 | ✓ | 0 | 9.55 | 1.91 | 2.54 | – | – |
| 12 | A | [f-a-200k-swarmgo](runs/f-a-200k-swarmgo/results.json) | SwarmGo, default | 200k | 199.9k | 0.9996 | 0.995 | ✓ | 2 | 7.91 | 1.58 | 2.15 | 5,248 | 0 |
| 13 | A | [f-a-300k-wrk2](runs/f-a-300k-wrk2/results.json) | wrk2 | 300k | 300.0k | 1.0000 | 1.000 | ✓ | 0 | 6.68 | 2.00 | 2.95 | – | – |
| 14 | A | [f-a-300k-vegeta](runs/f-a-300k-vegeta/results.json) | vegeta | 300k | 145.3k | 0.4845 | 0.469 | ✗ | 0 | 25.32 | 3.67 | 2.48 | – | – |
| 15 | A | [f-a-300k-oha](runs/f-a-300k-oha/results.json) | oha | 300k | 300.0k | 1.0000 | 1.000 | ✓ | 137 | 7.75 | 2.33 | 3.29 | – | – |
| 16 | A | [f-a-300k-swarmgo](runs/f-a-300k-swarmgo/results.json) | SwarmGo, default | 300k | 299.7k | 0.9990 | 0.989 | ✓ | 2 | 7.20 | 2.16 | 2.84 | 25,171 | 0 |
| 17 | A | [f-a-300k-swarmgo-catchup](runs/f-a-300k-swarmgo-catchup/results.json) | SwarmGo, catch-up | 300k | 300.0k | 1.0000 | 1.000 | ✓ | 0 | 7.19 | 2.16 | 2.84 | 0 | 0 |
| 18 | A | [f-a-400k-oha](runs/f-a-400k-oha/results.json) | oha | 400k | 400.1k | 1.0002 | 1.000 | ✓ | 137 | 7.59 | 3.04 | 4.28 | – | – |
| 19 | A | [f-a-400k-swarmgo](runs/f-a-400k-swarmgo/results.json) | SwarmGo, default | 400k | 399.9k | 0.9999 | 0.999 | ✓ | 2 | 6.79 | 2.72 | 3.78 | 20,014 | 0 |
| 20 | A | [f-a-400k-swarmgo-catchup](runs/f-a-400k-swarmgo-catchup/results.json) | SwarmGo, catch-up | 400k | 400.0k | 1.0000 | 1.000 | ✓ | 2 | 6.76 | 2.70 | 3.73 | 0 | 4,708 |
| 21 | A | [f-a-400k-wrk2](runs/f-a-400k-wrk2/results.json) | wrk2 | 400k | 400.0k | 1.0000 | 1.000 | ✓ | 0 | 6.12 | 2.45 | 3.86 | – | – |
| 22 | A | [f-a-500k-oha](runs/f-a-500k-oha/results.json) | oha | 500k | 489.6k | 0.9793 | 0.960 | ✗ | 137 | 7.63 | 3.73 | 5.13 | – | – |
| 23 | A | [f-a-500k-swarmgo](runs/f-a-500k-swarmgo/results.json) | SwarmGo, default | 500k | 498.1k | 0.9961 | 0.974 | ✗ | 2 | 6.41 | 3.19 | 4.60 | 138,579 | 0 |
| 24 | A | [f-a-500k-swarmgo-catchup](runs/f-a-500k-swarmgo-catchup/results.json) | SwarmGo, catch-up | 500k | 500.0k | 1.0000 | 1.000 | ✓ | 0 | 6.44 | 3.22 | 4.62 | 0 | 0 |
| 25 | A | [f-a-500k-wrk2](runs/f-a-500k-wrk2/results.json) | wrk2 | 500k | 500.0k | 1.0000 | 1.000 | ✓ | 0 | 6.02 | 3.01 | 4.58 | – | – |
| 26 | A | [f-a-600k-oha](runs/f-a-600k-oha/results.json) | oha | 600k | 489.0k | 0.8150 | 0.795 | ✗ | 137 | 7.63 | 3.72 | 5.10 | – | – |
| 27 | A | [f-a-600k-swarmgo](runs/f-a-600k-swarmgo/results.json) | SwarmGo, default | 600k | 574.5k | 0.9576 | 0.938 | ✗ | 2 | 6.53 | 3.75 | 5.40 | 1,856,857 | 18,899 |
| 28 | A | [f-a-600k-swarmgo-catchup](runs/f-a-600k-swarmgo-catchup/results.json) | SwarmGo, catch-up | 600k | 573.7k | 0.9562 | 0.933 | ✗ | 2 | 6.44 | 3.69 | 5.36 | 0 | 1,784,986 |
| 29 | A | [f-a-600k-wrk2](runs/f-a-600k-wrk2/results.json) | wrk2 | 600k | 600.0k | 1.0000 | 0.999 | ✓ | 0 | 6.19 | 3.71 | 5.14 | – | – |
| 30 | A | [f-a-700k-swarmgo-catchup](runs/f-a-700k-swarmgo-catchup/results.json) | SwarmGo, catch-up | 700k | 560.0k | 0.8000 | 0.782 | ✗ | 2 | 6.44 | 3.60 | 5.30 | 0 | 9,777,205 |
| 31 | A | [f-a-700k-wrk2](runs/f-a-700k-wrk2/results.json) | wrk2 | 700k | 677.7k | 0.9682 | 0.936 | ✗ | 0 | 5.75 | 3.91 | 5.62 | – | – |
| 32 | B | [f-b-01-400k-swarmgo](runs/f-b-01-400k-swarmgo/results.json) | SwarmGo, default | 400k | 400.0k | 0.9999 | 1.000 | ✓ | 2 | 6.72 | 2.69 | 3.75 | 14,934 | 0 |
| 33 | B | [f-b-02-500k-swarmgo-catchup](runs/f-b-02-500k-swarmgo-catchup/results.json) | SwarmGo, catch-up | 500k | 500.0k | 1.0000 | 1.000 | ✓ | 2 | 6.46 | 3.23 | 4.60 | 0 | 475 |
| 34 | B | [f-b-03-600k-wrk2](runs/f-b-03-600k-wrk2/results.json) | wrk2 | 600k | 600.0k | 1.0000 | 0.999 | ✓ | 0 | 6.18 | 3.71 | 5.11 | – | – |
| 35 | B | [f-b-04-100k-vegeta](runs/f-b-04-100k-vegeta/results.json) | vegeta | 100k | 100.0k | 1.0000 | 0.999 | ✓ | 0 | 24.80 | 2.48 | 1.44 | – | – |
| 36 | B | [f-b-05-400k-oha](runs/f-b-05-400k-oha/results.json) | oha | 400k | 400.0k | 1.0000 | 1.000 | ✓ | 137 | 7.62 | 3.05 | 4.28 | – | – |
| 37 | B | [f-b-06-500k-swarmgo-catchup](runs/f-b-06-500k-swarmgo-catchup/results.json) | SwarmGo, catch-up | 500k | 500.0k | 1.0000 | 0.999 | ✓ | 2 | 6.44 | 3.22 | 4.60 | 0 | 1,621 |
| 38 | B | [f-b-07-600k-wrk2](runs/f-b-07-600k-wrk2/results.json) | wrk2 | 600k | 600.0k | 1.0000 | 0.998 | ✓ | 0 | 6.19 | 3.71 | 5.12 | – | – |
| 39 | B | [f-b-08-100k-vegeta](runs/f-b-08-100k-vegeta/results.json) | vegeta | 100k | 100.0k | 1.0000 | 1.000 | ✓ | 0 | 24.96 | 2.50 | 1.45 | – | – |
| 40 | B | [f-b-09-400k-oha](runs/f-b-09-400k-oha/results.json) | oha | 400k | 400.1k | 1.0001 | 0.999 | ✓ | 137 | 7.59 | 3.04 | 4.29 | – | – |
| 41 | B | [f-b-10-400k-swarmgo](runs/f-b-10-400k-swarmgo/results.json) | SwarmGo, default | 400k | 399.9k | 0.9999 | 0.999 | ✓ | 2 | 6.73 | 2.69 | 3.74 | 21,703 | 0 |
| 42 | B | [f-b-11-650k-wrk2](runs/f-b-11-650k-wrk2/results.json) | wrk2 | 650k | 650.0k | 1.0000 | 0.994 | ✓ | 0 | 6.00 | 3.90 | 5.49 | – | – |
| 43 | B | [f-b-12-150k-vegeta](runs/f-b-12-150k-vegeta/results.json) | vegeta | 150k | 144.2k | 0.9611 | 0.924 | ✗ | 0 | 25.63 | 3.68 | 2.48 | – | – |
| 44 | B | [f-b-13-450k-oha](runs/f-b-13-450k-oha/results.json) | oha | 450k | 450.0k | 1.0001 | 0.999 | ✓ | 137 | 7.87 | 3.54 | 4.70 | – | – |
| 45 | B | [f-b-14-450k-swarmgo](runs/f-b-14-450k-swarmgo/results.json) | SwarmGo, default | 450k | 449.5k | 0.9989 | 0.995 | ✗ | 2 | 6.58 | 2.96 | 4.31 | 52,786 | 204 |
| 46 | B | [f-b-15-550k-swarmgo-catchup](runs/f-b-15-550k-swarmgo-catchup/results.json) | SwarmGo, catch-up | 550k | 550.0k | 1.0000 | 0.999 | ✓ | 2 | 6.57 | 3.61 | 5.00 | 0 | 4,064 |
| 47 | B | [f-b-16-450k-oha](runs/f-b-16-450k-oha/results.json) | oha | 450k | 450.0k | 1.0000 | 0.999 | ✓ | 137 | 7.84 | 3.53 | 4.71 | – | – |
| 48 | B | [f-b-17-550k-swarmgo-catchup](runs/f-b-17-550k-swarmgo-catchup/results.json) | SwarmGo, catch-up | 550k | 550.0k | 1.0000 | 1.000 | ✓ | 2 | 6.63 | 3.64 | 5.05 | 0 | 13,516 |
| 49 | B | [f-b-18-650k-wrk2](runs/f-b-18-650k-wrk2/results.json) | wrk2 | 650k | 649.9k | 0.9999 | 0.999 | ✓ | 0 | 5.98 | 3.89 | 5.44 | – | – |
| 50 | B | [f-b-19-450k-oha](runs/f-b-19-450k-oha/results.json) | oha | 450k | 450.0k | 0.9999 | 0.999 | ✓ | 137 | 7.76 | 3.49 | 4.70 | – | – |
| 51 | B | [f-b-20-550k-swarmgo-catchup](runs/f-b-20-550k-swarmgo-catchup/results.json) | SwarmGo, catch-up | 550k | 550.0k | 1.0000 | 0.999 | ✓ | 0 | 6.55 | 3.60 | 5.06 | 0 | 0 |
| 52 | B | [f-b-21-650k-wrk2](runs/f-b-21-650k-wrk2/results.json) | wrk2 | 650k | 650.0k | 1.0000 | 0.998 | ✓ | 0 | 5.96 | 3.87 | 5.46 | – | – |
