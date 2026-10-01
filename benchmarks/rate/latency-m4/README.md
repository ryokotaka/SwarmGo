# Apple M4 latency-accuracy check

Does each generator report the latency the target actually produced? Every tool sends 20k POSTs/s, well within all of their capacity, to a target that holds each request for a known time. The target also records how long it held each request, which is the ground truth the tools' reports are compared with. Method and harness: [benchmarks/rate](../README.md).

- **Delay:** the target holds every request 5 ms (`--target-query delay_us=5000`).
- **Stall:** the same, plus every 5 s the target holds everything that arrives during a 200 ms window until the window ends (`delay_us=5000&stall_every_ms=5000&stall_ms=200`). This is the classic coordinated-omission test: a tool that stops sending while the target is stalled, and measures only from when it finally sends, reports a latency far below what a user arriving on schedule would see. Measured from each request's scheduled time, about 4% of requests fall in a stall and wait 5–205 ms, so the true p99 is roughly 155 ms or more.

Each tool and scenario ran twice; values below are shown as "first / second". Target-held times are measured inside the target, from receiving a request to answering it, and do not include the network.

## Delay: 5 ms per request

| Tool | Delivered | Reported p50 (ms) | Reported p99 (ms) | Target-held p50 (ms) | Target-held p99 (ms) | Not sent on schedule |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| SwarmGo, default | 100.00% / 100.00% | 6.1 / 6.1 | 11.8 / 11.3 | 5.9 / 5.8 | 9.9 / 9.7 | 0 missed / 0 missed |
| SwarmGo, catch-up | 100.00% / 100.00% | 6.1 / 6.1 | 12.4 / 10.7 | 5.9 / 5.8 | 10.2 / 8.8 | 0 missed / 252 missed |
| wrk2 | 99.99% / 100.01% | 7.0 / 7.1 | 9.9 / 10.1 | 5.7 / 5.6 | 7.0 / 7.0 | – / – |
| oha | 100.00% / 100.00% | 6.4 / 6.5 | 7.9 / 7.7 | 5.9 / 5.9 | 7.0 / 6.8 | – / – |
| vegeta | 100.00% / 100.00% | 6.0 / 6.0 | 10.5 / 10.6 | 5.4 / 5.4 | 7.3 / 7.3 | – / – |
| k6 | 100.00% / 100.00% | 5.8 / 5.8 | 19.1 / 19.2 | 5.4 / 5.3 | 6.8 / 6.9 | 975 dropped / 676 dropped |

## Stall: 5 ms per request, and 200 ms every 5 s

| Tool | Delivered | Reported p50 (ms) | Reported p99 (ms) | Target-held p50 (ms) | Target-held p99 (ms) | Not sent on schedule |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| SwarmGo, default | 96.79% / 96.74% | 6.1 / 6.1 | 168.3 / 169.3 | 5.9 / 5.8 | 166.4 / 167.9 | 45,160 missed / 45,675 missed |
| SwarmGo, catch-up | 97.78% / 97.72% | 6.1 / 6.1 | 165.6 / 170.4 | 5.8 / 5.9 | 163.3 / 168.1 | 30,929 missed / 31,942 missed |
| wrk2 | 100.00% / 100.01% | 7.2 / 7.1 | 177.4 / 175.7 | 5.7 / 5.6 | 165.5 / 166.8 | sent late / sent late |
| oha | 100.00% / 100.00% | 6.5 / 6.6 | 172.1 / 170.7 | 5.9 / 5.9 | 163.8 / 162.0 | sent late / sent late |
| vegeta | 100.00% / 100.00% | 6.0 / 6.0 | 116.9 / 106.4 | 5.5 / 5.4 | 163.2 / 158.7 | sent late / sent late |
| k6 | 96.79% / 96.75% | 5.8 / 5.8 | 169.0 / 168.1 | 5.3 / 5.3 | 164.4 / 164.4 | 45,572 dropped / 46,666 dropped |

## Observations

- **Every tool reports the median correctly.** With a 5 ms hold, reported p50 was within 0.2–0.6 ms of the target's own median for SwarmGo, oha, vegeta and k6. wrk2 reported about 1.4 ms more; it measures from each request's scheduled send time, so any lag in its own sending adds to the reported latency.
- **Tail latency differs.** oha's p99 was closest to the target's (7.7–7.9 ms against 6.8–7.0 ms). k6 reported 19.1–19.2 ms against a target p99 of 6.8–6.9 ms, far above what the target produced.
- **vegeta's p99 in the stall scenario is an estimate, and it came out low.** It reported 106–117 ms while the target's own p99 was 159–163 ms. Every latency vegeta measures includes the time the target held that request, so the exact p99 of its measurements cannot be below the target's. vegeta computes percentiles with a t-digest, and here p99 sits at the edge of the stalled requests, about 1% of the total. [vegeta-check](vegeta-check/) reproduces this rate, delay, stall pattern and 1,024-worker cap in one process: the estimate was 91–102 ms, the exact p99 of the same latencies 159–160 ms.
- **With its workers capped, vegeta also leaves out queueing time.** Here `-max-workers` was 1,024, the in-flight limit every tool shared. Once all of them were waiting on the stall, vegeta sent the next requests late and measured each from when it was sent. At 20k/s that moved the exact p99 by only about 5 ms (159–160 against 164–165 ms from the scheduled time). With fewer workers for the rate it hides the stall almost entirely: at 2,000/s with 50 workers, the exact p99 was 6–29 ms against 183–184 ms from the scheduled time ([reported upstream](https://github.com/tsenart/vegeta/issues/766)). With unlimited workers, its estimate, its exact p99 and the scheduled-time p99 agree. wrk2 and oha measure late requests from their scheduled time and reported 171–177 ms.
- **SwarmGo reports what it could not send instead of hiding it.** With 1,024 requests in flight all held by the stall, the default mode counted 45,160–45,675 requests (3.2%) as missed and reported their share of the rate as not delivered; the requests it did send report 168–169 ms at p99, matching the target. With catch-up it sent those that could start within 50 ms (97.7% delivered) and counted the rest as missed. k6 behaves similarly, reporting 45–47k dropped iterations.
- **SwarmGo started requests up to about 5 ms late at p99** even in the plain delay scenario (start delay p99 4.4–5.4 ms), and the target's own p99 was higher under SwarmGo (8.8–10.2 ms) than under the other tools (6.8–7.3 ms), which suggests SwarmGo's sends arrive in bursts at this rate. Its reported latency is measured from the actual send, so the start delay is reported separately rather than added to latency.
- No run had invalid requests, and the largest difference between the target's wall and monotonic clocks in any sample was 0.8 ms.

## Conditions

- MacBook Air (Mac16,13), Apple M4, 24 GB memory, on AC power with Low Power Mode off, without an external fan. macOS 27.0.
- Docker Desktop 4.60.1, Engine 29.2.0, Linux ARM64 VM with 10 CPUs and approximately 7.65 GiB RAM.
- Harness defaults: generator on CPUs 0–3, target on CPUs 4–9, 1,024 requests in flight for every tool, five seconds of warmup, then 60 measured seconds. 20k POSTs/s requested.
- Tools: SwarmGo built with PGO at `8fd2b73` (catch-up runs with `--catch-up`, 50 ms `-max-start-delay`), wrk2 `44a94c1` with `--latency` (its corrected distribution), vegeta v12.13.0, k6 v2.3.0 (p99 added to its summary), oha v1.16.0 with `--latency-correction`.
- Latency values are each tool's own report, converted to microseconds by the harness (`reported_latency_us`). The target's held times come from its `/stats` at the end of each run (`held_*`).
- Runs were sequential: delay then stall with one tool order, then stall then delay with another.

## Every run

| # | Scenario | Record | Tool | Delivered | held | Exit | Reported p50 / p99 (ms) | Target-held p50 / p99 (ms) | SwarmGo start delay p99 |
| ---: | --- | --- | --- | ---: | :---: | ---: | --- | --- | ---: |
| 1 | delay | [lat-01-delay-swarmgo](runs/lat-01-delay-swarmgo/results.json) | SwarmGo, default | 100.00% | ✓ | 0 | 6.1 / 11.8 | 5.9 / 9.9 | 4.8 ms |
| 2 | delay | [lat-02-delay-swarmgo-catchup](runs/lat-02-delay-swarmgo-catchup/results.json) | SwarmGo, catch-up | 100.00% | ✓ | 0 | 6.1 / 12.4 | 5.9 / 10.2 | 5.4 ms |
| 3 | delay | [lat-03-delay-wrk2](runs/lat-03-delay-wrk2/results.json) | wrk2 | 99.99% | ✓ | 0 | 7.0 / 9.9 | 5.7 / 7.0 | – |
| 4 | delay | [lat-04-delay-vegeta](runs/lat-04-delay-vegeta/results.json) | vegeta | 100.00% | ✓ | 0 | 6.0 / 10.5 | 5.4 / 7.3 | – |
| 5 | delay | [lat-05-delay-k6](runs/lat-05-delay-k6/results.json) | k6 | 100.00% | ✓ | 0 | 5.8 / 19.1 | 5.4 / 6.8 | – |
| 6 | delay | [lat-06-delay-oha](runs/lat-06-delay-oha/results.json) | oha | 100.00% | ✓ | 0 | 6.4 / 7.9 | 5.9 / 7.0 | – |
| 7 | stall | [lat-07-stall-k6](runs/lat-07-stall-k6/results.json) | k6 | 96.79% | ✗ | 0 | 5.8 / 169.0 | 5.3 / 164.4 | – |
| 8 | stall | [lat-08-stall-oha](runs/lat-08-stall-oha/results.json) | oha | 100.00% | ✓ | 0 | 6.5 / 172.1 | 5.9 / 163.8 | – |
| 9 | stall | [lat-09-stall-swarmgo](runs/lat-09-stall-swarmgo/results.json) | SwarmGo, default | 96.79% | ✗ | 2 | 6.1 / 168.3 | 5.9 / 166.4 | 5.0 ms |
| 10 | stall | [lat-10-stall-swarmgo-catchup](runs/lat-10-stall-swarmgo-catchup/results.json) | SwarmGo, catch-up | 97.78% | ✗ | 2 | 6.1 / 165.6 | 5.8 / 163.3 | 13.2 ms |
| 11 | stall | [lat-11-stall-wrk2](runs/lat-11-stall-wrk2/results.json) | wrk2 | 100.00% | ✓ | 0 | 7.2 / 177.4 | 5.7 / 165.5 | – |
| 12 | stall | [lat-12-stall-vegeta](runs/lat-12-stall-vegeta/results.json) | vegeta | 100.00% | ✓ | 0 | 6.0 / 116.9 | 5.5 / 163.2 | – |
| 13 | stall | [lat-13-stall-oha](runs/lat-13-stall-oha/results.json) | oha | 100.00% | ✓ | 0 | 6.6 / 170.7 | 5.9 / 162.0 | – |
| 14 | stall | [lat-14-stall-vegeta](runs/lat-14-stall-vegeta/results.json) | vegeta | 100.00% | ✓ | 0 | 6.0 / 106.4 | 5.4 / 158.7 | – |
| 15 | stall | [lat-15-stall-k6](runs/lat-15-stall-k6/results.json) | k6 | 96.75% | ✗ | 0 | 5.8 / 168.1 | 5.3 / 164.4 | – |
| 16 | stall | [lat-16-stall-wrk2](runs/lat-16-stall-wrk2/results.json) | wrk2 | 100.01% | ✓ | 0 | 7.1 / 175.7 | 5.6 / 166.8 | – |
| 17 | stall | [lat-17-stall-swarmgo-catchup](runs/lat-17-stall-swarmgo-catchup/results.json) | SwarmGo, catch-up | 97.72% | ✗ | 2 | 6.1 / 170.4 | 5.9 / 168.1 | 15.5 ms |
| 18 | stall | [lat-18-stall-swarmgo](runs/lat-18-stall-swarmgo/results.json) | SwarmGo, default | 96.74% | ✗ | 2 | 6.1 / 169.3 | 5.8 / 167.9 | 5.5 ms |
| 19 | delay | [lat-19-delay-wrk2](runs/lat-19-delay-wrk2/results.json) | wrk2 | 100.01% | ✓ | 0 | 7.1 / 10.1 | 5.6 / 7.0 | – |
| 20 | delay | [lat-20-delay-swarmgo-catchup](runs/lat-20-delay-swarmgo-catchup/results.json) | SwarmGo, catch-up | 100.00% | ✓ | 2 | 6.1 / 10.7 | 5.8 / 8.8 | 4.4 ms |
| 21 | delay | [lat-21-delay-swarmgo](runs/lat-21-delay-swarmgo/results.json) | SwarmGo, default | 100.00% | ✓ | 0 | 6.1 / 11.3 | 5.8 / 9.7 | 4.5 ms |
| 22 | delay | [lat-22-delay-oha](runs/lat-22-delay-oha/results.json) | oha | 100.00% | ✓ | 0 | 6.5 / 7.7 | 5.9 / 6.8 | – |
| 23 | delay | [lat-23-delay-vegeta](runs/lat-23-delay-vegeta/results.json) | vegeta | 100.00% | ✓ | 0 | 6.0 / 10.6 | 5.4 / 7.3 | – |
| 24 | delay | [lat-24-delay-k6](runs/lat-24-delay-k6/results.json) | k6 | 100.00% | ✓ | 0 | 5.8 / 19.2 | 5.3 / 6.9 | – |
