# Uncapped HTTP throughput

This comparison removes the request-rate limit from SwarmGo, wrk, oha and k6. It measures how much POST traffic reaches the same local API, then checks whether that rate holds for a full minute.

Each tool was screened once at 64, 256 and 1,024 connections. The fastest observed setting for each tool was selected for one 60-second observation. SwarmGo, wrk and oha selected 256; k6 selected 64. This is a small configuration search, not an exhaustive maximum or a repeated-trial confidence estimate.

## Recorded minute

![Target-side RPS over the measured minute](../../assets/throughput.svg)

| Tool | Connections | Target POSTs/s | Observed duration |
| --- | ---: | ---: | ---: |
| wrk | 256 | 575,360 | 60.10 s |
| SwarmGo | 256 | 517,832 | 60.10 s |
| oha | 256 | 414,956 | 60.13 s |
| k6 | 64 | 119,480 | 60.07 s |

SwarmGo delivered 4.33× k6's target-side rate in this one-minute comparison, and 90% of wrk's. It did not beat wrk. Every target reported zero invalid requests. wrk's full native run reported 81 `timeout` counters; the raw report retains them. In this version that counter is incremented when a completed response's elapsed time cannot fit its configured latency histogram, so it is not a count of requests aborted by a deadline. These values use the same counter definition and observation window; they are not cross-tool latency comparisons or an all-tools superiority claim.

The [screening records](recorded/) retain all three connection settings, including the ones that were slower. [plot.py](plot.py) renders the interval-rate figure from the minute records using matplotlib.

## Conditions

- Apple M4; one local ARM64 Docker VM with 10 CPUs and approximately 7.65 GiB RAM. Client and target share the machine.
- Each generator has 6 GiB; the target has 512 MiB. No CPU quota, CPU pinning, additional swap or requested RPS limit.
- One HTTP/1.1 POST at a time per connection, 1 KiB JSON request and 1 KiB response. No target delay or separate probe. The fasthttp target validates every request's method, protocol and complete body. All generators read responses; no response-body equality callback is installed on any client.
- Fresh containers for every trial, on an internal Docker network with no published ports. There is no external load target, cloud runner or paid service.
- A common five-second warmup follows the first observed target activity. Counters are then sampled approximately every five seconds. Rates use the change in target request count divided by the change in the target's snapshot clock. The measurement excludes native stopping and report generation.
- wrk 4.2.0 uses eight threads. oha v1.16.0 has neither `-q` nor latency correction enabled. k6 v2.3.0 uses `constant-vus` with no sleep and discards response bodies after reading them.

SwarmGo's fixed-count CLI has no duration executor. The harness gives it 2,147,483,647 requests so it will not exhaust the count during observation, and a deadline ten seconds longer than the measurement. Its later deadline stop returns a partial report and exit 1; cancellation errors and the incomplete verdict are retained. The observed window is not relabelled as a successful fixed-count run. wrk, oha and k6 use their native duration settings, also ten seconds longer than the measurement.

The generator and target share CPU, so these are complete local-workload results, not isolated generator limits. The earlier [200k/s endurance comparison](../arrival/recorded-endurance/) uses a different target and an explicitly requested rate. Its points must not be mixed into this comparison.

## Reproduce

Requires Python 3, Go 1.25.7 and a local Linux ARM64 Docker engine. Use the same recorded versions. Preparation downloads only free OSS tools; it sends no load.

From the repository root:

```sh
python3 benchmarks/arrival/endurance.py --prepare
mkdir -p benchmarks/throughput/bin
cp benchmarks/arrival/bin/swarmgo benchmarks/arrival/bin/k6 benchmarks/arrival/bin/oha benchmarks/throughput/bin/
cp benchmarks/arrival/body.json benchmarks/throughput/body.json
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o benchmarks/throughput/bin/target benchmarks/throughput/target.go
```

Build wrk using the [existing instructions](../wrk/#reproduce), which create `swarmgo-wrk-local:4.2.0`. No rebuild is needed if that matching image is already present.

```sh
python3 benchmarks/throughput/compare.py --seconds 10 --concurrency 64 --out screen-64
python3 benchmarks/throughput/compare.py --seconds 10 --concurrency 256 --out screen-256
python3 benchmarks/throughput/compare.py --seconds 10 --concurrency 1024 --out screen-1024
# Select each tool's best screened setting; run each separately for 60 seconds.
python3 benchmarks/throughput/compare.py --tools swarmgo --seconds 60 --concurrency 256 --out minute-swarmgo
python3 benchmarks/throughput/compare.py --tools wrk --seconds 60 --concurrency 256 --out minute-wrk
python3 benchmarks/throughput/compare.py --tools oha --seconds 60 --concurrency 256 --out minute-oha
python3 benchmarks/throughput/compare.py --tools k6 --seconds 60 --concurrency 64 --out minute-k6
```

Every output directory must be new. Keep trials sequential. The script rejects remote Docker contexts and removes only its own containers and network when finished or interrupted.

## Recorded files

`recorded/` preserves commands, hashes, counters, samples, native reports and logs. `selected-settings.json` records the selection from the three screened connection counts. The 60-second trials ran in order: wrk, SwarmGo, k6, oha. Each screen and confirmation is one trial.

`recorded/compare-executed.py` is the exact pilot harness and expects its original `maximum-load-check` directory beside `SwarmGo`. The packaged `compare.py` changes those two repository paths; the workload and measurement logic are the same. Target source and binary hashes are in every manifest. SwarmGo's measured product sources are unchanged from `607ff79`; the binary was rebuilt from `27c8a47`, which added documentation and benchmark files.
