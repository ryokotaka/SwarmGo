"""Plot the saved local demo. Requires matplotlib; the demo itself does not."""

import argparse
import json
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt


def main():
    here = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--results", type=Path, default=here / "results")
    parser.add_argument("--output", type=Path, default=here.parents[1] / "assets/resilience.svg")
    parser.add_argument("--png", type=Path)
    args = parser.parse_args()
    runs = [json.loads((args.results / f"{name}.json").read_text()) for name in ("before", "after")]
    before, after = runs
    settings = before["settings"]
    if after["settings"] != settings:
        raise ValueError("The two runs must use the same settings")
    if any(run["load"]["missed"] or run["probe"]["missed"] for run in runs):
        raise ValueError("Cannot use an incomplete load schedule for this comparison")
    start = settings["baseline_seconds"]
    end = start + settings["spike_seconds"]
    duration = end + settings["recovery_seconds"]
    worst = [max(w["latency_p99_us"] for w in run["probe"]["windows"]
                 if start <= w["index"] < end) for run in runs]

    plt.rcParams.update({"font.family": "DejaVu Sans", "font.size": 11, "svg.hashsalt": "swarmgo-resilience"})
    fig = plt.figure(figsize=(11.6, 6.5), facecolor="#f8fafc")
    ink, muted = "#132238", "#526178"
    colors = ["#b54f28", "#007c91"]
    fig.text(.075, .925, "Do ordinary requests stay fast?", fontsize=24, weight="bold", color=ink)
    fig.text(.075, .873, "Same API. Same load. With and without admission control.", fontsize=12, color=muted)
    fig.text(.075, .777, f"{worst[0] / 1e6:.2f} s", fontsize=30, weight="bold", color=colors[0])
    fig.text(.33, .777, f"{worst[1] / 1e3:.0f} ms", fontsize=30, weight="bold", color=colors[1])
    fig.text(.075, .733, "Before", color=colors[0])
    fig.text(.33, .733, "With admission control", color=colors[1])
    fig.text(.66, .797, "Ordinary traffic during the spike", fontsize=11, color=ink)
    fig.text(.66, .759, "Worst one-second p99", fontsize=11, color=muted)

    ax = fig.add_axes([.09, .22, .85, .43], facecolor="#f8fafc")
    ax.axvspan(start, end, color="#e9dfd5", alpha=.55, zorder=0)
    for run, label, color in zip(runs, ["Before", "With admission control"], colors):
        windows = run["probe"]["windows"]
        ax.plot([w["index"] + w["seconds"] / 2 for w in windows],
                [w["latency_p99_us"] / 1e6 for w in windows],
                color=color, linewidth=2.8, marker="o", markersize=4, label=label)
    limit = settings["max_p99_us"] / 1e6
    ymax = max(limit * 2, max(w["latency_p99_us"] for run in runs for w in run["probe"]["windows"]) / 1e6 * 1.14)
    ax.axhline(limit, color=muted, linewidth=1, linestyle=(0, (4, 4)))
    ax.text(duration * .98, limit + ymax * .022, f"{limit * 1000:g} ms limit", ha="right", color=muted, fontsize=10)
    ax.set(xlim=(0, duration), ylim=(0, ymax),
           xlabel="Planned request time (s)", ylabel="Ordinary-request p99 (s)")
    ax.text((start + end) / 2, ymax * .94, "Load spike", ha="center", color=muted, fontsize=10)
    ax.grid(axis="y", color="#dce3ec", linewidth=.7)
    ax.set_axisbelow(True)
    for spine in ("top", "right"):
        ax.spines[spine].set_visible(False)
    for spine in ("left", "bottom"):
        ax.spines[spine].set_color("#b9c5d3")
    ax.tick_params(colors=muted, length=0, pad=7)
    ax.xaxis.label.set_color(muted)
    ax.yaxis.label.set_color(muted)
    ax.legend(loc="upper right", frameon=False, fontsize=10)
    count = before["load"]["planned"]
    fig.text(.075, .086, f"Local demo  ·  {settings['load_rate']} load req/s + {settings['probe_rate']} ordinary req/s  ·  {count}/{count} load attempts in both runs", fontsize=10, color=ink)
    fig.text(.075, .049, f"Zero missed starts. {settings['probe_rate']} ordinary samples per second. API defense comparison, not a generator speed benchmark.", fontsize=9, color=muted)
    fig.savefig(args.output, metadata={"Date": None})
    if args.output.suffix.lower() == ".svg":
        args.output.write_text("\n".join(line.rstrip() for line in args.output.read_text().splitlines()) + "\n")
    if args.png:
        fig.savefig(args.png, dpi=140)
    plt.close(fig)


if __name__ == "__main__":
    main()
