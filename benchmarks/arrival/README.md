# Arrival-rate diagnostic

This harness compares the current SwarmGo `resilience` CLI with the free **k6 v2.3.0** CLI at a specified arrival rate. It records missed requests and failures as well as completed requests. It does not establish a performance win or a maximum sustainable rate.

The default workload is **200,000 POST requests/s**, a **2,048** load-concurrency ceiling, **3 seconds of warmup followed by 60 measured seconds**, and three pairs with alternating tool order. Every trial uses fresh client and target containers. There are no container CPU or memory caps: both share the local Docker VM, whose resources are recorded.

Each POST sends 1 KiB of JSON and receives 1 KiB over HTTP/1.1 keep-alive. The target validates the entire request body; clients consume responses without retaining them. The target adds no delay. Both clients also send 10 GET probes/s with up to 32 concurrent probes, starting one second before the load and ending one second after it. GET responses are 128 bytes. Request timeouts and k6's graceful-stop period are five seconds. The target's container IP is used directly, avoiding per-connection DNS lookup differences.

## Run locally

Requires Python 3.9+, Git, Go **1.25.7**, and a local Linux Docker engine, including Docker Desktop. Preparation builds the current checkout and target for the Docker engine's ARM64 or AMD64 architecture. Results from different hosts or architectures are separate measurements.

```bash
cd benchmarks/arrival
python3 bench.py --prepare
python3 bench.py --output results/run-1
```

`--prepare` downloads k6 from its fixed official GitHub release, verifies the archive checksum, reads only its regular executable entry, and fetches the pinned Debian image. The ARM64 executable also has a recorded binary-hash check. It sends no load. Build metadata and hashes are saved in `build.json`; changed product sources or binaries require preparation again.

For a small setup check:

```bash
python3 bench.py --rate 100 --seconds 2 --warmup 1 \
  --concurrency 32 --k6-vus 32 --repeats 1 --output results/smoke
```

`--tools k6` or `--tools swarmgo` runs one tool for setup or tuning.

`--k6-vus` independently sets both k6 `preAllocatedVUs` and `maxVUs`; the default is 2,048. VU tuning can change its result. Record that setting when comparing runs. `--concurrency` controls SwarmGo's load ceiling. No VU or runtime-memory tuning is implied by the defaults.

The harness mounts this directory read-only at `/bench`. It refuses remote TCP/SSH Docker contexts and existing output directories. Traffic stays on a newly created **internal Docker network with no published ports**. Each client has a wall-time limit of warmup + measured seconds + 35 seconds; override it with `--timeout`. Cleanup removes only this run's containers and network, including after a timeout or interruption.

## Read the results

`results.json` contains the settings, build and file hashes, Docker resources, target counters, cgroup CPU/memory accounting, and each trial's native exit code. Native JSON reports and logs are retained alongside it. Target validation checks zero invalid requests, no outstanding requests, total load-plus-probe counts, and POST bytes equal to load completions × 1,024. Missing k6 metrics remain `null` and are listed explicitly; they are never converted to zero.

`load_completed_per_second` and `load_missed_percent` cover the entire scheduled load phase, including startup. Both use the requested load duration; started responses may finish afterward. `load_accounting_difference` retains nominal requests minus reported completions and drops, including executor boundary effects. Small drop rates can still be useful comparative results: a native failure verdict is retained rather than used to hide those measurements.

The measured intervals differ at their boundary:

- **SwarmGo:** load windows are grouped by **scheduled start**. Responses completing later remain in their original window. `steady_missed` is available for those windows.
- **k6:** the `phase:measured` tag is set at the iteration's **actual start**, relative to its scenario start. Its counter includes responses from those iterations even if they finish later. Dropped iterations never enter the function and receive no phase tag, so the measured interval's missed count is unavailable. The whole-load dropped count is retained separately.

`steady_completed_per_second` divides that interval's count by the requested measured duration. It is not the target's whole-run rate, and boundary tagging prevents treating the two interval counts as perfectly identical cohorts. CPU, peak cgroup memory, and target counters cover the **whole run**, including startup and warmup. Peak cgroup memory is not process RSS. No cross-tool latency comparison is made.

A SwarmGo report can be **inconclusive** because of cold-start misses while later windows delivered their planned load. The harness exposes both facts: `steady_schedule_met` concerns only the selected load windows; `native_verdict` remains the whole resilience test's result. It never upgrades that result to a pass. k6 has its own whole-scenario thresholds, which are retained rather than equated with SwarmGo's probe SLOs.

Exit 0 requires every native trial to succeed and its counters to validate. Exit 1 retains completed comparisons with failed/inconclusive trials or invalid counters. Exit 2 is a setup/harness error. This directory contains the reproducible diagnostic, not a claim that 200,000 requests/s was sustained.
