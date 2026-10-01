# Benchmarks

The records below ran the load generator and an owned target in Docker on one MacBook Air (Apple M4). In the comparisons, the target validates and counts each request itself, and rates come from its counts, not from what a tool reports about itself. Each record keeps its commands, tool versions, settings, raw JSON and every run, including the ones that failed or were stopped.

| Question | Result | Record |
| --- | --- | --- |
| Uncapped 1 KiB POSTs per second | SwarmGo 640k; wrk 683k, oha 591k, k6 159k (medians of six runs) | [throughput/rerun-m4](throughput/rerun-m4/) |
| Highest fixed rate held for a minute, three times | SwarmGo 550k with `-catch-up`, 400k by default; wrk2 650k, oha 450k, vegeta 100k, k6 none | [rate/recorded-m4-final](rate/recorded-m4-final/) |
| Reported latency against the target's own timing | SwarmGo within 0.3 ms at p50 and 2.3 ms at p99, with a 5 ms hold and 200 ms stalls; four other tools compared | [rate/latency-m4](rate/latency-m4/) |
| Cost of scenario files | CPU per request within 1% of the plain flags; the same 550k ceiling | [rate/recorded-m4-scenario](rate/recorded-m4-scenario/) |
| Five minutes at 200k POSTs/s | SwarmGo 59.7 million successful POSTs, 89.7 MiB peak memory; oha stopped at its 6 GiB limit after 169 s | [arrival/recorded-endurance](arrival/recorded-endurance/) |
| Ordinary requests during a spike | Worst one-second p99 3.51 s without admission control, 92 ms with it | [examples/resilience](../examples/resilience/) |

The harnesses are [throughput/compare.py](throughput/compare.py) and [rate/ladder.py](rate/ladder.py); each record's README has the commands to reproduce it.

## Earlier records

These used older versions of SwarmGo, other targets or, for header-parsing, a cloud VM. They are kept as measured.

| Record | What it measured |
| --- | --- |
| [capacity](capacity/) | 20,000 concurrent requests to a target that waits 200 ms: SwarmGo 71,548 req/s, k6 44,478 |
| [concurrency](concurrency/) | 10,000 concurrent requests with 2 CPUs and 2 GiB: SwarmGo 47,129 req/s; k6 hit its memory limit in all five runs |
| [wrk](wrk/) | wrk on the capacity workload: 93,738 req/s, above SwarmGo's result at the time |
| [header-parsing](header-parsing/) | The in-place response-header parser and profile-guided optimization |
| [throughput](throughput/) | The first uncapped comparison, [repeated runs](throughput/repeated/) and [M4 tuning](throughput/tuning-m4/) |
| [arrival](arrival/) | SwarmGo and k6 at a fixed 200k/s arrival rate; also the harness of the five-minute trial |
| [rate](rate/) | The paced engine from 100k to 500k: [stopped](rate/recorded-m4-partial/), [rebuilt](rate/recorded-m4-claim/), [lower overhead](rate/recorded-m4-overhead/), [catch-up](rate/recorded-m4-catchup/) |

## Commit IDs in the records

Most records cite commits that are not on main: some from before main's history was rewritten, others from branches that were merged with new IDs. They still open on GitHub through the pull requests that carried them. To check one out, fetch those refs:

```sh
git fetch origin '+refs/pull/*/head:refs/remotes/origin/pr/*'
```

<details>
<summary>The same content on main</summary>

| Cited | On main, same files |
| --- | --- |
| `03a1bfd` | `22ac94b` |
| `1401778` | `a724c91` |
| `182e7d7` | `2c9c7f5` |
| `310fbf5` | `653dc7b` |
| `6b4d5df` | `f94eb97` |
| `7105a9b` | `60180fc` |
| `786d7ee` | `c04a479` |
| `7a5508c` | `44c1178` |
| `8fd2b73` | `7dbf9fe` |
| `95425fe` | `b552648` |
| `9dd8960` | `897a84e` |
| `a9997b6` | `5fc6a7d` |
| `b9ab0ae` | `171ca03` |
| `e281d6e` | `6b701c5` |
| `ef9e13f` | `8dd9017` |
| `fb19876` | `1be473a` |
| `27c8a47`, `607ff79` | none; SwarmGo's code is the same as `c01a79f` (pull request 1) |
| `413400f`, `45b89f8`, `468ba43` | none; intermediate commits of pull request 1 |

</details>
