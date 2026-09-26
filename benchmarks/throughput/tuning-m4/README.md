# Apple M4 tuning follow-up

The [M4 re-measurement](../rerun-m4/) found SwarmGo at wrk's generator CPU per request (6.72 vs 6.67 µs) but about 6% lower throughput. On a shared host, throughput is the host's CPU divided by the generator's and the target's combined CPU per request, so this follow-up records the target's CPU as well and runs in two phases:

- **Phase 1, shared host:** SwarmGo with different GOMAXPROCS settings and builds, against wrk. Generator and target share all 10 CPUs.
- **Phase 2, pinned CPUs:** the generator on CPUs 0–3 and the target on CPUs 4–9, to compare generators on equal, separate CPU budgets. wrk, oha and k6 are included.

Variants:

- **head:** `claude/nice-albattani-rglt0t` at `ef9e13f`, with the request-timeout watchdog and profile-guided optimization (the build shows `-pgo=…/cmd/swarmgo/default.pgo`)
- **head-p8 / p6 / p4:** head with `--gomaxprocs 8`, `6` or `4`
- **main:** `origin/main` at `f3712ec`, with neither change. The difference between head and main is the combined effect of the watchdog and PGO.

All 44 runs are complete and valid; none were excluded. Aggregates are in [`summary.json`](summary.json).

## Phase 1: shared host

| Variant | Median POST/s | All four runs | Generator µs/req | Target µs/req | Generator cores | Target cores | Total cores |
| --- | ---: | --- | ---: | ---: | ---: | ---: | ---: |
| wrk | **679.2k** | 685k, 677k, 679k, 680k | 6.64 | 7.24 | 4.50 | 4.92 | 9.42 |
| head | **652.7k** | 655k, 653k, 652k, 651k | 6.51 | 7.43 | 4.23 | 4.83 | 9.06 |
| head-p8 | **653.6k** | 654k, 655k, 654k, 653k | 6.37 | 7.57 | 4.15 | 4.93 | 9.08 |
| head-p6 | **657.8k** | 656k, 663k, 658k, 657k | 6.01 | 7.68 | 3.95 | 5.04 | 8.99 |
| head-p4 | **667.8k** | 661k, 674k, 668k, 668k | 5.37 | 8.17 | 3.56 | 5.43 | 8.99 |
| main | **640.0k** | 641k, 639k, 642k, 638k | 6.73 | 7.51 | 4.29 | 4.79 | 9.08 |

## Phase 2: generator on CPUs 0–3, target on CPUs 4–9

| Variant | Median POST/s | All four runs | Generator µs/req | Target µs/req | Generator cores | Target cores | Total cores |
| --- | ---: | --- | ---: | ---: | ---: | ---: | ---: |
| SwarmGo (head) | **677.4k** | 676k, 679k, 681k, 675k | 5.47 | 7.91 | 3.69 | 5.32 | 9.00 |
| wrk, 4 threads | **743.7k** | 738k, 747k, 741k, 746k | 5.32 | 7.58 | 3.94 | 5.62 | 9.56 |
| wrk, 8 threads | **726.3k** | 727k, 730k, 725k, 726k | 5.46 | 7.81 | 3.95 | 5.66 | 9.61 |
| oha | **609.6k** | 604k, 610k, 609k, 612k | 6.53 | 8.94 | 3.96 | 5.42 | 9.39 |
| k6, 64 VUs | **147.3k** | 148k, 147k, 147k, 147k | 26.92 | 19.79 | 3.96 | 2.91 | 6.87 |

SwarmGo and k6 set GOMAXPROCS to 4 from the cpuset; oha sizes itself to the available CPUs.

## Observations

- **head vs main:** the watchdog and PGO together raise SwarmGo from 640.0k to 652.7k (+2.0%) and lower generator CPU per request from 6.73 to 6.51 µs.
- **GOMAXPROCS on the shared host:** fewer Go processors lower SwarmGo's CPU per request (6.51 → 5.37 µs at 4) and leave the target more cores (4.83 → 5.43), raising throughput to 667.8k (+2.3% over head). The gap to wrk narrows from 3.9% to 1.7%.
- **SwarmGo leaves CPU unused.** Total cores stay near 9.0 for every SwarmGo variant but reach 9.4 for wrk. With its own four CPUs in phase 2, SwarmGo used 3.69 cores while wrk, oha and k6 each used 3.94–3.96.
- **SwarmGo makes the target work harder.** The target spends more CPU per request for SwarmGo than for wrk in both phases (7.43 vs 7.24 µs in phase 1, 7.91 vs 7.58 µs in phase 2), and more as SwarmGo's GOMAXPROCS falls (8.17 µs at 4).
- **Target limit in phase 2:** the target used at most 5.67 of its 6 CPUs (wrk, 8 threads; 5.59–5.67 across the wrk runs). The wrk results may therefore be partly limited by the target, so its phase 2 figure is a lower bound for wrk rather than wrk's full capacity. SwarmGo's runs used 5.30–5.37 target cores and 3.67–3.70 of its 4 generator CPUs, so they were limited by the generator.
- **wrk threads:** 4 threads (one per pinned CPU) was 2.4% faster than 8.

## Conditions

- MacBook Air (Mac16,13), Apple M4, 24 GB memory, on AC power with Low Power Mode off. macOS 27.0.
- Docker Desktop 4.60.1, Engine 29.2.0, Linux ARM64 VM with 10 CPUs and approximately 7.65 GiB RAM.
- Workload as in the [throughput comparison](../): HTTP/1.1, 1 KiB POST body and response, no rate cap, internal Docker network, no published ports.
- Five seconds of warmup, then 60 seconds of measurement. Fresh containers for each run. Runs are sequential.
- Concurrency 256 for SwarmGo, wrk and oha; 64 VUs for k6. wrk uses 8 threads unless noted.
- Generator memory 6 GiB, target 512 MiB. No CPU quota; phase 2 uses Docker cpusets only.
- head, head-p*, wrk and all of phase 2 ran from the head worktree; main ran from a worktree at `origin/main` with head's `compare.py` copied in to record target CPU. Both used Go 1.25.7 and the same target binary.

## Measurement

Throughput is `results[0].target_rps`, counted when `completed_observation` and `target_valid` are both true. CPU per request is the container's `usage_usec` delta divided by the target's request delta, and cores are the same `usage_usec` delta divided by the elapsed target snapshot time, both between the first and last samples of the window (about 5 s to 60 s). The generator is the client container; the target is the target container.

Exit codes: SwarmGo is deadline-stopped after the observation and exits 1 by design. oha exited 137 in all four phase 2 runs after reaching its 6 GiB memory limit after the window; peak memory during the window was 5.38–5.64 GiB. Those rates are included, as in the earlier series.

## Run order

Phase 1 rotated six variants in blocks of six:

| Block | Runs | Order |
| ---: | --- | --- |
| 1 | 1–6 | wrk, head, head-p8, head-p6, head-p4, main |
| 2 | 7–12 | head, head-p8, head-p6, head-p4, main, wrk |
| 3 | 13–18 | head-p8, head-p6, head-p4, main, wrk, head |
| 4 | 19–24 | head-p6, head-p4, main, wrk, head, head-p8 |

Phase 2 followed, rotating five variants in blocks of five:

| Block | Runs | Order |
| ---: | --- | --- |
| 1 | 1–5 | SwarmGo, wrk 4, wrk 8, oha, k6 |
| 2 | 6–10 | wrk 4, wrk 8, oha, k6, SwarmGo |
| 3 | 11–15 | wrk 8, oha, k6, SwarmGo, wrk 4 |
| 4 | 16–20 | oha, k6, SwarmGo, wrk 4, wrk 8 |

## Every run

| Run | Record | POST/s | Generator cores | Target cores | Exit |
| --- | --- | ---: | ---: | ---: | ---: |
| 1-1 | [tune-01-wrk](phase1/tune-01-wrk/results.json) | 685.2k | 4.51 | 4.90 | 0 |
| 1-2 | [tune-02-head](phase1/tune-02-head/results.json) | 654.9k | 4.23 | 4.84 | 1 |
| 1-3 | [tune-03-head-p8](phase1/tune-03-head-p8/results.json) | 653.8k | 4.16 | 4.93 | 1 |
| 1-4 | [tune-04-head-p6](phase1/tune-04-head-p6/results.json) | 656.2k | 3.93 | 5.04 | 1 |
| 1-5 | [tune-05-head-p4](phase1/tune-05-head-p4/results.json) | 661.2k | 3.54 | 5.40 | 1 |
| 1-6 | [tune-06-main](phase1/tune-06-main/results.json) | 640.7k | 4.29 | 4.80 | 1 |
| 1-7 | [tune-07-head](phase1/tune-07-head/results.json) | 653.3k | 4.25 | 4.82 | 1 |
| 1-8 | [tune-08-head-p8](phase1/tune-08-head-p8/results.json) | 655.4k | 4.15 | 4.94 | 1 |
| 1-9 | [tune-09-head-p6](phase1/tune-09-head-p6/results.json) | 663.2k | 3.97 | 5.04 | 1 |
| 1-10 | [tune-10-head-p4](phase1/tune-10-head-p4/results.json) | 674.4k | 3.60 | 5.46 | 1 |
| 1-11 | [tune-11-main](phase1/tune-11-main/results.json) | 639.2k | 4.29 | 4.78 | 1 |
| 1-12 | [tune-12-wrk](phase1/tune-12-wrk/results.json) | 676.9k | 4.48 | 4.95 | 0 |
| 1-13 | [tune-13-head-p8](phase1/tune-13-head-p8/results.json) | 653.5k | 4.14 | 4.91 | 1 |
| 1-14 | [tune-14-head-p6](phase1/tune-14-head-p6/results.json) | 658.1k | 3.90 | 5.03 | 1 |
| 1-15 | [tune-15-head-p4](phase1/tune-15-head-p4/results.json) | 667.7k | 3.52 | 5.39 | 1 |
| 1-16 | [tune-16-main](phase1/tune-16-main/results.json) | 641.9k | 4.30 | 4.76 | 1 |
| 1-17 | [tune-17-wrk](phase1/tune-17-wrk/results.json) | 678.9k | 4.51 | 4.89 | 0 |
| 1-18 | [tune-18-head](phase1/tune-18-head/results.json) | 652.1k | 4.22 | 4.84 | 1 |
| 1-19 | [tune-19-head-p6](phase1/tune-19-head-p6/results.json) | 657.4k | 3.99 | 5.02 | 1 |
| 1-20 | [tune-20-head-p4](phase1/tune-20-head-p4/results.json) | 667.9k | 3.57 | 5.47 | 1 |
| 1-21 | [tune-21-main](phase1/tune-21-main/results.json) | 637.7k | 4.28 | 4.81 | 1 |
| 1-22 | [tune-22-wrk](phase1/tune-22-wrk/results.json) | 679.5k | 4.50 | 4.93 | 0 |
| 1-23 | [tune-23-head](phase1/tune-23-head/results.json) | 651.0k | 4.23 | 4.82 | 1 |
| 1-24 | [tune-24-head-p8](phase1/tune-24-head-p8/results.json) | 652.5k | 4.15 | 4.93 | 1 |
| 2-1 | [pin-01-head](phase2/pin-01-head/results.json) | 676.1k | 3.67 | 5.33 | 1 |
| 2-2 | [pin-02-wrk4](phase2/pin-02-wrk4/results.json) | 737.5k | 3.94 | 5.59 | 0 |
| 2-3 | [pin-03-wrk8](phase2/pin-03-wrk8/results.json) | 726.8k | 3.95 | 5.66 | 0 |
| 2-4 | [pin-04-oha](phase2/pin-04-oha/results.json) | 604.0k | 3.96 | 5.42 | 137 |
| 2-5 | [pin-05-k6](phase2/pin-05-k6/results.json) | 147.6k | 3.95 | 2.90 | 0 |
| 2-6 | [pin-06-wrk4](phase2/pin-06-wrk4/results.json) | 746.5k | 3.94 | 5.63 | 0 |
| 2-7 | [pin-07-wrk8](phase2/pin-07-wrk8/results.json) | 729.6k | 3.95 | 5.66 | 0 |
| 2-8 | [pin-08-oha](phase2/pin-08-oha/results.json) | 610.4k | 3.96 | 5.43 | 137 |
| 2-9 | [pin-09-k6](phase2/pin-09-k6/results.json) | 147.2k | 3.96 | 2.91 | 0 |
| 2-10 | [pin-10-head](phase2/pin-10-head/results.json) | 678.6k | 3.70 | 5.30 | 1 |
| 2-11 | [pin-11-wrk8](phase2/pin-11-wrk8/results.json) | 725.2k | 3.95 | 5.64 | 0 |
| 2-12 | [pin-12-oha](phase2/pin-12-oha/results.json) | 608.8k | 3.96 | 5.43 | 137 |
| 2-13 | [pin-13-k6](phase2/pin-13-k6/results.json) | 147.4k | 3.96 | 2.91 | 0 |
| 2-14 | [pin-14-head](phase2/pin-14-head/results.json) | 680.5k | 3.70 | 5.37 | 1 |
| 2-15 | [pin-15-wrk4](phase2/pin-15-wrk4/results.json) | 741.5k | 3.94 | 5.61 | 0 |
| 2-16 | [pin-16-oha](phase2/pin-16-oha/results.json) | 612.1k | 3.96 | 5.41 | 137 |
| 2-17 | [pin-17-k6](phase2/pin-17-k6/results.json) | 146.7k | 3.96 | 2.92 | 0 |
| 2-18 | [pin-18-head](phase2/pin-18-head/results.json) | 675.1k | 3.68 | 5.30 | 1 |
| 2-19 | [pin-19-wrk4](phase2/pin-19-wrk4/results.json) | 745.9k | 3.95 | 5.63 | 0 |
| 2-20 | [pin-20-wrk8](phase2/pin-20-wrk8/results.json) | 725.8k | 3.95 | 5.67 | 0 |
