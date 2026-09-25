# Does normal traffic survive a load spike?

This example runs against an API on your own machine. The API has eight processing slots, each taking 80 ms. A second run enables a small per-client admission limit so you can compare ordinary-request latency before and after the change.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../../assets/resilience-dark.svg">
  <source media="(prefers-color-scheme: light)" srcset="../../assets/resilience.svg">
  <img alt="Ordinary-request latency before and after admission control" src="../../assets/resilience.svg">
</picture>

The recorded run used **200 load requests/s for three seconds**, alongside 10 ordinary requests/s throughout a two-second baseline, the spike and seven seconds of recovery observation.

| Observed result | Before | With admission control |
| --- | ---: | ---: |
| Ordinary latency during the spike, worst one-second p99 | 3.51 s | 92 ms |
| Successful ordinary requests across the whole run | 120/120 | 120/120 |
| Load requests started / planned | 600/600 | 600/600 |
| Load requests rejected with HTTP 429 | 0 | 477 |
| Check against the 250 ms ordinary-request limit | Fail | Pass |

The protected API stayed within the latency limit. Without the limit, ordinary requests eventually succeeded but kept waiting well after the spike ended. Recovery was confirmed six seconds after the scheduled load stopped. For the protected API, the two-second confirmation window completed with no preceding degradation.

These are single local demonstrations of an API change, not generator performance benchmarks. Each one-second window has ten ordinary samples, so its p99 is effectively the slowest request. [Before JSON](results/before.json), [after JSON](results/after.json) and [source revision and environment](results/manifest.json) preserve the measurement. Results will vary with the machine. Regenerate the light and dark figures with `python3 examples/resilience/plot.py` after installing matplotlib.

## Run it locally

For an automatic before/after run using Go, Python 3 and a **local** Docker engine:

```sh
python3 examples/resilience/demo.py
```

This builds the current source, uses an internal Docker network with no published ports, writes both reports to `resilience-results/`, and removes its containers afterward. It does not use a hosted testing service. Use `--output PATH` for another empty result directory.

## Run the steps yourself

Build both programs from the repository root:

```sh
go build -o swarmgo ./cmd/swarmgo
go build -o /tmp/swarmgo-demo-api ./examples/resilience
```

Start the API in another terminal:

```sh
/tmp/swarmgo-demo-api
```

Run the scenario:

```sh
./swarmgo resilience -url http://127.0.0.1:8080/work \
  -rate 200 -c 512 -header 'X-Test-Client: bulk' \
  -probe-rate 10 -probe-c 64 -probe-header 'X-Test-Client: ordinary' \
  -baseline 2s -spike 3s -recovery 7s -recovery-window 2s \
  -max-p99 250ms -output before.json
```

Stop the API with Ctrl+C, restart it with admission control, then repeat the same scenario with `-output after.json`:

```sh
/tmp/swarmgo-demo-api -limit 40
```

The headers identify the two test clients. They are only fixture labels, not authentication or a production security policy. Both streams use the same `/work` endpoint and processing slots.

## Read the result

Look at the **ordinary requests** during the spike. A 429 on the bulk stream records a rejection; a 429 on the ordinary stream fails its availability check. The report also shows when ordinary requests recover after the scheduled load ends. Background requests already in flight may continue until completion or the request timeout.

Exit codes: `0` means the baseline and spike meet the ordinary-request limits and recovery is confirmed; `1` means the spike/recovery check fails; `2` means a configuration/runtime error or an inconclusive run. Missed or interrupted requests, or load requests without complete HTTP responses, prevent a passing result. These can arise from either target behavior or generator/network limits and need investigation. Complete HTTP rejection responses such as 429 remain separate from incomplete responses. Keep all reports, including inconclusive runs.

<details>
<summary>Timing, response checks and report fields</summary>

The JSON contains separate `load` and `probe` streams, one-second windows, scheduling delays, status-code counts and phase assessments. Request windows are grouped by **planned start time**; an eventual slow response stays in its original window. Phase latency is the **worst one-second p99**, not the p99 of the whole phase. Recovery requires consecutive healthy windows and no later regression through the observation period. Recovery confirmation includes the completion time of those windows' responses. Each dispatch batch holds at most 1 ms of planned work. After a late wake, batches may run consecutively to catch up within `-max-start-delay`; older slots are missed. Started counts describe request attempts, including connection establishment, rather than timestamps observed by the server.

This mode runs on one machine with separate connection pools for each stream. It shares the process and CPU, so missed or late probe starts must be checked before attributing a slowdown to the API. It checks final status codes and complete response bodies, not application-specific JSON fields. Probe method/body/headers are independent (`-probe-method`, `-probe-body-file`, `-probe-header`) and are not inherited from the bulk stream.

Reports omit request headers and bodies, URL user information, query strings and fragments. The scheme, host, port and path remain so you can identify the tested endpoints.

</details>

This demonstrates HTTP API behavior under bounded overload. It does not measure network-wide DDoS protection or establish superiority over another load-testing tool.
