"""Prepare local benchmark files; importing this module performs no actions."""

from pathlib import Path
import hashlib
import io
import json
import os
import re
import subprocess
import tarfile
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parent
REPO = ROOT.parents[1]
IMAGE = "debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251"
GO_VERSION = "go1.25.7"
K6_VERSION = "2.3.0"
BODY = ('{"data":"' + "x" * 1013 + '"}').encode("ascii")
K6_ARM64_SHA256 = "c458464bcc5e9f6e4ae9b6985286ff337cfb139a64dfe2c731a1f5d551cd601f"
MAX_DOWNLOAD = 256 * 1024 * 1024


def command(args, *, timeout=30, cwd=None, env=None):
    return subprocess.check_output(
        args, text=True, timeout=timeout, cwd=cwd, env=env
    ).strip()


def sha256(path):
    h = hashlib.sha256()
    with Path(path).open("rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def product_hash():
    """Hash build inputs without recording private paths or source contents."""
    files = [REPO / "go.mod", REPO / "go.sum"]
    for folder in ["cmd", "internal", "proto"]:
        files.extend(p for p in (REPO / folder).rglob("*.go") if not p.name.endswith("_test.go"))
    h = hashlib.sha256()
    for path in sorted(files):
        h.update(path.relative_to(REPO).as_posix().encode() + b"\0")
        h.update(bytes.fromhex(sha256(path)))
    return h.hexdigest()


class Docker:
    """Pin every command to an inspected local socket context."""

    def __init__(self):
        context = command(["docker", "context", "show"])
        inspected = json.loads(command(["docker", "context", "inspect", context]))
        endpoint = inspected[0]["Endpoints"]["docker"]["Host"]
        if not endpoint.startswith(("unix://", "npipe://")):
            raise RuntimeError("Use a local Docker socket context; TCP/SSH/remote contexts are refused.")
        if os.environ.get("DOCKER_HOST") not in (None, "", endpoint):
            raise RuntimeError("DOCKER_HOST overrides the selected context; unset it and select a local context.")
        self.prefix = ["docker", "--context", context]
        self.info = json.loads(self.run("info", "--format", "{{json .}}"))

    def run(self, *args, timeout=30):
        return command(self.prefix + list(args), timeout=timeout)


def download(url, limit):
    request = urllib.request.Request(url, headers={"User-Agent": "SwarmGo-arrival-benchmark"})
    with urllib.request.urlopen(request, timeout=60) as response:
        if not response.geturl().startswith("https://"):
            raise RuntimeError("Release download redirected away from HTTPS.")
        data = response.read(limit + 1)
    if len(data) > limit:
        raise RuntimeError("Release download exceeds the size limit.")
    return data


def prepare(docker):
    arch = {"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "amd64": "amd64"}.get(docker.info["Architecture"])
    if arch is None:
        raise RuntimeError("This harness supports Linux ARM64 and AMD64 Docker engines.")
    env = {**os.environ, "GOOS": "linux", "GOARCH": arch, "CGO_ENABLED": "0", "GOTOOLCHAIN": "local"}
    if env.get("GOFLAGS") or env.get("GOEXPERIMENT"):
        raise RuntimeError("Unset GOFLAGS and GOEXPERIMENT to use the recorded build settings.")
    version = command(["go", "version"], cwd=REPO, env=env)
    if version.split()[2] != GO_VERSION:
        raise RuntimeError(f"Use Go {GO_VERSION.removeprefix('go')} for this comparison.")
    source_hash = product_hash()
    source_commit = command(["git", "rev-parse", "HEAD"], cwd=REPO)
    dirty = bool(command(["git", "status", "--porcelain", "--untracked-files=normal"], cwd=REPO))
    base = f"https://github.com/grafana/k6/releases/download/v{K6_VERSION}/"
    archive_name = f"k6-v{K6_VERSION}-linux-{arch}.tar.gz"
    checks = download(base + f"k6-v{K6_VERSION}-checksums.txt", 1024 * 1024).decode("utf-8")
    matches = []
    for line in checks.splitlines():
        fields = line.split()
        if len(fields) == 2 and fields[1].lstrip("*") == archive_name:
            matches.append(fields[0].lower())
    if len(matches) != 1 or re.fullmatch(r"[0-9a-f]{64}", matches[0]) is None:
        raise RuntimeError("Release checksum file has no unique valid archive checksum.")
    archive = download(base + archive_name, MAX_DOWNLOAD)
    if hashlib.sha256(archive).hexdigest() != matches[0]:
        raise RuntimeError("k6 release archive checksum mismatch.")
    expected_member = f"k6-v{K6_VERSION}-linux-{arch}/k6"
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as tar:
        entries = [entry for entry in tar.getmembers() if entry.name == expected_member]
        if len(entries) != 1 or not entries[0].isfile() or not 0 < entries[0].size <= MAX_DOWNLOAD:
            raise RuntimeError("Archive does not contain one regular k6 executable.")
        # No extract()/extractall(): archive paths and links never reach disk.
        with tar.extractfile(entries[0]) as f:
            binary = f.read(MAX_DOWNLOAD + 1)
        if len(binary) != entries[0].size:
            raise RuntimeError("Invalid k6 executable size.")
    if arch == "arm64" and hashlib.sha256(binary).hexdigest() != K6_ARM64_SHA256:
        raise RuntimeError("k6 ARM64 binary differs from the recorded official v2.3.0 binary.")
    with tempfile.TemporaryDirectory(prefix=".prepare-", dir=ROOT) as temp:
        staging = Path(temp)
        (staging / "k6").write_bytes(binary)
        (staging / "k6").chmod(0o755)
        for name, source in [("swarmgo", "./cmd/swarmgo"), ("target", str(ROOT / "target.go"))]:
            subprocess.run(["go", "build", "-o", str(staging / name), source], cwd=REPO, env=env, check=True, timeout=600)
        if product_hash() != source_hash:
            raise RuntimeError("Product source changed during preparation; run --prepare again.")
        build_info = {}
        for name in ["swarmgo", "target", "k6"]:
            text = command(["go", "version", "-m", str(staging / name)], cwd=REPO, env=env)
            build_info[name] = text.split(": ", 1)[-1]
        manifest = {
            "source_commit": source_commit, "source_dirty": dirty,
            "product_source_sha256": source_hash, "go_version": version,
            "go_build_info": build_info, "goarch": arch, "k6_version": K6_VERSION,
            "k6_archive_url": base + archive_name, "k6_archive_sha256": matches[0],
            "image": IMAGE,
            "sha256": {f"bin/{name}": sha256(staging / name) for name in ["swarmgo", "target", "k6"]},
        }
        manifest["sha256"].update({"target.go": sha256(ROOT / "target.go"), "body.json": hashlib.sha256(BODY).hexdigest()})
        (ROOT / "bin").mkdir(exist_ok=True)
        for name in ["swarmgo", "target", "k6"]:
            os.replace(staging / name, ROOT / "bin" / name)
        (ROOT / "body.json").write_bytes(BODY)
        (ROOT / "build.json").write_text(json.dumps(manifest, indent=2) + "\n")
    # Bench runs use --pull=never; all external fetching is confined to preparation.
    docker.run("pull", IMAGE, timeout=600)
    print("Prepared the current checkout, target, pinned k6, and image. No load was sent.")


if __name__ == "__main__":
    prepare(Docker())
