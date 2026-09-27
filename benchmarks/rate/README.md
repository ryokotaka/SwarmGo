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

wrk2 does not build for ARM64 as published. [Dockerfile.wrk2](Dockerfile.wrk2) makes three build-only changes:

- its bundled LuaJIT 2.0.3 is replaced by LuaJIT 2.1 at a pinned commit;
- the one type LuaJIT 2.1 removed is renamed (`luaL_reg` → `luaL_Reg`, three lines in `src/script.c`);
- an unused x86-only `#include <x86intrin.h>` is removed from `src/hdr_histogram.c`.

wrk2's rate control and latency code are unchanged. The first M4 build stopped at that include. After the fix, the patched sources and LuaJIT 2.1 were cross-compiled for aarch64, and the unpatched `hdr_histogram.c` reproduces the failure.

## Reproduce

Requires Python 3.9+, Git, Go 1.25.7 and a local Docker engine. First prepare [benchmarks/throughput](../throughput/README.md#reproduce), which builds SwarmGo and the target and fetches k6 and oha. Then:

```sh
python3 benchmarks/rate/ladder.py --prepare
python3 benchmarks/rate/ladder.py --tool wrk2 --rate 200000 --out wrk2-200k
```

Each run uses one tool at one rate and needs a new `--out` name. `results/<out>/results.json` holds the settings, file hashes, five-second target and container CPU samples, the verdict and the tool's native report.
