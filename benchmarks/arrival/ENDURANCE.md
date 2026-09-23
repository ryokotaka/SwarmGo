# Sustained HTTP load

This harness compares SwarmGo and oha v1.16.0 on one local Linux ARM64 Docker engine. It requests 200,000 POSTs/s for up to 300 seconds, with concurrency 2,048 by default. Each request and response is 1 KiB. A separate 10/s GET probe begins about one second before the load and ends about one second after it. Startup is included; no warmup interval is discarded.

The generator has a 6 GiB memory limit and the target has 512 MiB. Neither has a CPU limit or extra swap. Both share the Docker VM's CPU and memory. Each run creates a fresh internal network and containers, publishes no ports, and removes only its own resources. The wall-time limit is the requested duration plus 35 seconds. Interruptions, timeouts, and generator failure stop the colocated probe as well.

Use the same Go 1.25.7 and local Docker prerequisites as [the arrival comparison](README.md). Preparation reuses `prepare.py` to build the current checkout and target, fetch its pinned k6 release, and prepare the image. It also downloads the official oha ARM64 executable and checks its pinned SHA-256. Preparation sends no load. Changed product sources or prepared binaries are rejected before a run.

```sh
python3 benchmarks/arrival/endurance.py --prepare
python3 benchmarks/arrival/endurance.py --tool swarmgo --seconds 300 --output benchmarks/arrival/results/endurance-swarmgo-1
python3 benchmarks/arrival/endurance.py --tool oha --seconds 300 --output benchmarks/arrival/results/endurance-oha-1
```

Run the tools sequentially on an otherwise idle engine; alternate order for repetitions. `--seconds` accepts 1–300 and `--concurrency` accepts 1–20,000. Every `--output` must be a new directory. Preparation and load must not run alongside another comparison.

`results.json` retains the build/source hashes, exact commands, Docker information, approximately five-second target/resource samples, process exit codes, timeout/interruption state, observed cgroup OOM events, peak memory, and the full SwarmGo native report when available. Logs and native files are kept separately. Memory covers the generator container, including its probe; samples do not replace a final counter. If the container itself is killed, unavailable final counters stay unavailable and the last observed peak is explicitly labelled as such.

oha runs with `--no-tui --output-format quiet`, which avoids final display work. In this version, [each completed response is retained](https://github.com/hatoo/oha/blob/v1.16.0/src/result_data.rs#L12-L76); [quiet only suppresses final printing](https://github.com/hatoo/oha/blob/v1.16.0/src/printer.rs#L108-L133). Quiet produces no client latency/failure summary. Its target POST/GET counts validate received request bodies and remain distinct from successful client response completions. Exit zero alone cannot establish zero HTTP or body-read failures. An OOM, incomplete native report, or missing counter is retained as a limitation, never converted into a passing workload result.

For SwarmGo, retain the native verdict, missed count, HTTP failures, and probe outcome alongside delivered counts. A useful throughput comparison does not turn an inconclusive native resilience verdict into a pass. Compare the whole scheduled POST interval; SwarmGo schedules its probe internally, whereas oha's probe is a separate process started approximately one second earlier. This harness supplies measurements, not a general claim that either tool is faster.
