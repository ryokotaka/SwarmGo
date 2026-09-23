"""Local arrival-rate comparison. --prepare builds files without sending load."""

from pathlib import Path
import argparse
import json
import math
import os
import platform
import signal
import subprocess
import sys
import time
import uuid

from prepare import BODY, IMAGE, ROOT, Docker, prepare, product_hash, sha256


def resources(docker, name):
    lines = docker.run(
        "exec", name, "sh", "-c",
        "cat /sys/fs/cgroup/memory.peak /sys/fs/cgroup/cpu.stat /sys/fs/cgroup/memory.events",
        timeout=10,
    ).splitlines()
    return {"memory_peak_bytes": int(lines[0]), **{k: int(v) for k, v in (line.split() for line in lines[1:])}}


def stats(docker, target):
    return json.loads(docker.run("exec", target, "/bench/bin/target", "stats", timeout=10))


def number(value, label):
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0:
        raise ValueError(f"Invalid or missing numeric value: {label}")
    return value


def metric(raw, name, field, missing):
    value = raw.get("metrics", {}).get(name, {}).get("values", {}).get(field)
    if value is None:
        missing.append(f"{name}.{field}")
        return None
    return number(value, f"{name}.{field}")


def read_summary(record, raw, args):
    total_seconds = args.warmup + args.seconds
    record["steady_expected"] = args.rate * args.seconds
    record["missing_metrics"] = []
    if record["tool"] == "swarmgo":
        load, probe = raw["load"], raw["probe"]
        windows = load["windows"]
        if len(windows) != total_seconds or [w["index"] for w in windows] != list(range(total_seconds)):
            raise ValueError("SwarmGo report does not contain every scheduled one-second load window.")
        steady = windows[args.warmup:]
        for key in ["completed", "failed", "missed"]:
            record[key] = number(load[key], f"load.{key}")
        for key in ["planned", "started", "completed", "failed", "missed", "incomplete_responses"]:
            record[f"steady_{key}"] = sum(number(w[key], key) for w in steady)
        record["probe_completed"] = number(probe["completed"], "probe.completed")
        record["probe_missed"] = number(probe["missed"], "probe.missed")
        record["native_verdict"] = raw["status"]
        record["native_reasons"] = raw.get("reasons", [])
        record["native_success"] = raw["status"] == "pass" and record["exit_code"] == 0
        record["steady_schedule_met"] = (
            record["steady_planned"] == record["steady_started"] == record["steady_completed"] == record["steady_expected"]
            and record["steady_missed"] == record["steady_failed"] == record["steady_incomplete_responses"] == 0
        )
        record["whole_load_responses_successful"] = load["failed"] == 0 and load["incomplete_responses"] == 0
        record["windows"] = windows
    else:
        missing = record["missing_metrics"]
        record["completed"] = metric(raw, "http_reqs{scenario:load}", "count", missing)
        record["steady_completed"] = metric(raw, "http_reqs{scenario:load,phase:measured}", "count", missing)
        record["failed_rate"] = metric(raw, "http_req_failed{scenario:load}", "rate", missing)
        record["missed"] = metric(raw, "dropped_iterations{scenario:load}", "count", missing)
        record["probe_completed"] = metric(raw, "http_reqs{scenario:probe}", "count", missing)
        record["probe_failed_rate"] = metric(raw, "http_req_failed{scenario:probe}", "rate", missing)
        record["probe_missed"] = metric(raw, "dropped_iterations{scenario:probe}", "count", missing)
        # Dropped iterations never run load(), so they have no phase tag. The
        # measured interval's misses cannot be recovered from this summary.
        record["steady_missed"] = None
        record["steady_schedule_met"] = None
        record["steady_missed_note"] = "Unavailable: dropped iterations have no actual-start phase tag; missed covers the entire load scenario."
        record["native_success"] = record["exit_code"] == 0
        record["native_verdict"] = "thresholds_passed" if record["native_success"] else "thresholds_failed_or_runtime_error"
        record["whole_load_responses_successful"] = record["failed_rate"] == 0 if record["failed_rate"] is not None else None
    completed = record["steady_completed"]
    record["steady_completed_per_second"] = completed / args.seconds if completed is not None else None
    # The whole scheduled load phase is shared by both executors, including
    # startup. Keep reported drops and the nominal-count boundary discrepancy
    # separate; do not manufacture missing k6 metrics from a subtraction.
    planned = args.rate * total_seconds
    done, missed = record["completed"], record["missed"]
    record["load_duration_seconds"] = total_seconds
    record["load_requested"] = planned
    record["load_completed_per_second"] = done / total_seconds if done is not None else None
    record["load_missed_percent"] = 100 * missed / planned if missed is not None else None
    record["load_accounting_difference"] = planned - done - missed if done is not None and missed is not None else None


def validate_target(record):
    issues = []
    server = record.get("server")
    load, probe = record.get("completed"), record.get("probe_completed")
    if server is None or load is None or probe is None:
        return ["Target or client counters unavailable; target validation cannot be completed."]
    if server["invalid"] != 0:
        issues.append("Target rejected a request's method, HTTP version, or complete body.")
    if server["active_requests"] != 0:
        issues.append("Target still has active requests; counters are not final.")
    if server["requests"] != load + probe:
        issues.append("Target request count differs from load plus probe HTTP completions.")
    if server["body_bytes"] != load * len(BODY):
        issues.append("Target POST bytes differ from load HTTP completions times 1024.")
    if record.get("whole_load_responses_successful") is not True:
        issues.append("Some load responses failed, or their failure metric is unavailable.")
    return issues


def run_trial(docker, network, output, args, repetition, tool, created):
    label = f"{repetition}-{tool}"
    target, client = network + "-target", network + "-client"
    for name, cmd in [(target, ["/bench/bin/target"]), (client, ["sleep", "infinity"])]:
        # Register the intended unique name first so an interrupted docker run
        # cannot strand a container whose creation succeeded on the daemon.
        created.append(name)
        docker.run("run", "--pull=never", "-d", "--name", name, "--network", network,
                   "-e", "K6_NO_USAGE_REPORT=true", "-v", f"{ROOT}:/bench:ro",
                   "-v", f"{output}:/results", IMAGE, *cmd)
    deadline = time.monotonic() + 10
    while True:
        try:
            stats(docker, target)
            break
        except (subprocess.SubprocessError, ValueError):
            if time.monotonic() >= deadline:
                raise RuntimeError("Target did not become ready.")
            time.sleep(.05)
    address = docker.run("inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", target)
    url, summary = f"http://{address}:8080/work", f"/results/{label}.json"
    total_seconds = args.warmup + args.seconds
    if tool == "swarmgo":
        cmd = docker.prefix + ["exec", client, "/bench/bin/swarmgo", "resilience",
            "-url", url, "-method", "POST", "-body-file", "/bench/body.json",
            "-header", "Content-Type: application/json", "-header", "Accept-Encoding: identity",
            "-rate", str(args.rate), "-c", str(args.concurrency), "-baseline", "1s",
            "-spike", f"{total_seconds}s", "-recovery", "1s", "-recovery-window", "1s",
            "-probe-rate", "10", "-probe-c", "32", "-max-p99", "250ms",
            "-request-timeout", "5s", "-max-start-delay", "50ms", "-output", summary]
    else:
        env = {"RATE": args.rate, "SECONDS": total_seconds, "WARMUP": args.warmup,
               "CONCURRENCY": args.k6_vus, "TARGET": url, "SUMMARY": summary}
        cmd = docker.prefix + ["exec", *[part for k, v in env.items() for part in ["-e", f"{k}={v}"]],
                              client, "/bench/bin/k6", "run", "--quiet", "/bench/k6.js"]
    before = resources(docker, target)
    started = time.monotonic()
    record = {"tool": tool, "repetition": repetition, "exit_code": None, "timed_out": False,
              "native_success": False, "native_report": label + ".json", "log": label + ".log"}
    with (output / (label + ".log")).open("w") as log:
        try:
            run = subprocess.run(cmd, stdout=log, stderr=subprocess.STDOUT,
                                 timeout=args.timeout or total_seconds + 35)
            record["exit_code"] = run.returncode
        except subprocess.TimeoutExpired:
            record["timed_out"] = True
            log.write("\nHarness timeout; client and target containers will be removed.\n")
            # Killing the local docker-exec process alone leaves its workload
            # running in the container. Stop the client before collecting data.
            docker.run("kill", client, timeout=10)
    record["process_seconds"] = time.monotonic() - started
    record["validation_errors"] = []
    for key, read in [
        ("generator", lambda: resources(docker, client)),
        ("server", lambda: stats(docker, target)),
        ("target_cpu_usec", lambda: resources(docker, target)["usage_usec"] - before["usage_usec"]),
    ]:
        if key == "generator" and record["timed_out"]:
            record[key] = None
            continue
        try:
            record[key] = read()
        except (subprocess.SubprocessError, ValueError, KeyError) as error:
            record[key] = None
            record["validation_errors"].append(f"Could not read {key}: {type(error).__name__}")
    try:
        raw = json.loads((output / (label + ".json")).read_text())
        read_summary(record, raw, args)
    except (OSError, ValueError, KeyError, TypeError) as error:
        record["validation_errors"].append(f"Native summary unavailable or incomplete: {error}")
    record["validation_errors"].extend(validate_target(record))
    for name in record.get("missing_metrics", []):
        record["validation_errors"].append(f"Missing k6 metric: {name}")
    if record["timed_out"]:
        record["validation_errors"].append("Timed out; snapshots cannot certify a completed trial.")
    record["target_validation_passed"] = not record["validation_errors"]
    return record


def cleanup(docker, created):
    failures = []
    for name in list(reversed(created)):
        try:
            docker.run("rm", "-f", name, timeout=15)
            created.remove(name)
        except subprocess.SubprocessError:
            failures.append(name)
    if failures:
        print("Cleanup could not remove these benchmark containers: " + ", ".join(failures), file=sys.stderr)
    return not failures


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--prepare", action="store_true", help="Build current checkout and target, download pinned k6, then exit without load")
    parser.add_argument("--output", type=Path, help="New result directory; existing directories are refused")
    parser.add_argument("--rate", type=int, default=200000)
    parser.add_argument("--seconds", type=int, default=60, help="Measured load interval, excluding warmup")
    parser.add_argument("--warmup", type=int, default=3)
    parser.add_argument("--concurrency", type=int, default=2048, help="SwarmGo load concurrency ceiling")
    parser.add_argument("--k6-vus", type=int, default=2048, help="k6 preAllocatedVUs and maxVUs; independent tuning parameter")
    parser.add_argument("--repeats", type=int, default=3, help="Pairs of runs; tool order alternates")
    parser.add_argument("--tools", nargs="+", choices=["swarmgo", "k6"], default=["swarmgo", "k6"], help="Use one tool for a setup or tuning check")
    parser.add_argument("--timeout", type=float, help="Per-client wall time limit in seconds; default warmup + seconds + 35")
    args = parser.parse_args()
    if args.prepare:
        prepare(Docker())
        return 0
    if args.output is None:
        parser.error("--output is required unless --prepare is used")
    if any(n < 1 or n > 1000000 for n in [args.rate, args.concurrency, args.k6_vus]) or args.repeats < 1:
        parser.error("rate/concurrency/VUs must be between 1 and 1000000; repeats must be positive")
    if args.seconds < 1 or args.warmup < 0 or args.seconds + args.warmup + 2 > 3600:
        parser.error("seconds must be positive, warmup nonnegative, and the whole scenario at most 1h")
    if args.timeout is not None and (not math.isfinite(args.timeout) or args.timeout <= 0):
        parser.error("timeout must be finite and positive")
    output = args.output.expanduser().resolve()
    if output.exists():
        parser.error("--output already exists; choose a new directory")
    build = json.loads((ROOT / "build.json").read_text())
    if build["product_source_sha256"] != product_hash():
        raise RuntimeError("Product source changed since preparation; run --prepare again.")
    for path, expected in build["sha256"].items():
        if sha256(ROOT / path) != expected:
            raise RuntimeError(f"Prepared file changed: {path}; run --prepare again.")
    if (ROOT / "body.json").read_bytes() != BODY:
        raise RuntimeError("Request body must be exactly the target's 1024-byte JSON payload.")
    docker = Docker()
    arch = {"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "amd64": "amd64"}.get(docker.info["Architecture"])
    if arch != build["goarch"]:
        raise RuntimeError("Docker architecture changed since preparation; run --prepare again.")
    docker.run("image", "inspect", IMAGE)
    output.mkdir(parents=True, exist_ok=False)
    workload = {k: v for k, v in vars(args).items() if k not in ["output", "prepare"]}
    manifest = {
        "build": build, "image": IMAGE, "network_internal": True, "published_ports": [],
        "cpu_memory_limits": None, "workload": workload, "host_architecture": platform.machine(),
        "docker": {k: docker.info.get(k) for k in ["ServerVersion", "KernelVersion", "OperatingSystem", "Architecture", "NCPU", "MemTotal"]},
        "sha256": {name: sha256(ROOT / name) for name in ["bench.py", "prepare.py", "k6.js", "target.go", "body.json"]},
        "note": "Client and target share one local Docker VM without container CPU/RAM caps. Fresh containers per trial. Cold-start and native verdicts retained; excluding warmup does not turn an inconclusive whole test into a pass.",
    }
    results, created = [], []
    result_path = output / "results.json"

    def save():
        pending = output / "results.json.tmp"
        pending.write_text(json.dumps({"manifest": manifest, "results": results}, indent=2, allow_nan=False) + "\n")
        os.replace(pending, result_path)

    save()
    network = "swarmgo-arrival-" + uuid.uuid4().hex[:12]
    network_label = "org.swarmgo.arrival=" + network
    try:
        docker.run("network", "create", "--internal", "--label", network_label, network)
        for repetition in range(1, args.repeats + 1):
            order = args.tools if repetition % 2 else list(reversed(args.tools))
            for tool in order:
                try:
                    record = run_trial(docker, network, output, args, repetition, tool, created)
                    results.append(record)
                    save()
                    print(json.dumps({k: v for k, v in record.items() if k != "windows"}), flush=True)
                finally:
                    if not cleanup(docker, created):
                        raise RuntimeError("Container cleanup failed; stopping before the next trial.")
    finally:
        cleanup(docker, created)
        # Creation may succeed on the daemon before an interrupted CLI returns.
        # Find only our uniquely labelled network, including that uncertain case.
        owned = docker.run("network", "ls", "--filter", "label=" + network_label,
                           "--format", "{{.Name}}", timeout=15).splitlines()
        if network in owned:
            docker.run("network", "rm", network, timeout=15)
    # Native scenario verdicts remain authoritative even if the selected steady
    # interval delivered its load. A comparative record can legitimately exit 1.
    return 0 if all(r["native_success"] and r["target_validation_passed"] for r in results) else 1


if __name__ == "__main__":
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, interrupted)
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        print("Interrupted; cleanup was attempted for this run's containers and network.", file=sys.stderr)
        raise SystemExit(130)
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"Benchmark stopped: {error}", file=sys.stderr)
        raise SystemExit(2)
