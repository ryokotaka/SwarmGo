"""Plot the saved local demo. Requires matplotlib; the demo itself does not."""

import argparse
import json
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib import font_manager


def main():
    here = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--results", type=Path, default=here / "results")
    parser.add_argument("--output", type=Path, default=here.parents[1] / "assets/resilience.svg")
    parser.add_argument("--dark-output", type=Path, default=here.parents[1] / "assets/resilience-dark.svg")
    parser.add_argument("--png", type=Path, help="Also write a PNG of the light figure")
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

    for font in (here.parents[1] / "benchmarks/throughput/fonts").glob("IBMPlexSans-*.ttf"):
        font_manager.fontManager.addfont(font)
    plt.rcParams.update({"font.family": "IBM Plex Sans", "font.size": 10.5, "svg.fonttype": "path",
                         "svg.hashsalt": "swarmgo-resilience"})
    palettes = {
        "light": dict(background="#ffffff", ink="#1f2328", muted="#59636e", grid="#e6e9ed", axis="#8c959f",
                      band="#f1f3f5", lines=["#b35900", "#2f6fae"]),
        "dark": dict(background="#0d1117", ink="#e6edf3", muted="#9198a1", grid="#21262d", axis="#484f58",
                     band="#161b22", lines=["#e08a3c", "#4f8fd1"]),
    }
    labels = ["Without admission control", "With admission control"]
    for output, palette in ((args.output, palettes["light"]), (args.dark_output, palettes["dark"])):
        ink, muted, background = palette["ink"], palette["muted"], palette["background"]
        fig = plt.figure(figsize=(8.4, 4.6), facecolor=background)
        fig.text(.03, .93, "Ordinary-request latency during a load spike", fontsize=13, weight="medium", color=ink)
        fig.text(.03, .87, f"Same API and load schedule, with and without admission control \u00b7 "
                 f"worst one-second p99 during the spike: {worst[0] / 1e6:.2f} s \u2192 {worst[1] / 1e3:.0f} ms",
                 fontsize=10, color=muted)
        ax = fig.add_axes([.085, .24, .885, .54], facecolor=background)
        ax.axvspan(start, end, color=palette["band"], zorder=0)
        limit = settings["max_p99_us"] / 1e6
        ymax = max(limit * 2, max(w["latency_p99_us"] for run in runs for w in run["probe"]["windows"]) / 1e6 * 1.18)
        ax.text((start + end) / 2, ymax * .955, f"Load spike, {settings['load_rate']} req/s", ha="center", va="top",
                color=muted, fontsize=9.5)
        for run, label, color in zip(runs, labels, palette["lines"]):
            windows = run["probe"]["windows"]
            ax.plot([w["index"] + w["seconds"] / 2 for w in windows],
                    [w["latency_p99_us"] / 1e6 for w in windows],
                    color=color, linewidth=1.8, marker="o", markersize=3.2, label=label, zorder=3)
        ax.axhline(limit, color=palette["axis"], linewidth=.9, linestyle=(0, (4, 3)), zorder=2)
        ax.text(duration * .99, limit + ymax * .02, f"{limit * 1000:g} ms limit", ha="right", color=muted, fontsize=9.5)
        ax.set(xlim=(0, duration), ylim=(0, ymax))
        ax.set_xlabel("Planned request time (s)", color=muted, fontsize=10, labelpad=6)
        ax.set_ylabel("Ordinary-request p99 (s)", color=muted, fontsize=10, labelpad=6)
        ax.grid(axis="y", color=palette["grid"], linewidth=.8)
        ax.set_axisbelow(True)
        for name, spine in ax.spines.items():
            spine.set_visible(name == "bottom")
            spine.set_color(palette["axis"])
            spine.set_linewidth(.8)
        ax.tick_params(colors=muted, length=0, pad=6, labelsize=10)
        legend = ax.legend(loc="upper right", frameon=False, fontsize=9.5)
        for text in legend.get_texts():
            text.set_color(ink)
        count = before["load"]["planned"]
        fig.text(.03, .075, f"Local demo: {settings['load_rate']} load req/s and {settings['probe_rate']} ordinary req/s. "
                 f"{count}/{count} load attempts sent in both runs, zero missed starts.", fontsize=9, color=muted)
        fig.text(.03, .035, "Compares the API's defense with and without admission control, not generator speed.",
                 fontsize=9, color=muted)
        fig.savefig(output, facecolor=background, metadata={"Date": None})
        if output.suffix.lower() == ".svg":
            output.write_text("\n".join(line.rstrip() for line in output.read_text().splitlines()) + "\n")
        if args.png and output == args.output:
            fig.savefig(args.png, dpi=160, facecolor=background)
        plt.close(fig)


if __name__ == "__main__":
    main()
