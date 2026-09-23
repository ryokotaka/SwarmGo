"""Plot target-observed POST rate against duration. Requires matplotlib."""

import argparse
import json
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt

ROOT = Path(__file__).resolve().parent
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--png", type=Path, help="Optional PNG output path")
args = parser.parse_args()

plt.rcParams.update({"font.family": "DejaVu Sans", "svg.fonttype": "none", "font.size": 11})
background, muted = "#ffffff", "#57606a"
fig = plt.figure(figsize=(8, 3.5), facecolor=background)
ax = fig.add_axes([.12, .21, .83, .73], facecolor=background)
ax.set_xlim(0, 6)
ax.set_ylim(0, 245_000)
ax.set_xticks([0, 1, 2, 3, 4, 5, 6])
ax.set_yticks([0, 100_000, 200_000], ["0", "100k", "200k"])
ax.tick_params(colors=muted, length=0, pad=8)
ax.grid(axis="y", color="#e5e7eb", linewidth=.8)
ax.set_axisbelow(True)
for spine in ax.spines.values():
    spine.set_visible(False)
ax.set_xlabel("Duration (min)", color=muted, labelpad=10)
ax.set_ylabel("RPS", color=muted, labelpad=12)

for tool, color, marker, label in [
    ("oha", "#6e7781", "X", "oha"),
    ("swarmgo", "#00856a", "o", "SwarmGo"),
]:
    result = json.loads((ROOT / "recorded-endurance" / tool / "results.json").read_text())["result"]
    server = result["server"]
    assert server["invalid"] == 0 and server["active_requests"] == 0
    assert server["body_bytes"] % 1024 == 0
    duration = server["elapsed_seconds"]
    rate = (server["body_bytes"] // 1024) / duration
    x = duration / 60
    ax.scatter(x, rate, s=140, marker=marker, color=color, edgecolors=background, linewidths=1.3, zorder=5)
    ax.vlines(x, 0, rate, color=color, linewidth=1, linestyles=(0, (3, 5)), alpha=.25)
    ax.annotate(label, (x, rate), xytext=(0, 16), textcoords="offset points",
                ha="center", color=color, fontsize=15, weight="bold")
    status = "Stopped at 2.8 min" if tool == "oha" else "5 min completed"
    ax.annotate(status, (x, rate), xytext=(0, -25), textcoords="offset points",
                ha="center", color=color, fontsize=11)
    print(f"{tool}: duration={duration:.9f}s, target POST rate={rate:.6f}/s")

svg = ROOT.parents[1] / "assets/endurance-xy.svg"
fig.savefig(svg, facecolor=background)
svg.write_text("\n".join(line.rstrip() for line in svg.read_text().splitlines()) + "\n")
if args.png:
    args.png.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(args.png, dpi=150, facecolor=background)
