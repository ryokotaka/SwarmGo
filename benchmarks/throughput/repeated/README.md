# Repeated HTTP POST throughput

Six 60-second observations per tool. SwarmGo and wrk were measured first; k6 and oha were added in a later series using the same workload, binaries and machine. Each pair was measured sequentially with the starting order reversed between groups.

![Median throughput and observed ranges](../../../assets/throughput-repeated.svg)

| Tool | Median POST/s | Minimum | Maximum | Runs |
| --- | ---: | ---: | ---: | ---: |
| wrk | **618,869** | 591,656 | 665,462 | 6 |
| SwarmGo | **576,958** | 521,353 | 613,766 | 6 |
| oha | **462,560** | 450,373 | 508,608 | 6 |
| k6 | **123,128** | 119,416 | 147,321 | 6 |

## Every observation

![Box plots with all 24 observations](../../../assets/throughput-distribution.svg)

Each dot is one complete 60-second observation. Filled dots are the first three runs for that tool; hollow dots are the next three. Boxes show the middle 50% with linearly interpolated quartiles. Whiskers show the observed minimum and maximum, not a confidence interval. Every complete comparison observation is retained.

## Conditions

- Apple M4, local ARM64 Docker VM, 10 CPUs and approximately 7.65 GiB RAM. Generator and target share the host.
- HTTP/1.1, 1 KiB POST body and response, no target delay or request-rate cap. No HTTP pipelining.
- Concurrency: SwarmGo, wrk and oha use 256; k6 uses 64 VUs. These were selected in the earlier 64/256/1,024 screening. wrk uses eight threads.
- Five seconds of warmup followed by 60 seconds of measurement. Fresh containers for each run; 15 seconds between runs in a series.
- Each generator has a 6 GiB memory budget; the target has 512 MiB. No CPU quota, additional swap or tuning between repeated runs.
- Internal Docker network, no published ports, no external load target or paid service.
- The host is a desktop environment. Background activity and temperature were not held constant. The tools were recorded in separate paired series, so these are descriptive local results rather than a universal ranking.

## Measurement and process completion

Rates use target request-counter deltas divided by target snapshot-clock deltas. The target validates method, protocol and body for every POST. All 24 comparison observations reached the full window with zero invalid requests and valid final body-byte counters.

SwarmGo uses a large fixed request count and is deadline-stopped after the observation; its exit 1 and partial report are preserved. Other tools use native durations. Stopping and report generation are outside the measured window. Native error counters have different definitions.

oha: 3 of six processes returned a nonzero exit code after the completed observation. 3 recorded a memory-limit kill after the last observed sample. Those 60-second rates remain included; native status and memory counters are retained in the records.

## Recorded runs

| Series | Order | Tool | POST/s | Record |
| --- | ---: | --- | ---: | --- |
| 1 | 1 | SwarmGo | 613,766 | [JSON](batch-1/01-swarmgo/results.json) |
| 1 | 2 | wrk | 606,612 | [JSON](batch-1/02-wrk/results.json) |
| 1 | 3 | wrk | 665,462 | [JSON](batch-1/03-wrk/results.json) |
| 1 | 4 | SwarmGo | 575,326 | [JSON](batch-1/04-swarmgo/results.json) |
| 1 | 5 | SwarmGo | 580,573 | [JSON](batch-1/05-swarmgo/results.json) |
| 1 | 6 | wrk | 624,612 | [JSON](batch-1/06-wrk/results.json) |
| 2 | 1 | wrk | 637,737 | [JSON](batch-2/01-wrk/results.json) |
| 2 | 2 | SwarmGo | 578,590 | [JSON](batch-2/02-swarmgo/results.json) |
| 2 | 3 | SwarmGo | 572,660 | [JSON](batch-2/03-swarmgo/results.json) |
| 2 | 4 | wrk | 613,125 | [JSON](batch-2/04-wrk/results.json) |
| 2 | 5 | wrk | 591,656 | [JSON](batch-2/05-wrk/results.json) |
| 2 | 6 | SwarmGo | 521,353 | [JSON](batch-2/06-swarmgo/results.json) |
| 3 | 1 | k6 | 147,321 | [JSON](batch-3/01-k6/results.json) |
| 3 | 2 | oha | 508,608 | [JSON](batch-3/02-oha/results.json) |
| 3 | 3 | oha | 482,719 | [JSON](batch-3/03-oha/results.json) |
| 3 | 4 | k6 | 129,076 | [JSON](batch-3/04-k6/results.json) |
| 3 | 5 | k6 | 124,130 | [JSON](batch-3/05-k6/results.json) |
| 3 | 6 | oha | 464,364 | [JSON](batch-3/06-oha/results.json) |
| 4 | 1 | oha | 460,757 | [JSON](batch-4/01-oha/results.json) |
| 4 | 2 | k6 | 122,127 | [JSON](batch-4/02-k6/results.json) |
| 4 | 3 | k6 | 121,337 | [JSON](batch-4/03-k6/results.json) |
| 4 | 4 | oha | 457,774 | [JSON](batch-4/04-oha/results.json) |
| 4 | 5 | oha | 450,373 | [JSON](batch-4/05-oha/results.json) |
| 4 | 6 | k6 | 119,416 | [JSON](batch-4/06-k6/results.json) |

## Reference checks

Two additional SwarmGo runs bracket the k6/oha series. They check for a large shift relative to the earlier observations and are kept separately from the fixed six-run summaries.

- before extension: **577,580 POST/s**. [Record](reference-before/results.json).
- after extension: **495,733 POST/s**. [Record](reference-after/results.json).

The after-series reference was 14.2% lower than the before-series reference. Host conditions were not controlled; the cause of the change has not been isolated.

One [interrupted attempt](interrupted-attempt/results.json) ended before its full measurement window when the schedule was paused. It is preserved as incomplete and does not count as one of the six complete observations.

## Reproduce

Use the setup and pinned tool versions in the [benchmark guide](../README.md#reproduce). Run one tool at a time, use a fresh output directory each time, and wait 15 seconds between runs.

```sh
python3 benchmarks/throughput/compare.py --tools swarmgo --seconds 60 --concurrency 256 --out repeat-swarmgo-01
python3 benchmarks/throughput/compare.py --tools k6 --seconds 60 --concurrency 64 --out repeat-k6-01
```

The [summary JSON](summary.json) includes all rates, group identities and run plans. [plot_repeated.py](../plot_repeated.py) renders both figures. The [original four-tool recording](../README.md) and short README GIF remain separate observations.
