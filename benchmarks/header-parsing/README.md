# Response-header fast path

Commit `471e471` added an in-place parser for common HTTP/1.1 response headers ([direct_head.go](../../internal/worker/direct_head.go)). Unusual responses still go to fasthttp's full parser. This record covers why the change was made, what it saved, and a larger redesign that was tested and not adopted.

These measurements come from a 4-vCPU Linux cloud VM, not the Apple M4 used for the [throughput comparison](../throughput/repeated/). They measure the change itself. They are not a new headline throughput figure.

## Why the header parser

A CPU profile of the worker sending 1 KiB POSTs over loopback showed:

- About 67% of CPU time in syscalls: one `write` per request and two reads per request (see below).
- Of the remaining user-space time, about 70% in fasthttp's `ResponseHeader.Read`, which normalizes and copies every header field.

The worker only needs the status, `Content-Length` or chunked framing, `Content-Encoding` and `Connection`. The fast path reads these without copying. It still checks every field for valid bytes, and passes anything else to fasthttp on the same unconsumed bytes.

## Per-request cost without the network

`BenchmarkDirectRequest*` replaces the connection with one that returns a fixed response. The benchmark isolates the worker's own work per request: building the request, parsing the header and consuming the body. Ten runs per version, alternating in two blocks of five.

| Response | Before (`03a1bfd`) | After | Change | Allocations |
| --- | ---: | ---: | ---: | --- |
| 1 KiB, `Content-Length` | 914.8 ns | **334.7 ns** | −63% | 1 → **0** |
| 1 KiB, chunked | 1,064.0 ns | **532.9 ns** | −50% | 3 → 1 |

Full output: [benchstat.txt](benchstat.txt), [micro-old.txt](micro-old.txt) and [micro-new.txt](micro-new.txt). Intel Xeon @ 2.80 GHz, Go 1.25.7, `-cpu 4`. Reproduce with:

```sh
go test ./internal/worker -run '^$' -bench DirectRequest -benchmem -count 10
```

For the older commit, copy `internal/worker/direct_bench_test.go` into a checkout of `03a1bfd` first.

## Effect on loopback throughput

The loopback check uses [loopback/main.go](loopback/main.go) with the generator on one CPU (`GOMAXPROCS=1`) and the [throughput target](../throughput/target.go) on the other three. Eight alternating runs per version, 800,000 POSTs each at 128 connections, after a warmup.

| | Before | After | Change |
| --- | ---: | ---: | ---: |
| Median CPU per request | 8.77 µs | 8.30 µs | −5% |
| Median requests/s | 111,166 | 113,743 | +2% |

Raw lines: [loopback-ab.txt](loopback-ab.txt). The run-to-run spread on this VM was about ±8%, so the throughput change is within noise. The lower CPU per request is consistent with the micro-benchmark. On loopback, the kernel's TCP work dominates each request, so saving about 0.5 µs of user-space time has only a small effect. A generator that spends less time in the kernel per request would gain proportionally more. The "after" runs used the fast path before a later change to the read-error branch, which successful requests do not use.

## Not adopted: an epoll event loop

The profile also showed two read syscalls per request (`/proc/self/io` `syscr` = 2.00). After writing a request, the worker's first read usually returns `EAGAIN`. Go's netpoller then parks the goroutine and reads again once the socket is ready. A wrk-style design would read only after `epoll_wait` reports the socket as readable. It would also avoid goroutine scheduling.

A minimal prototype was tested: raw `epoll` loops on locked OS threads, the same request bytes, and a simple `Content-Length` parser. It sent the same workload through the same target. Three alternating runs each:

| Generator CPUs | Go netpoller (SwarmGo) | epoll prototype |
| --- | --- | --- |
| 1 | 7.5–9.6 µs/request, 104k–132k req/s | 8.1–9.1 µs/request, 109k–122k req/s |
| 2 | 8.5–9.3 µs/request, 140k–152k req/s | 9.4–11.0 µs/request, 127k–138k req/s |

The prototype showed no measurable improvement. The extra read and the scheduler round trip cost a few hundred nanoseconds, but the loopback TCP work per request costs several microseconds. A rewrite would also be Linux-only and would have to reimplement deadlines, cancellation and TLS. SwarmGo keeps the Go netpoller. The prototype code was discarded; these figures are its only record.

## Correctness

[direct_head_test.go](../../internal/worker/direct_head_test.go) requires every header accepted by the fast path to be read identically by fasthttp, including the number of bytes consumed. It covers table cases and a fuzz target (`FuzzParseHead`). The fuzz target ran for more than five minutes during development without a mismatch. It passed another 90 seconds on the final code. One earlier difference was found this way: fasthttp treats only an exact `Connection: close` as closing. The fast path now sends any other `Connection` value to fasthttp.
