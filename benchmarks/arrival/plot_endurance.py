"""Plot the recorded five-minute comparison. Requires matplotlib."""
from pathlib import Path
import json
import argparse
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
ROOT=Path(__file__).resolve().parent
DATA=ROOT/'recorded-endurance'
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--png', type=Path, help='Optional PNG output path')
args=parser.parse_args()
plt.rcParams.update({'font.family':'DejaVu Sans','svg.fonttype':'none','font.size':11})
fig,axs=plt.subplots(1,2,figsize=(12,5.0),facecolor='#0b1220')
colors={'swarmgo':'#75debd','oha':'#ffb574'}
for ax in axs:
 ax.set_facecolor('#0b1220');ax.tick_params(colors='#bfccdc',length=0,pad=7)
 ax.grid(axis='y',color='#273449',linewidth=.7);ax.set_axisbelow(True)
 for s in ax.spines.values():s.set_visible(False)
 ax.set_xlim(0,310);ax.set_xticks([0,60,120,180,240,300]);ax.set_xlabel('Elapsed seconds',color='#bfccdc',labelpad=10)
for tool in ['swarmgo','oha']:
 d=json.loads((DATA/tool/'results.json').read_text());r=d['result'];samples=d['samples']
 times=[s['process_seconds'] for s in samples]+[r['process_seconds']]
 counts=[s['server']['body_bytes']/1024/1e6 for s in samples]+[r['server']['body_bytes']/1024/1e6]
 mem=[s['generator']['memory_peak_bytes']/1024**3 for s in samples]+[r['generator']['memory_peak_bytes']/1024**3]
 label='SwarmGo' if tool=='swarmgo' else 'oha 1.16.0 (quiet)'
 axs[0].plot(times,counts,color=colors[tool],lw=2.7,label=label)
 axs[1].plot(times,mem,color=colors[tool],lw=2.7,label=label)
 if tool=='oha':
  for ax,y in [(axs[0],counts[-1]),(axs[1],mem[-1])]:ax.plot(times[-1],y,'x',color=colors[tool],ms=10,mew=2.5)
  axs[0].annotate('Stopped: memory limit',xy=(times[-1],counts[-1]),xytext=(98,44),color=colors[tool],fontsize=10,arrowprops={'arrowstyle':'-','color':colors[tool]})
  axs[1].annotate('6 GiB limit reached',xy=(times[-1],mem[-1]),xytext=(105,5.0),color=colors[tool],fontsize=10)
axs[0].set_title('POST bodies received by the target',loc='left',color='#edf4fc',pad=15,fontsize=12)
axs[0].set_ylim(0,65);axs[0].set_yticks([0,20,40,60],['0','20m','40m','60m'])
axs[0].legend(loc='upper left',frameon=False,labelcolor='#edf4fc',fontsize=10)
axs[1].set_title('Peak generator memory',loc='left',color='#edf4fc',pad=15,fontsize=12)
axs[1].set_ylim(0,6.6);axs[1].set_yticks([0,2,4,6],['0','2 GiB','4 GiB','6 GiB'])
axs[1].text(205,.43,'SwarmGo: 89.7 MiB',color=colors['swarmgo'],fontsize=10)
fig.text(.055,.93,'59.7 million successful POSTs in five minutes.',color='#edf4fc',fontsize=19,weight='bold')
fig.text(.055,.055,'200k requests/s requested · 1 KiB POST · local Apple M4 Docker VM · same 6 GiB generator limit · one trial per tool',color='#bfccdc',fontsize=9)
fig.text(.055,.022,'SwarmGo: 0 HTTP failures; 0.48% of planned starts missed. oha stopped with kernel OOM; its quiet mode has no final client report.',color='#bfccdc',fontsize=9)
fig.subplots_adjust(left=.065,right=.97,top=.78,bottom=.20,wspace=.19)
svg=ROOT.parents[1]/'assets/endurance.svg'
fig.savefig(svg,facecolor=fig.get_facecolor())
svg.write_text('\n'.join(line.rstrip() for line in svg.read_text().splitlines())+'\n')
if args.png:
 args.png.parent.mkdir(parents=True,exist_ok=True)
 fig.savefig(args.png,dpi=140,facecolor=fig.get_facecolor())
