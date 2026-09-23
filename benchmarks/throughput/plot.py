"""Plot recorded target-side interval rates; requires matplotlib."""
import argparse
import json
from pathlib import Path

import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

ROOT = Path(__file__).resolve().parent
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--png', type=Path)
a = p.parse_args()
plt.rcParams.update({'font.family': 'DejaVu Sans', 'svg.fonttype': 'none', 'font.size': 11})
fig, ax = plt.subplots(figsize=(8, 3.5), facecolor='white')
colors = {'swarmgo': '#00856a', 'wrk': '#3b5b92', 'oha': '#d48125', 'k6': '#858b94'}
for tool in ['wrk', 'swarmgo', 'oha', 'k6']:
    data = json.loads((ROOT/f'recorded/confirm-{tool}/results.json').read_text())
    record = data['results'][0]
    samples = record['samples']
    edges = [0]+[s['measurement_seconds'] for s in samples]
    rates = [s['target_rps'] for s in samples]
    ax.stairs(rates, edges, baseline=None, color=colors[tool], linewidth=2.2,
              label='SwarmGo' if tool == 'swarmgo' else tool)
    if not record['completed_observation']:
        ax.plot(edges[-1], rates[-1], 'x', color=colors[tool], markersize=8)
ax.set_xlim(0, 61)
ax.set_ylim(0, 650_000)
ax.set_xticks([0, 15, 30, 45, 60])
ax.set_yticks([0, 200_000, 400_000, 600_000], ['0', '200k', '400k', '600k'])
ax.set_xlabel('Time (seconds)', color='#57606a', labelpad=10)
ax.set_ylabel('RPS', color='#57606a', labelpad=12)
ax.tick_params(colors='#57606a', length=0, pad=7)
ax.grid(axis='y', color='#e5e7eb', linewidth=.8)
ax.set_axisbelow(True)
for spine in ax.spines.values():
    spine.set_visible(False)
ax.legend(frameon=False, loc='lower left', bbox_to_anchor=(0, 1.03), ncol=4)
fig.subplots_adjust(left=.12, right=.96, bottom=.21, top=.80)
svg = ROOT.parents[1]/'assets/throughput.svg'
fig.savefig(svg, facecolor='white')
svg.write_text('\n'.join(line.rstrip() for line in svg.read_text().splitlines())+'\n')
if a.png:
    a.png.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(a.png, dpi=150, facecolor='white')
