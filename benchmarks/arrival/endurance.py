"""Bounded, local HTTP endurance comparison; preparation never sends load."""

from pathlib import Path
import argparse
import hashlib
import json
import os
import signal
import subprocess
import sys
import time
import uuid

from prepare import BODY, IMAGE, ROOT, Docker, download, prepare, product_hash, sha256
from bench import resources, stats

RATE = 200000
OHA_VERSION = "1.16.0"
OHA_URL = f"https://github.com/hatoo/oha/releases/download/v{OHA_VERSION}/oha-linux-arm64"
OHA_SHA256 = "99a790eb8c3e0feaca974bd6b32f0f8d4426a0c5b289f39e833e5b2c7529cd39"


def require_arm64(docker):
    if docker.info["Architecture"] not in ("aarch64", "arm64"):
        raise RuntimeError("This endurance comparison currently supports local Linux ARM64 Docker only.")


def prepare_endurance(docker):
    require_arm64(docker)
    prepare(docker)
    binary = download(OHA_URL, 64 * 1024 * 1024)
    if hashlib.sha256(binary).hexdigest() != OHA_SHA256:
        raise RuntimeError("Official oha v1.16.0 ARM64 executable checksum mismatch.")
    pending = ROOT / "bin" / "oha.tmp"
    pending.write_bytes(binary)
    pending.chmod(0o755)
    os.replace(pending, ROOT / "bin" / "oha")
    print("Prepared pinned oha v1.16.0 for Linux ARM64. No load was sent.")


def checked_build(docker):
    require_arm64(docker)
    build = json.loads((ROOT / "build.json").read_text())
    if build["product_source_sha256"] != product_hash():
        raise RuntimeError("Product source changed since preparation; run --prepare again.")
    for name in ["bin/swarmgo", "bin/target", "target.go", "body.json"]:
        if sha256(ROOT / name) != build["sha256"][name]:
            raise RuntimeError(f"Prepared file changed: {name}; run --prepare again.")
    if build["goarch"] != "arm64" or build["image"] != IMAGE:
        raise RuntimeError("Prepared architecture or image differs; run --prepare again.")
    if (ROOT / "body.json").read_bytes() != BODY:
        raise RuntimeError("Request body must match the target's exact 1024-byte JSON payload.")
    if sha256(ROOT / "bin" / "oha") != OHA_SHA256:
        raise RuntimeError("oha executable differs from the pinned official release; run --prepare again.")
    docker.run("image", "inspect", IMAGE)
    return build


def spawn(docker, container, command, log):
    with log.open("w") as stream:
        return subprocess.Popen(docker.prefix + ["exec", container] + command,
                                stdout=stream, stderr=subprocess.STDOUT)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--prepare", action="store_true", help="Prepare binaries and image, then exit without load")
    parser.add_argument("--tool", choices=["swarmgo", "oha"])
    parser.add_argument("--seconds", type=int, default=300)
    parser.add_argument("--concurrency", type=int, default=2048)
    parser.add_argument("--output", type=Path, help="New result directory; existing paths are refused")
    args = parser.parse_args()
    if not 1 <= args.seconds <= 300 or not 1 <= args.concurrency <= 20000:
        parser.error("seconds must be 1..300 and concurrency 1..20000")
    if not args.prepare and (args.tool is None or args.output is None):
        parser.error("--tool and --output are required unless --prepare is used")
    docker = Docker()
    if args.prepare:
        prepare_endurance(docker)
        return 0
    output = args.output.expanduser().resolve()
    if output.exists():
        parser.error("--output already exists; choose a new directory")
    build = checked_build(docker)
    output.mkdir(parents=True, exist_ok=False)
    network = "swarmgo-endurance-" + uuid.uuid4().hex[:12]
    label = "org.swarmgo.endurance=" + network
    client, target = network + "-client", network + "-target"
    created, processes, samples, errors = [], [], [], []
    proc = probe = None
    started = None
    result = {"exit_code": None, "probe_exit_code": None, "timed_out": False,
              "interrupted": False, "finished": False, "generator": None, "server": None}
    manifest = {
        "tool": args.tool, "seconds": args.seconds, "requested_rate": RATE,
        "planned_load": RATE * args.seconds, "concurrency": args.concurrency,
        "generator_memory_limit_bytes": 6 * 1024**3, "target_memory_limit_bytes": 512 * 1024**2,
        "extra_swap_bytes": 0, "cpu_limits": None, "image": IMAGE, "build": build,
        "oha": {"version": OHA_VERSION, "url": OHA_URL, "sha256": OHA_SHA256},
        "sha256": {name: sha256(ROOT / name) for name in ["endurance.py", "prepare.py", "bench.py", "target.go", "body.json", "bin/swarmgo", "bin/target", "bin/oha"]},
        "docker": {k: docker.info.get(k) for k in ["ServerVersion", "KernelVersion", "NCPU", "MemTotal", "Architecture"]},
        "internal_network": True, "published_ports": [], "sample_interval_seconds": 5,
        "timeout_seconds": args.seconds + 35,
        "note": "One local Docker VM; fresh containers; startup included, no discarded warmup. A 10/s GET probe starts approximately 1s before POST load and ends approximately 1s after. oha quiet retains per-request results but emits no native response/failure summary; target counters are not client completion counts.",
    }

    def save():
        pending = output / "results.json.tmp"
        pending.write_text(json.dumps({"manifest": manifest, "samples": samples,
                                       "result": result, "errors": errors}, indent=2, allow_nan=False) + "\n")
        os.replace(pending, output / "results.json")

    def read(label, action):
        try:
            return action()
        except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
            errors.append(f"{label}: {type(error).__name__}: {error}")
            return None

    def snapshot():
        return {"generator": read("generator resources", lambda: resources(docker, client)),
                "server": read("target counters", lambda: stats(docker, target))}

    def kill_client():
        # Terminating docker exec alone does not stop either process in the container.
        if client in created:
            read("stop generator and probe", lambda: docker.run("kill", client, timeout=10))

    save()
    try:
        docker.run("network", "create", "--internal", "--label", label, network)
        for name, memory, command in [(target, "512m", ["/bench/bin/target"]),
                                      (client, "6g", ["sleep", "infinity"])]:
            created.append(name)
            docker.run("run", "--pull=never", "-d", "--name", name, "--label", label,
                       "--network", network, "--memory", memory, "--memory-swap", memory,
                       "-v", f"{ROOT}:/bench:ro", "-v", f"{output}:/results", IMAGE, *command)
        deadline = time.monotonic() + 10
        while True:
            try:
                stats(docker, target)
                break
            except (ValueError, subprocess.SubprocessError):
                if time.monotonic() >= deadline:
                    raise RuntimeError("Target did not become ready.")
                time.sleep(.05)
        address = docker.run("inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", target)
        url = f"http://{address}:8080/work"
        if args.tool == "swarmgo":
            command = ["/bench/bin/swarmgo", "resilience", "-url", url, "-method", "POST",
                       "-body-file", "/bench/body.json", "-header", "Content-Type: application/json",
                       "-header", "Accept-Encoding: identity", "-rate", str(RATE), "-c", str(args.concurrency),
                       "-baseline", "1s", "-spike", f"{args.seconds}s", "-recovery", "1s",
                       "-recovery-window", "1s", "-probe-rate", "10", "-probe-c", "32",
                       "-max-p99", "250ms", "-request-timeout", "5s", "-max-start-delay", "50ms",
                       "-output", "/results/native.json"]
        else:
            common = ["/bench/bin/oha", "--no-tui", "--output-format", "quiet", "--http-version", "1.1",
                      "-w", "-t", "5s", "--latency-correction", "-H", "Accept-Encoding: identity"]
            manifest["probe_command"] = common + ["-c", "32", "-q", "10", "-z", f"{args.seconds+2}s",
                                                  "-o", "/results/probe.json", url]
            probe = spawn(docker, client, manifest["probe_command"], output / "probe.log")
            processes.append(probe)
            time.sleep(1)
            command = common + ["-o", "/results/native.json", "-m", "POST", "-D", "/bench/body.json",
                                "-T", "application/json", "-c", str(args.concurrency),
                                "-q", str(RATE), "-z", f"{args.seconds}s", url]
        manifest["command"] = command
        save()
        started = time.monotonic()
        proc = spawn(docker, client, command, output / "run.log")
        processes.append(proc)
        next_sample = started
        while proc.poll() is None:
            now = time.monotonic()
            if now - started > manifest["timeout_seconds"]:
                result["timed_out"] = True
                result.update(snapshot())
                kill_client()
                break
            if now >= next_sample:
                sample = {"process_seconds": now - started, **snapshot()}
                samples.append(sample)
                save()
                print(json.dumps({"tool": args.tool, **sample}), flush=True)
                next_sample = now + 5
            time.sleep(.2)
        result["process_seconds"] = time.monotonic() - started
        if not result["timed_out"]:
            result["exit_code"] = proc.poll()
            if probe is not None:
                if proc.returncode == 0:
                    probe.wait(timeout=10)
                else:
                    result.update(snapshot())
                    kill_client()
            result["finished"] = proc.returncode == 0 and (probe is None or probe.poll() == 0)
    except KeyboardInterrupt:
        result["interrupted"] = True
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.SubprocessError) as error:
        errors.append(f"Run stopped: {type(error).__name__}: {error}")
    finally:
        # One interruption initiates cleanup; subsequent signals must not strand the probe.
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        if started is not None and "process_seconds" not in result:
            result["process_seconds"] = time.monotonic() - started
        if client in created:
            final = snapshot()
            if final["generator"] is not None:
                result["generator"] = final["generator"]
            if final["server"] is not None:
                result["server"] = final["server"]
            if any(process.poll() is None for process in processes):
                kill_client()
            result["container_state"] = read("generator container state", lambda: json.loads(
                docker.run("inspect", "--format", "{{json .State}}", client, timeout=10)))
        for process in processes:
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                read("reap local exec process", lambda: process.wait(timeout=10))
        if proc is not None:
            result["exit_code"] = proc.returncode
        if probe is not None:
            result["probe_exit_code"] = probe.returncode
        if target in created:
            deadline = time.monotonic() + 6
            while True:
                final_server = read("final target counters", lambda: stats(docker, target))
                if final_server is not None:
                    result["server"] = final_server
                if final_server is None or final_server["active_requests"] == 0 or time.monotonic() >= deadline:
                    break
                time.sleep(.05)
        # memory.events survives an exec process OOM if the container's PID 1 survives.
        observations = [s["generator"] for s in samples if s["generator"] is not None]
        if result["generator"] is not None:
            observations.append(result["generator"])
        oom_killed = any(r.get("oom_kill", 0) > 0 for r in observations) or bool(
            (result.get("container_state") or {}).get("OOMKilled"))
        result["oom_kill_observed"] = oom_killed if observations or result.get("container_state") else None
        result["memory_peak_bytes_observed"] = max((r["memory_peak_bytes"] for r in observations), default=None)
        result["native"] = None
        native = output / "native.json"
        if native.exists() and native.stat().st_size:
            result["native"] = read("native JSON", lambda: json.loads(native.read_text()))
        server = result["server"]
        valid = (server is not None and server["invalid"] == server["active_requests"] == 0
                 and server["body_bytes"] % len(BODY) == 0 and server["requests"] >= server["body_bytes"] // len(BODY))
        result["target_payload_validation_passed"] = bool(valid)
        if valid:
            result["target_post_requests"] = server["body_bytes"] // len(BODY)
            result["target_get_requests"] = server["requests"] - result["target_post_requests"]
            if args.tool == "swarmgo" and result["native"] is not None:
                try:
                    load = result["native"]["load"]
                    ordinary = result["native"]["probe"]
                    consistent = (load["planned"] == manifest["planned_load"]
                                  and load["completed"] == result["target_post_requests"]
                                  and ordinary["completed"] == result["target_get_requests"])
                except (KeyError, TypeError):
                    consistent = False
                result["client_target_counts_match"] = consistent
                if not consistent:
                    errors.append("SwarmGo native counts differ from target counters or requested workload")
        for name in reversed(created):
            read("remove " + name, lambda name=name: docker.run("rm", "-f", name, timeout=15))
        # Label lookup covers an interrupted create whose daemon-side outcome is unknown.
        owned = read("find owned network", lambda: docker.run("network", "ls", "--filter", "label=" + label,
                                                               "--format", "{{.Name}}", timeout=15))
        if owned is not None and network in owned.splitlines():
            read("remove owned network", lambda: docker.run("network", "rm", network, timeout=15))
        save()
    if result["interrupted"]:
        return 130
    return 0 if result["finished"] and result["target_payload_validation_passed"] and not result["oom_kill_observed"] and not errors else 1


if __name__ == "__main__":
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, interrupted)
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        raise SystemExit(130)
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"Endurance benchmark stopped: {error}", file=sys.stderr)
        raise SystemExit(2)
