from pathlib import Path
import argparse, subprocess, json, time, os, hashlib

ROOT = Path(__file__).resolve().parent
parser = argparse.ArgumentParser()
parser.add_argument("--concurrency", type=int, required=True)
parser.add_argument("--delay-ms", type=int, default=0)
parser.add_argument("--seconds", type=int, default=35)
parser.add_argument("--threads", type=int, default=8)
parser.add_argument("--out", required=True)
args = parser.parse_args()
prefix = "swarmgo-wrk-" + str(os.getpid())
target = prefix + "-target"
client = prefix + "-client"
created = []
out = ROOT / "results" / args.out
out.mkdir(parents=True, exist_ok=False)
image = "debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251"


def docker(*x):
    return subprocess.check_output(["docker", *x], text=True).strip()


def stats():
    return json.loads(docker("exec", target, "/bench/bin/target", "stats"))


def resources(name):
    lines = docker(
        "exec",
        name,
        "sh",
        "-c",
        "cat /sys/fs/cgroup/memory.peak /sys/fs/cgroup/cpu.stat /sys/fs/cgroup/memory.events",
    ).splitlines()
    return {
        "memory_peak_bytes": int(lines[0]),
        **{k: int(v) for k, v in [x.split() for x in lines[1:]]},
    }


try:
    docker("network", "create", "--internal", prefix)
    for name, img, cmd in [
        (target, image, ["/bench/bin/target"]),
        (client, "swarmgo-wrk-local:4.2.0", ["sleep", "infinity"]),
    ]:
        docker(
            "run",
            "-d",
            "--name",
            name,
            "--network",
            prefix,
            "-v",
            str(ROOT) + ":/bench",
            img,
            *cmd,
        )
        created.append(name)
    for _ in range(30):
        try:
            stats()
            break
        except subprocess.CalledProcessError:
            time.sleep(0.1)
    docker("exec", target, "/bench/bin/target", "reset")
    before = resources(target)
    cmd = [
        "docker",
        "exec",
        "-e",
        f"SUMMARY=/bench/results/{args.out}/wrk.json",
        client,
        "wrk",
        f"-t{args.threads}",
        f"-c{args.concurrency}",
        f"-d{args.seconds}s",
        "--timeout",
        "30s",
        "-s",
        "/bench/wrk.lua",
        f"http://{target}:8080/work?delay_ms={args.delay_ms}",
    ]
    with (out / "wrk.log").open("w") as log:
        result = subprocess.run(
            cmd, stdout=log, stderr=subprocess.STDOUT, timeout=args.seconds + 60
        )
    gen = resources(client)
    for _ in range(50):
        if stats()["active_requests"] == 0:
            break
        time.sleep(0.1)
    observed = stats()
    after = resources(target)
    summary = json.loads((out / "wrk.json").read_text())
    d = {
        "tool": "wrk",
        "version": "4.2.0",
        "commit": "a211dd5a7050b1f9e8a9870b95513060e72ac4a0",
        "args": vars(args),
        "exit_code": result.returncode,
        "generator": gen,
        "target_cpu_usec": after["usage_usec"] - before["usage_usec"],
        "server": observed,
        "summary": summary,
        "client_rps": summary["requests"] / (summary["duration_usec"] / 1e6),
        "server_rps": observed["requests"] / observed["elapsed_seconds"],
        "container_image": docker(
            "image", "inspect", "swarmgo-wrk-local:4.2.0", "--format", "{{.Id}}"
        ),
        "binary_sha256": docker(
            "exec", client, "sha256sum", "/usr/local/bin/wrk"
        ).split()[0],
    }
    (out / "results.json").write_text(json.dumps(d, indent=2) + "\n")
    print(json.dumps(d), flush=True)
    assert (
        result.returncode == 0
        and observed["invalid"] == 0
        and not any(summary["errors"].values())
    )
finally:
    for n in created:
        subprocess.run(["docker", "rm", "-f", n], capture_output=True)
    subprocess.run(["docker", "network", "rm", prefix], capture_output=True)
