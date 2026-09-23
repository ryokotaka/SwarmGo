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
background, foreground, muted = "#0b1220", "#edf4fc", "#bfccdc"
fig = plt.figure(figsize=(12, 5.9), facecolor=background)
ax = fig.add_axes([.10, .27, .84, .48], facecolor=background)
ax.set_xlim(0, 360)
ax.set_ylim(0, 250_000)
ax.set_xticks([0, 60, 120, 180, 240, 300, 360])
ax.set_yticks([0, 50_000, 100_000, 150_000, 200_000], ["0", "50k", "100k", "150k", "200k"])
ax.tick_params(colors=muted, length=0, pad=8)
ax.grid(color="#263449", linewidth=.7)
ax.set_axisbelow(True)
for spine in ax.spines.values():
    spine.set_visible(False)
ax.set_xlabel("Observed duration (seconds)", color=muted, labelpad=12)
ax.set_ylabel("POSTs received / second", color=muted, labelpad=14)

for tool, color, marker, label in [
    ("oha", "#ffb574", "X", "oha 1.16.0 · quiet"),
    ("swarmgo", "#75debd", "o", "SwarmGo"),
]:
    result = json.loads((ROOT / "recorded-endurance" / tool / "results.json").read_text())["result"]
    server = result["server"]
    assert server["invalid"] == 0 and server["active_requests"] == 0
    assert server["body_bytes"] % 1024 == 0
    duration = server["elapsed_seconds"]
    rate = (server["body_bytes"] // 1024) / duration
    ax.scatter(duration, rate, s=180, marker=marker, color=color, edgecolors=background, linewidths=1.5, zorder=5)
    ax.vlines(duration, 0, rate, color=color, linewidth=1, linestyles=(0, (3, 5)), alpha=.3)
    ax.annotate(label, (duration, rate), xytext=(0, 27), textcoords="offset points",
                ha="center", color=color, fontsize=14, weight="bold")
    ax.annotate(f"{rate:,.0f}/s · {duration:.0f} s", (duration, rate), xytext=(0, -29),
                textcoords="offset points", ha="center", color=foreground, fontsize=12)
    status = "Stopped: 6 GiB memory limit" if tool == "oha" else "Completed 5-minute schedule"
    ax.annotate(status, (duration, rate), xytext=(0, -49), textcoords="offset points",
                ha="center", color=muted, fontsize=9.5)
    print(f"{tool}: duration={duration:.9f}s, target POST rate={rate:.6f}/s")

fig.text(.06, .92, "High request rates, sustained for longer.", color=foreground, fontsize=23, weight="bold")
fig.text(.06, .855, "Higher and farther right means more load, for longer. Both tools were asked for 200k POSTs/s for 5 minutes.", color=muted, fontsize=11)
fig.text(.06, .115, "Same 6 GiB generator budget · 1 KiB POST · Apple M4 / local Docker · one recorded trial per tool", color=muted, fontsize=10)
fig.text(.06, .074, "RPS = target-validated POST count / observed time, including the ordinary-request probe before and after the load.", color=muted, fontsize=9)
fig.text(.06, .035, "SwarmGo reached the scheduled end; 5 minutes is the tested duration, not its endurance limit. oha stopped with kernel OOM.", color=muted, fontsize=9)

svg = ROOT.parents[1] / "assets/endurance-xy.svg"
fig.savefig(svg, facecolor=background)
svg.write_text("\n".join(line.rstrip() for line in svg.read_text().splitlines()) + "\n")
if args.png:
    args.png.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(args.png, dpi=150, facecolor=background)
