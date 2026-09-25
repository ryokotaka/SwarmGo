"""Render the README architecture diagram in the same style as the benchmark figures."""
import argparse
from pathlib import Path
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
from matplotlib import font_manager
from matplotlib.patches import FancyArrowPatch, FancyBboxPatch

ASSETS = Path(__file__).resolve().parent
FONTS = ASSETS.parent / 'benchmarks/throughput/fonts'
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--png-dir', type=Path)
a = p.parse_args()
for font in FONTS.glob('*.ttf'):
    font_manager.fontManager.addfont(font)
plt.rcParams.update({'font.family': 'IBM Plex Sans', 'svg.fonttype': 'path', 'svg.hashsalt': 'swarmgo-architecture'})

palettes = {
    'light': dict(background='#ffffff', ink='#1f2328', muted='#59636e', box='#f6f8fa', edge='#8c959f',
                  shadow='#d0d7de', accent='#2f6fae', accent_fill='#eaf1f8'),
    'dark': dict(background='#0d1117', ink='#e6edf3', muted='#9198a1', box='#161b22', edge='#6e7681',
                 shadow='#30363d', accent='#4f8fd1', accent_fill='#12243a'),
}
W, H = 100, 42  # data units; the figure keeps this aspect ratio


def box(ax, x, y, w, h, fill, edge, lw=1.0, z=2):
    ax.add_patch(FancyBboxPatch((x, y), w, h, boxstyle='round,pad=0,rounding_size=1.1',
                                facecolor=fill, edgecolor=edge, linewidth=lw, zorder=z))


def label(ax, x, y, title, detail=None, ha='center'):
    ax.text(x, y + (1.3 if detail else 0), title, ha=ha, va='center', fontsize=10, fontweight='medium', color=ink, zorder=5)
    if detail:
        ax.text(x, y - 1.6, detail, ha=ha, va='center', fontsize=8.5, color=muted, zorder=5)


def arrow(ax, start, end, color, lw=1.0, style='-|>', z=4):
    ax.add_patch(FancyArrowPatch(start, end, arrowstyle=style, mutation_scale=9, color=color, linewidth=lw,
                                 shrinkA=0, shrinkB=0, zorder=z))


for theme, palette in palettes.items():
    ink, muted = palette['ink'], palette['muted']
    fig = plt.figure(figsize=(8.4, 8.4 * H / W), facecolor=palette['background'])
    ax = fig.add_axes([0, 0, 1, 1])
    ax.set_xlim(0, W); ax.set_ylim(0, H); ax.axis('off')

    ax.text(3, 38.6, 'How a SwarmGo run is wired', fontsize=13, fontweight='medium', color=ink)
    ax.text(3, 35.3, 'Control travels over gRPC. Load goes from each worker straight to the target.', fontsize=10, color=muted)

    # Controller
    box(ax, 3, 11, 12, 17, palette['box'], palette['edge'])
    label(ax, 9, 22.5, 'Controller', 'master or run')
    ax.text(9, 16.2, 'live dashboard', ha='center', va='center', fontsize=8.8, color=muted)
    ax.text(9, 13.9, 'JSON report', ha='center', va='center', fontsize=8.8, color=muted)

    # Workers: two offset outlines behind the expanded one stand for "any number".
    for k in (2, 1):
        box(ax, 33 + k * 0.8, 4 + k * 0.8, 45, 26, palette['background'], palette['shadow'], z=1)
    box(ax, 33, 4, 45, 26, palette['background'], palette['accent'], lw=1.2)
    ax.text(35, 27.6, 'Worker × N', fontsize=10.5, fontweight='medium', color=palette['accent'], va='center')
    ax.text(76, 27.6, 'one machine or many', fontsize=8.8, color=muted, va='center', ha='right')

    col = {'a': (35, 19.5), 'b': (57, 19.5)}
    row = {'send': 16.2, 'back': 6.2}
    stages = [
        ('a', 'send', 'Goroutine pool', 'prepared request bytes'),
        ('b', 'send', 'Keep-alive connections', 'one per goroutine'),
        ('b', 'back', 'Header parse', 'in place, fasthttp fallback'),
        ('a', 'back', 'Latency histograms', 'HDR, one per goroutine'),
    ]
    for c, r, title, detail in stages:
        x, w = col[c]
        box(ax, x, row[r], w, 7.4, palette['box'], palette['edge'])
        label(ax, x + w / 2, row[r] + 3.7, title, detail)
    arrow(ax, (54.5, 19.9), (57, 19.9), palette['edge'])
    arrow(ax, (66.75, 16.2), (66.75, 13.6), palette['edge'])
    arrow(ax, (57, 9.9), (54.5, 9.9), palette['edge'])

    # Target API and the HTTP load path
    box(ax, 86, 15.2, 11, 9.4, palette['accent_fill'], palette['accent'], lw=1.2)
    label(ax, 91.5, 19.9, 'Target API')
    arrow(ax, (76.5, 19.9), (86, 19.9), palette['accent'], lw=2.2, style='<|-|>')
    ax.text(81.9, 21.6, 'HTTP/1.1', ha='center', va='bottom', fontsize=9, fontweight='medium', color=palette['accent'])

    # gRPC control path
    arrow(ax, (15, 19.5), (33, 19.5), palette['edge'], style='<|-|>')
    ax.text(24, 22.9, 'one gRPC stream', ha='center', fontsize=9, fontweight='medium', color=ink)
    ax.text(24, 20.6, 'worker dials :50051', ha='center', fontsize=8.5, color=muted)
    ax.text(24, 17.4, 'Start · Stop · Quit →', ha='center', va='top', fontsize=8.5, color=muted)
    ax.text(24, 15.1, '← Register · Stats · Finish', ha='center', va='top', fontsize=8.5, color=muted)

    name = 'architecture.svg' if theme == 'light' else 'architecture-dark.svg'
    out = ASSETS / name
    fig.savefig(out, facecolor=palette['background'], metadata={'Date': None})
    out.write_text('\n'.join(line.rstrip() for line in out.read_text().splitlines()) + '\n')
    if a.png_dir:
        a.png_dir.mkdir(parents=True, exist_ok=True)
        fig.savefig(a.png_dir / Path(name).with_suffix('.png'), dpi=160, facecolor=palette['background'])
    plt.close(fig)
