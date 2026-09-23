"""Render the six-run comparison. All values come from repeated/summary.json."""
import argparse
import json
from pathlib import Path
import math
import numpy as np
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
ROOT=Path(__file__).resolve().parent
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--png-dir',type=Path)
a=p.parse_args()
d=json.loads((ROOT/'repeated/summary.json').read_text())
tools=['wrk','swarmgo','oha','k6'];names=['wrk','SwarmGo','oha','k6']
colors={t:('#008caa' if t=='swarmgo' else '#7c8d99') for t in tools}
ink='#243d49';muted='#576973'
plt.rcParams.update({'font.family':'DejaVu Sans','svg.fonttype':'path','svg.hashsalt':'swarmgo-repeat-six','font.size':13})
values={t:[r['target_rps']/1000 for r in d['results'] if r['tool']==t] for t in tools}
assert all(len(v)==6 for v in values.values())
max_x=math.ceil(max(max(v) for v in values.values())/100)*100

def style(ax):
    ax.tick_params(axis='both',length=0,pad=9,labelcolor=muted)
    ax.grid(axis='x',color='#e6ecef',linewidth=.8,zorder=0)
    ax.set_axisbelow(True)
    for spine in ax.spines.values():spine.set_visible(False)
    ax.set_yticks(range(len(tools)),names,fontsize=17)
    ax.get_yticklabels()[1].set_fontweight('bold');ax.get_yticklabels()[1].set_color('#00718a')
    ax.set_ylim(len(tools)-.35,-.65)

def save(fig,name):
    out=ROOT.parents[1]/'assets'/name
    fig.savefig(out,facecolor='white',metadata={'Date':None})
    out.write_text('\n'.join(line.rstrip() for line in out.read_text().splitlines())+'\n')
    if a.png_dir:
        a.png_dir.mkdir(parents=True,exist_ok=True)
        fig.savefig(a.png_dir/Path(name).with_suffix('.png'),dpi=160,facecolor='white')
    plt.close(fig)

fig,ax=plt.subplots(figsize=(8.4,4.3),facecolor='white')
fig.text(.04,.91,'HTTP POST throughput',fontsize=21,fontweight='bold',color='#162932')
fig.text(.04,.82,'Six 60-second runs per tool · no rate cap',fontsize=14,color=muted)
for y,t in enumerate(tools):
    v=values[t];median=float(np.median(v))
    ax.barh(y,median,height=.48,color=colors[t],zorder=3)
    ax.hlines(y,min(v),max(v),color=ink,linewidth=1.5,zorder=4)
    ax.vlines([min(v),max(v)],y-.10,y+.10,color=ink,linewidth=1.5,zorder=4)
    ax.text(1.19,y,f'{median:.0f}k',transform=ax.get_yaxis_transform(),ha='right',va='center',fontsize=21,fontweight='bold' if t=='swarmgo' else 'normal',color='#00718a' if t=='swarmgo' else ink,clip_on=False)
style(ax)
ax.set_xlim(0,max_x)
ticks=np.arange(0,max_x+1,200)
ax.set_xticks(ticks,[f'{int(x)}k' if x else '0' for x in ticks],fontsize=14)
fig.text(.04,.055,'Median with min–max range',fontsize=14,color=muted)
fig.text(.97,.055,'POSTs/s',ha='right',fontsize=14,color=muted)
fig.subplots_adjust(left=.19,right=.84,bottom=.23,top=.75)
save(fig,'throughput-repeated.svg')

fig,ax=plt.subplots(figsize=(8.4,5.3),facecolor='white')
fig.text(.04,.91,'Run-to-run variation',fontsize=21,fontweight='bold',color='#162932')
fig.text(.04,.83,'Six 60-second runs per tool · all 24 points',fontsize=14,color=muted)
style(ax)
ax.set_ylim(len(tools)-.05,-.55)
ax.set_xlim(0,max_x)
ticks=np.arange(0,max_x+1,200)
ax.set_xticks(ticks,[f'{int(x)}k' if x else '0' for x in ticks],fontsize=14)
fig.text(.97,.045,'POSTs/s',ha='right',fontsize=14,color=muted)
fig.subplots_adjust(left=.19,right=.97,bottom=.18,top=.76)
# Space points in display units, keeping their measured horizontal positions.
width_pt=fig.get_figwidth()*72*ax.get_position().width
height_pt=fig.get_figheight()*72*ax.get_position().height
lane_step=7.5*(ax.get_ylim()[0]-ax.get_ylim()[1])/height_pt
for y,t in enumerate(tools):
    v=values[t]
    ax.boxplot([v],positions=[y-.16],vert=False,widths=.22,whis=(0,100),showfliers=False,patch_artist=True,manage_ticks=False,
        boxprops={'facecolor':colors[t],'alpha':.35,'edgecolor':ink},medianprops={'color':ink,'linewidth':2},
        whiskerprops={'color':ink},capprops={'color':ink})
    rows=[r for r in d['results'] if r['tool']==t]
    lanes=[]
    for r in rows:
        x=r['target_rps']/1000
        x_pt=x/max_x*width_pt
        lane=next((i for i,points in enumerate(lanes) if all(abs(x_pt-other)>=7.5 for other in points)),len(lanes))
        if lane==len(lanes):lanes.append([])
        lanes[lane].append(x_pt)
        ax.scatter(x,y+.13+lane*lane_step,s=28,facecolors=colors[t] if r['batch']%2==1 else 'white',edgecolors='#00718a' if t=='swarmgo' else '#536b78',linewidths=1.1,zorder=5)
save(fig,'throughput-distribution.svg')
