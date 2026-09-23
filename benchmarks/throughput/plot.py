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
plt.rcParams.update({'font.family': 'DejaVu Sans', 'svg.fonttype': 'path',
                     'svg.hashsalt': 'swarmgo-throughput', 'font.size': 14})
fig, ax = plt.subplots(figsize=(8, 4), facecolor='white')
colors = {'swarmgo': '#008caa', 'wrk': '#46566a', 'oha': '#b07a36', 'k6': '#949caa'}
for tool in ['wrk', 'swarmgo', 'oha', 'k6']:
    data = json.loads((ROOT/f'recorded/confirm-{tool}/results.json').read_text())
    record = data['results'][0]
    samples = record['samples']
    edges = [0]+[s['measurement_seconds'] for s in samples]
    rates = [s['target_rps'] for s in samples]
    name = 'SwarmGo' if tool == 'swarmgo' else tool
    ax.stairs(rates, edges, baseline=None, color=colors[tool],
              linewidth=3 if tool == 'swarmgo' else 2,
              label=f'{name}  {record["target_rps"]/1000:.0f}k/s')
    if not record['completed_observation']:
        ax.plot(edges[-1], rates[-1], 'x', color=colors[tool], markersize=8)
ax.set_xlim(0, 61)
ax.set_ylim(0, 650_000)
ax.set_xticks([0, 15, 30, 45, 60])
ax.set_yticks([0, 200_000, 400_000, 600_000], ['0', '200k', '400k', '600k'])
ax.set_xlabel('Time (seconds)', color='#57606a', labelpad=10)
ax.set_ylabel('POSTs / second', color='#57606a', labelpad=12)
ax.tick_params(colors='#57606a', length=0, pad=7)
ax.grid(axis='y', color='#e5e7eb', linewidth=.8)
ax.set_axisbelow(True)
for spine in ax.spines.values():
    spine.set_visible(False)
legend = ax.legend(frameon=False, loc='lower left', bbox_to_anchor=(-.01, 1.05),
                   ncol=2, fontsize=14, handlelength=1.5, columnspacing=1.4)
for label in legend.get_texts():
    if label.get_text().startswith('SwarmGo'):
        label.set_fontweight('bold')
fig.subplots_adjust(left=.14, right=.98, bottom=.20, top=.71)
svg = ROOT.parents[1]/'assets/throughput.svg'
fig.savefig(svg, facecolor='white', metadata={'Date': None})
svg.write_text('\n'.join(line.rstrip() for line in svg.read_text().splitlines())+'\n')
if a.png:
    a.png.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(a.png, dpi=150, facecolor='white')

# A short comparison for the README opening; the full time series stays above.
fig, ax = plt.subplots(figsize=(10, 3.9), facecolor='white')
tools = ['wrk', 'swarmgo', 'oha', 'k6']
rates = [json.loads((ROOT/f'recorded/confirm-{tool}/results.json').read_text())
         ['results'][0]['target_rps'] for tool in tools]
fig.text(.04, .90, 'HTTP POST throughput', fontsize=23, fontweight='bold', color='#162932')
fig.text(.04, .81, '60-second average · no rate cap', fontsize=15, color='#576973')
for i, (tool, rate) in enumerate(zip(tools, rates)):
    color = '#008caa' if tool == 'swarmgo' else '#a8b6bf'
    ax.barh(i, rate, height=.48, color=color, zorder=3)
    ax.text(rate+12000, i, f'{rate/1000:.0f}k', va='center', fontsize=17,
            fontweight='bold' if tool == 'swarmgo' else 'normal',
            color='#00718a' if tool == 'swarmgo' else '#354c59')
ax.set_yticks(range(4), ['wrk', 'SwarmGo', 'oha', 'k6'])
for label in ax.get_yticklabels():
    if label.get_text() == 'SwarmGo':
        label.set_fontweight('bold')
        label.set_color('#00718a')
ax.set_ylim(3.6, -.6)
ax.set_xlim(0, 650000)
ax.set_xticks([0, 200000, 400000, 600000], ['0', '200k', '400k', '600k'])
ax.tick_params(axis='both', length=0, pad=10, labelsize=16, color='#576973')
ax.tick_params(axis='x', labelsize=13, labelcolor='#576973')
ax.grid(axis='x', color='#e6ecef', linewidth=.8, zorder=0)
for spine in ax.spines.values():
    spine.set_visible(False)
fig.text(.04, .045, 'Apple M4 · local Docker · 1 KiB request + response', fontsize=12, color='#576973')
fig.text(.96, .045, 'POSTs / second', ha='right', fontsize=12, color='#576973')
fig.subplots_adjust(left=.16, right=.96, bottom=.20, top=.74)
overview = ROOT.parents[1]/'assets/throughput-summary.svg'
fig.savefig(overview, facecolor='white', metadata={'Date': None})
overview.write_text('\n'.join(line.rstrip() for line in overview.read_text().splitlines())+'\n')
