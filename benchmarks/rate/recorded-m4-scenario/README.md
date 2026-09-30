# Apple M4: what scenario files cost

Does sending requests from a scenario file (`-config`) cost more CPU than the usual flags, and does it lower the highest rate SwarmGo can hold? Four variants were run at the same rate under the [rate harness](../README.md), with one session on one machine and no external fan.

| Variant | Build | Requests |
| --- | --- | --- |
| base | main at `9c19afa`, before scenarios | the harness's usual POST, from flags |
| new | this branch | the same POST, from flags |
| static | this branch | the same POST, from [scenarios/static.yaml](../scenarios/static.yaml) |
| mix | this branch | browse 60%, search 30%, buy 10% from [scenarios/mix.yaml](../scenarios/mix.yaml): a CSV column in the query and a header, random integers, a UUID and `{{seq}}` |

The target validates every request: the exact 1 KiB POST on `/work`, and bodiless GETs and JSON POSTs on `/mix/`.

## CPU per request at 100k/s

Generator CPU (cgroup `usage_usec`) over the measured minute, divided by the requests the target validated in it. Five rounds, with the order rotated by one each round. Every run delivered at least 99.5% of the rate with no invalid requests; 19 of 20 held (99.9%).

| Variant | Round 1 | Round 2 | Round 3 | Round 4 | Round 5 | Median | vs base |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| base | 10.83 | 10.61 | 10.56 | 10.53 | 10.53 | **10.56 µs** | |
| new | 11.44 | 11.44 | 10.55 | 10.51 | 10.52 | **10.55 µs** | −0.1% |
| static | 11.43 | 11.42 | 10.58 | 10.55 | 10.63 | **10.63 µs** | +0.7% |
| mix | 10.63 | 10.60 | 10.53 | 10.46 | 10.45 | **10.53 µs** | −0.3% |

The design's gate was within 2% of base for the flags and a one-request config, and within 10% for a three-request mix. All three pass.

**Rounds 1 and 2 ran on a slower host.** The new and static runs there took about 11.4 µs, 8% above the later rounds. The target's own CPU per request rose in the same runs, to 16.3–17.2 µs from about 15.1 µs, although the target binary and the requests it served were identical. Generator code cannot change that, so the machine itself was slower for that stretch. The first base run (10.83 µs, target 15.37 µs) also falls in it. When checked right after that stretch, a macOS on-device inference service was the busiest process on the host.

To check, three more base/new pairs were run afterwards, alternating which went first:

| Pair | base | new | Change |
| --- | ---: | ---: | ---: |
| 1 (new first) | 10.55 | 10.60 | +0.5% |
| 2 (base first) | 10.62 | 10.52 | −0.9% |
| 3 (new first) | 10.54 | 10.58 | +0.4% |
| Median | **10.55 µs** | **10.58 µs** | +0.3% |

The target stayed at 15.11–15.20 µs in all six.

A request with no variables is sent from its compiled bytes, so a one-request config does the same work as the flags. The mix costs slightly less than the 1 KiB POST: most of its requests are GETs with no body, which outweighs filling in the values (about 40 ns per request in a micro-benchmark).

## Highest rate held

Catch-up mode, one run per variant per rate, variants rotated at each rate. At 550k, flags and static were run twice more.

| Rate | flags | static | mix |
| ---: | :---: | :---: | :---: |
| 400k | ✓ 6.72 µs | ✓ 6.79 µs | ✓ 6.65 µs |
| 500k | ✓ 6.42 µs | ✓ 6.49 µs | ✓ 6.39 µs |
| 550k | ✓ ✓ ✓ | ✗ 99.89%, ✓, ✓ | ✓ 6.37 µs |
| 600k | ✗ 95.34% | ✗ 93.57% | ✗ 97.80% |

At 550k the static config held 2 of 3 runs; the run that did not held 99.89% against the 99.9% bar. Its CPU per request there (6.61–6.64 µs) is within 1% of the flags (6.57–6.59 µs). At 600k all three used 3.6–3.8 of the generator's four CPUs with no busy misses: the limit is CPU, as in the [final comparison](../recorded-m4-final/), where catch-up also held 550k and not 600k. A scenario does not lower that limit.

## Runs that did not hold

- **aborted-200k-r1-base.** The series first started at 200k. Its first run, the base build, delivered 99.71%, with dips late in the minute, and the series was restarted at 100k so that every variant would deliver its full rate. No other run at 200k was made.
- **gate-r2-mix** delivered 99.58%. Its misses fell in seconds 15–16 and 24–26, some busy and some started more than 50 ms late, while the generator used about 1.05 cores: the process stalled rather than ran out of CPU.
- **peak-550k-static** (above) and the three 600k runs.

SwarmGo exits with 2 whenever a planned request was missed, including in seconds before the measured window; several held runs have exit code 2 for misses in their first second. The verdict uses only the target's counter over the measured window.

## Files

`summary.json` lists every run with its verdict, generator and target CPU per request. `runs/<name>/` holds the harness record (`results.json`, with the manifest, samples and SwarmGo's own report), `native.json`, `run.log` and, for scenario runs, the exact `scenario.yaml` sent. The scenario build is `scenario-config` at `7a5508c`; the base build is main at `9c19afa`, built the same way (Linux ARM64, `CGO_ENABLED=0`, with `cmd/swarmgo/default.pgo`). Binary hashes are in each manifest.

## Reproduce

Prepare as in [benchmarks/rate](../README.md), then build the base alongside:

```sh
git worktree add ../swarmgo-base 9c19afa
(cd ../swarmgo-base && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$OLDPWD/benchmarks/throughput/bin/swarmgo-base" ./cmd/swarmgo)
python3 benchmarks/rate/ladder.py --tool swarmgo --rate 100000 --swarmgo-bin swarmgo-base --out gate-base
python3 benchmarks/rate/ladder.py --tool swarmgo --rate 100000 --out gate-new
python3 benchmarks/rate/ladder.py --tool swarmgo --rate 100000 --scenario static --out gate-static
python3 benchmarks/rate/ladder.py --tool swarmgo --rate 100000 --scenario mix --out gate-mix
python3 benchmarks/rate/ladder.py --tool swarmgo --rate 550000 --catch-up --scenario mix --out peak-mix
```
