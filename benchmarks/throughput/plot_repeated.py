"""Render the six-run comparison. All values come from repeated/summary.json."""
import argparse
import json
from pathlib import Path
import math
import numpy as np
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
from matplotlib import font_manager
from matplotlib.colors import to_rgba
ROOT=Path(__file__).resolve().parent
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--png-dir',type=Path)
a=p.parse_args()
d=json.loads((ROOT/'repeated/summary.json').read_text())
tools=['wrk','swarmgo','oha','k6'];names=['wrk','SwarmGo','oha','k6']
for font in (ROOT/'fonts').glob('*.ttf'):
    font_manager.fontManager.addfont(font)
plt.rcParams.update({'font.family':'IBM Plex Sans','svg.fonttype':'path','svg.hashsalt':'swarmgo-repeat-six','font.size':11})
# Technical-report styling: neutral greys, one accent for SwarmGo, descriptive
# titles and the measurement conditions stated on the figure itself.
palettes={
    'light':dict(background='#ffffff',ink='#1f2328',muted='#59636e',grid='#e6e9ed',axis='#8c959f',
                 accent='#2f6fae',neutral='#b3bac2',point='#57606a'),
    'dark':dict(background='#0d1117',ink='#e6edf3',muted='#9198a1',grid='#21262d',axis='#484f58',
                accent='#4f8fd1',neutral='#4a525c',point='#b1bac4'),
}
values={t:[r['target_rps']/1000 for r in d['results'] if r['tool']==t] for t in tools}
assert all(len(v)==6 for v in values.values())
max_x=math.ceil(max(max(v) for v in values.values())/100)*100

def style(ax):
    ax.set_facecolor(background)
    ax.tick_params(axis='both',length=0,pad=7,labelcolor=muted,labelsize=10.5)
    ax.grid(axis='x',color=palette['grid'],linewidth=.8,zorder=0)
    ax.set_axisbelow(True)
    for name,spine in ax.spines.items():
        spine.set_visible(name=='bottom')
        spine.set_color(palette['axis']);spine.set_linewidth(.8)
    ax.set_yticks(range(len(tools)),names,fontsize=11.5,color=ink)
    ax.get_yticklabels()[1].set_fontweight('medium')
    ax.set_ylim(len(tools)-.4,-.6)
    ax.set_xlim(0,max_x)
    ticks=np.arange(0,max_x+1,100)
    ax.set_xticks(ticks,[f'{int(x)}k' if x else '0' for x in ticks])

def heading(fig,title,subtitle):
    fig.text(.03,.93,title,fontsize=13,fontweight='medium',color=ink)
    fig.text(.03,.865,subtitle,fontsize=10,color=muted)

def save(fig,name):
    if theme=='dark':
        name=name.replace('.svg','-dark.svg')
    out=ROOT.parents[1]/'assets'/name
    fig.savefig(out,facecolor=background,metadata={'Date':None})
    out.write_text('\n'.join(line.rstrip() for line in out.read_text().splitlines())+'\n')
    if a.png_dir:
        a.png_dir.mkdir(parents=True,exist_ok=True)
        fig.savefig(a.png_dir/Path(name).with_suffix('.png'),dpi=160,facecolor=background)
    plt.close(fig)

conditions='Apple M4, local Docker · HTTP/1.1, 1 KiB POST and response · no rate cap'
for theme,palette in palettes.items():
    background=palette['background'];ink=palette['ink'];muted=palette['muted']
    colors={t:palette['accent'] if t=='swarmgo' else palette['neutral'] for t in tools}
    fig,ax=plt.subplots(figsize=(8.4,3.9),facecolor=background)
    heading(fig,'HTTP POST throughput by load generator',conditions)
    for y,t in enumerate(tools):
        v=values[t];median=float(np.median(v))
        ax.barh(y,median,height=.56,color=colors[t],zorder=3)
        ax.hlines(y,min(v),max(v),color=ink,linewidth=1,zorder=4)
        ax.vlines([min(v),max(v)],y-.09,y+.09,color=ink,linewidth=1,zorder=4)
        ax.text(1.015,y,f'{median:,.0f}k',transform=ax.get_yaxis_transform(),ha='left',va='center',fontsize=11.5,
                fontweight='medium' if t=='swarmgo' else 'normal',color=ink,clip_on=False)
    style(ax)
    ax.text(1.015,-.62,'Median',transform=ax.get_yaxis_transform(),ha='left',va='bottom',fontsize=9.5,color=muted,clip_on=False)
    ax.set_xlabel('POSTs per second, validated at the target',fontsize=10,color=muted,labelpad=8)
    fig.text(.03,.045,'Bars: median of six 60-second runs per tool. Whiskers: observed minimum and maximum. '
             'Concurrency: 256 connections; k6 64 VUs.',fontsize=9,color=muted)
    fig.subplots_adjust(left=.12,right=.87,bottom=.27,top=.78)
    save(fig,'throughput-repeated.svg')

    fig,ax=plt.subplots(figsize=(8.4,4.9),facecolor=background)
    heading(fig,'Run-to-run variation, all 24 observations',conditions)
    style(ax)
    ax.set_ylim(len(tools)-.1,-.55)
    ax.set_xlabel('POSTs per second, validated at the target',fontsize=10,color=muted,labelpad=8)
    fig.text(.03,.04,'Boxes: middle 50% with median. Whiskers: minimum and maximum. '
             'Dots: one 60-second run each; filled = first three, hollow = next three.',fontsize=9,color=muted)
    fig.subplots_adjust(left=.12,right=.97,bottom=.21,top=.8)
    # Space points in display units, keeping their measured horizontal positions.
    width_pt=fig.get_figwidth()*72*ax.get_position().width
    height_pt=fig.get_figheight()*72*ax.get_position().height
    lane_step=7.5*(ax.get_ylim()[0]-ax.get_ylim()[1])/height_pt
    for y,t in enumerate(tools):
        v=values[t]
        edge=palette['accent'] if t=='swarmgo' else palette['point']
        ax.boxplot([v],positions=[y-.16],orientation='horizontal',widths=.22,whis=(0,100),showfliers=False,patch_artist=True,manage_ticks=False,
            boxprops={'facecolor':to_rgba(colors[t],.35),'edgecolor':ink,'linewidth':.9},medianprops={'color':ink,'linewidth':1.6},
            whiskerprops={'color':ink,'linewidth':.9},capprops={'color':ink,'linewidth':.9})
        rows=[r for r in d['results'] if r['tool']==t]
        lanes=[]
        for r in rows:
            x=r['target_rps']/1000
            x_pt=x/max_x*width_pt
            lane=next((i for i,points in enumerate(lanes) if all(abs(x_pt-other)>=7.5 for other in points)),len(lanes))
            if lane==len(lanes):lanes.append([])
            lanes[lane].append(x_pt)
            ax.scatter(x,y+.13+lane*lane_step,s=24,facecolors=edge if r['batch']%2==1 else background,edgecolors=edge,linewidths=1,zorder=5)
    save(fig,'throughput-distribution.svg')
