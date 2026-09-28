# Constant-rate comparison

Can a load generator deliver a requested request rate? This harness asks each tool for a fixed rate of POSTs per second and checks what the target actually received.

It compares the generators built for constant-rate (open-model) load: SwarmGo's `resilience` runner, [wrk2](https://github.com/giltene/wrk2), [vegeta](https://github.com/tsenart/vegeta), k6's `constant-arrival-rate` executor and oha's `-q`. wrk is not included: it has no rate control.

## Verdict

A run **held** its rate when, over the measured window:

- the target received validated POSTs at no less than the requested rate × (1 − tolerance), with the default tolerance of 0.1%;
- the target counted zero invalid requests; and
- the tool was still running at the end of the window.

The verdict uses only the target's own counter. Each tool's native report is kept as well, but tools count missed or dropped requests differently, so none of them decides the verdict. `min_interval_ratio` shows the lowest five-second rate in the window, relative to the requested rate, so a run that held on average but dipped is visible.

## Equal conditions

- The generator runs on CPUs 0–3 and the target on CPUs 4–9 (Docker cpusets), so a tool cannot win by taking CPU from the target.
- Every tool gets the same in-flight ceiling, 1,024 by default: SwarmGo's `-c`, wrk2's connections, vegeta's `-max-workers`, k6's VUs and oha's `-c`. wrk2 runs one thread per generator CPU.
- The same fasthttp target, 1 KiB POST body and 1 KiB response as the [throughput comparison](../throughput/), on an internal Docker network with no published ports.
- Five seconds of warmup after the first request reaches the target, then 60 measured seconds. Startup behavior is outside the window.
- Generator memory 6 GiB, target 512 MiB. A tool that exceeds its memory is recorded as it happened, not retried with more.
- SwarmGo also sends one ordinary request per second to the target's `/stats` endpoint, which the target does not count as load.

## Tools

| Tool | Version | Source |
| --- | --- | --- |
| SwarmGo | this checkout | built by the throughput preparation, with `cmd/swarmgo/default.pgo` |
| wrk2 | commit `44a94c1` | built locally by `--prepare`; see below |
| vegeta | v12.13.0 | official release; checksum pinned in `ladder.py` |
| k6 | v2.3.0 | official release, from the throughput preparation |
| oha | v1.16.0 | official release, from the throughput preparation |

wrk2 does not build or run correctly on ARM64 as published. [Dockerfile.wrk2](Dockerfile.wrk2) makes five changes:

- its bundled LuaJIT 2.0.3 is replaced by LuaJIT 2.1 at a pinned commit;
- the one type LuaJIT 2.1 removed is renamed (`luaL_reg` → `luaL_Reg`, three lines in `src/script.c`);
- an unused x86-only `#include <x86intrin.h>` is removed from `src/hdr_histogram.c`;
- in `src/wrk.c`, the variable holding `getopt_long`'s result becomes an `int`. It was a `char` compared with `-1`; `char` is unsigned on ARM64, so every command line ended in the usage text;
- `time_us()` in `src/wrk.c` reads `CLOCK_MONOTONIC` instead of `gettimeofday`. wrk2 schedules requests and measures latency with this clock. A wall clock can step backwards when the Docker VM syncs its time with the host; a backward step during a request makes its latency negative, and wrk2 then aborts in its histogram. wrk2 uses these times only as differences, so a monotonic clock keeps its meaning.

The Dockerfile checks that each edit applied. wrk2's rate-control and latency logic are unchanged.

The first M4 build stopped at the include, and the first M4 smoke test found the `char` problem. Both were then reproduced away from ARM64: the unpatched `hdr_histogram.c` fails with an aarch64 cross compiler, and wrk2 built with `-funsigned-char` on x86 prints only its usage text. With all four changes, every source cross-compiles for aarch64, LuaJIT 2.1 cross-builds, and the `-funsigned-char` build held 20,000 POSTs/s against the local target with zero errors.

The first M4 ladder run then aborted wrk2 once, 36 seconds into its 100,000/s run, with a negative latency. Its send schedule and latency start use the same formula and counts, so a negative value requires the clock to move backwards between a request and its response. With the monotonic clock, wrk2 delivered the same rate as the wall-clock build in an A/B on this VM (42.6k/s for both, the VM's limit at 50,000/s requested).

The Go generators and oha already time with monotonic clocks. The harness does too: the target's `/stats` reports `monotonic_ns`, rates use it, and each sample records `wall_clock_step_seconds`, how far the wall clock moved beyond it. `max_wall_clock_step_seconds` in each result shows whether the wall clock stepped during the window.

## Reproduce

Requires Python 3.9+, Git, Go 1.25.7 and a local Docker engine. First prepare [benchmarks/throughput](../throughput/README.md#reproduce), which builds SwarmGo and the target and fetches k6 and oha. Then:

```sh
python3 benchmarks/rate/ladder.py --prepare
python3 benchmarks/rate/ladder.py --tool wrk2 --rate 200000 --out wrk2-200k
```

Each run uses one tool at one rate and needs a new `--out` name. `results/<out>/results.json` holds the settings, file hashes, five-second target and container CPU samples, the verdict and the tool's native report.

By default SwarmGo misses a request that comes due while all 1,024 connections are busy rather than sending it late. `--catch-up` passes SwarmGo's `-catch-up`, which starts such requests as soon as a connection frees, within `-max-start-delay` (50 ms by default). wrk2 likewise sends late requests, without such a limit. The setting is recorded in the manifest as `swarmgo_catch_up`.

`--profile cpu` or `--profile trace` (SwarmGo only) also writes a CPU profile of the run, or a one-second Go execution trace from 20 seconds in, to the same directory. Profiling costs CPU, so a profiled run is a diagnostic, not a result.
