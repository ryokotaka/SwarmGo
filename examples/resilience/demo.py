#!/usr/bin/env python3
"""Run the before/after fixture entirely inside a local internal Docker network."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[2]
IMAGE = "debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251"


def command(*args, **kwargs):
    return subprocess.run(args, text=True, check=True, **kwargs)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=Path("resilience-results"))
    args = parser.parse_args()
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    for name in ("before.json", "after.json", "manifest.json"):
        if (out / name).exists():
            parser.error(f"refusing to overwrite {out / name}")
    contexts = json.loads(command("docker", "context", "inspect", capture_output=True).stdout)
    host = os.environ.get("DOCKER_HOST") or contexts[0]["Endpoints"]["docker"]["Host"]
    if not host.startswith("unix://"):
        parser.error("use a local Docker context; remote engines are not supported by this demo")
    info = json.loads(command("docker", "info", "--format", "{{json .}}", capture_output=True).stdout)
    arch = {"aarch64": "arm64", "x86_64": "amd64"}.get(info["Architecture"], info["Architecture"])
    network = "swarmgo-resilience-" + uuid.uuid4().hex[:10]
    containers = []
    created_network = False
    manifest = {
        "image": IMAGE, "architecture": arch, "docker_cpus": info["NCPU"],
        "docker_memory_bytes": info["MemTotal"], "network_internal": True,
        "published_ports": [], "runs": [],
        "revision": command("git", "rev-parse", "HEAD", cwd=ROOT, capture_output=True).stdout.strip(),
        "working_tree_modified": bool(command("git", "status", "--porcelain", cwd=ROOT, capture_output=True).stdout.strip()),
    }
    with tempfile.TemporaryDirectory(prefix=".build-", dir=out) as build:
        env = dict(os.environ, GOOS="linux", GOARCH=arch, CGO_ENABLED="0")
        command("go", "build", "-o", str(Path(build) / "swarmgo"), "./cmd/swarmgo", cwd=ROOT, env=env)
        command("go", "build", "-o", str(Path(build) / "api"), "./examples/resilience", cwd=ROOT, env=env)
        try:
            command("docker", "network", "create", "--internal", network, capture_output=True)
            created_network = True
            for label, limit in (("before", 0), ("after", 40)):
                target, client = network + "-api", network + "-client"
                command("docker", "run", "-d", "--name", target, "--network", network,
                        "-v", build + ":/bin/demo:ro", IMAGE, "/bin/demo/api", "-listen", ":8080", "-limit", str(limit), capture_output=True)
                containers.append(target)
                for attempt in range(50):
                    ready = subprocess.run(["docker", "exec", target, "/bin/demo/api", "-check"], capture_output=True)
                    if ready.returncode == 0:
                        break
                    time.sleep(.1)
                else:
                    raise RuntimeError("local API did not become ready")
                command("docker", "run", "-d", "--name", client, "--network", network,
                        "-v", build + ":/bin/demo:ro", "-v", str(out) + ":/results", IMAGE, "sleep", "infinity", capture_output=True)
                containers.append(client)
                cmd = ["docker", "exec", client, "/bin/demo/swarmgo", "resilience", "-url", f"http://{target}:8080/work",
                       "-rate", "200", "-c", "512", "-header", "X-Test-Client: bulk", "-probe-rate", "10", "-probe-c", "64",
                       "-probe-header", "X-Test-Client: ordinary", "-baseline", "2s", "-spike", "3s", "-recovery", "7s",
                       "-recovery-window", "2s", "-max-p99", "250ms", "-output", f"/results/{label}.json"]
                with (out / (label + ".log")).open("w") as log:
                    result = subprocess.run(cmd, text=True, stdout=log, stderr=subprocess.STDOUT, timeout=30)
                if not (out / (label + ".json")).exists():
                    raise RuntimeError(f"{label} did not produce a report; inspect its log")
                report = json.loads((out / (label + ".json")).read_text())
                manifest["runs"].append({"label": label, "admission_limit_per_client": limit, "exit_code": result.returncode, "status": report["status"]})
                (out / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
                print((out / (label + ".log")).read_text(), flush=True)
                for name in (client, target):
                    command("docker", "rm", "-f", name, capture_output=True)
                    containers.remove(name)
            if [r["status"] for r in manifest["runs"]] != ["fail", "pass"]:
                raise SystemExit("Demo did not show the expected fail/pass pair; all reports were retained.")
        finally:
            for name in containers:
                subprocess.run(["docker", "rm", "-f", name], capture_output=True)
            if created_network:
                subprocess.run(["docker", "network", "rm", network], capture_output=True)


if __name__ == "__main__":
    main()
