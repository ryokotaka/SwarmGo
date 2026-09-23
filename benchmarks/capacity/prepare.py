"""Build the measured SwarmGo revision and fetch the free k6 CLI."""

from pathlib import Path
import hashlib, io, json, os, subprocess, tarfile, urllib.request

ROOT = Path(__file__).resolve().parent
REVISION = "468ba43cb8151e2916f8333faec4556a3a7d78e5"
VERSION = "2.3.0"


def run(*args, **kwargs):
    return subprocess.check_output(list(args), text=True, **kwargs).strip()


info = json.loads(run("docker", "info", "--format", "{{json .}}"))
if info["Architecture"] not in ["aarch64", "arm64"] or info["NCPU"] < 6:
    raise SystemExit(
        "This recorded comparison requires ARM64 Docker with at least 6 CPUs."
    )
if not run("go", "version").split()[2] == "go1.25.7":
    raise SystemExit("Use Go 1.25.7 to reproduce the recorded SwarmGo build.")
binary = ROOT / "bin"
binary.mkdir(exist_ok=True)
source = ROOT / "src/SwarmGo"
source.parent.mkdir(exist_ok=True)
if not source.exists():
    subprocess.run(
        ["git", "clone", "https://github.com/ryokotaka/SwarmGo.git", str(source)],
        check=True,
    )
    subprocess.run(["git", "checkout", "--detach", REVISION], cwd=source, check=True)
elif run("git", "rev-parse", "HEAD", cwd=source) != REVISION or run(
    "git", "status", "--porcelain", cwd=source
):
    raise SystemExit(
        "The existing source directory differs from the recorded revision; use a new folder."
    )
env = {**os.environ, "GOOS": "linux", "GOARCH": "arm64", "CGO_ENABLED": "0"}
subprocess.run(
    ["go", "build", "-o", str(binary / "swarmgo"), "./cmd/swarmgo"],
    cwd=source,
    env=env,
    check=True,
)
subprocess.run(
    ["go", "build", "-o", str(binary / "target"), str(ROOT / "target.go")],
    env=env,
    check=True,
)

base = f"https://github.com/grafana/k6/releases/download/v{VERSION}/"
name = f"k6-v{VERSION}-linux-arm64.tar.gz"
checks = urllib.request.urlopen(base + f"k6-v{VERSION}-checksums.txt").read().decode()
expected = next(line.split()[0] for line in checks.splitlines() if line.endswith(name))
archive = urllib.request.urlopen(base + name).read()
if hashlib.sha256(archive).hexdigest() != expected:
    raise SystemExit("Archive checksum mismatch.")
with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as tar:
    entry = tar.getmember(f"k6-v{VERSION}-linux-arm64/k6")
    data = tar.extractfile(entry).read()
recorded = json.loads((ROOT / "results.json").read_text())["manifest"]["binary_sha256"][
    "k6"
]
if hashlib.sha256(data).hexdigest() != recorded:
    raise SystemExit("k6 binary differs from the recorded benchmark.")
(binary / "k6").write_bytes(data)
(binary / "k6").chmod(0o755)
print("Prepared SwarmGo, target server, and k6. No cloud account is used.")
