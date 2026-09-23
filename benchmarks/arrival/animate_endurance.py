"""Replay the recorded samples at 25x speed; requires matplotlib and Pillow."""
from pathlib import Path
import json
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
from matplotlib.animation import FuncAnimation, PillowWriter
import numpy as np

ROOT=Path(__file__).resolve().parent
plt.rcParams.update({'font.family':'DejaVu Sans','font.size':11})
fig=plt.figure(figsize=(10.4,5),facecolor='#0b1220')
ax=fig.add_axes([.09,.28,.55,.47]);ax.set_facecolor('#0b1220')
ax.set_xlim(0,305);ax.set_ylim(0,225)
ax.set_xticks([0,60,120,180,240,300]);ax.set_yticks([0,100,200],['0','100k/s','200k/s'])
ax.tick_params(colors='#bccbdd',length=0,pad=7);ax.grid(axis='y',color='#26374b')
for spine in ax.spines.values():spine.set_visible(False)
ax.set_xlabel('Elapsed seconds',color='#bccbdd',labelpad=8)
ax.set_title('POST rate at target · 5-second samples',loc='left',color='#bccbdd',fontsize=11,pad=12)
fig.text(.06,.90,'199,000 POSTs/s, sustained for five minutes.',color='#f0f6fc',fontsize=21,weight='bold')
fig.text(.06,.84,'SwarmGo and oha started at the same requested rate: 200,000 POSTs/s.',color='#bccbdd',fontsize=12)
fig.text(.06,.105,'Apple M4 / local Docker · 1 KiB POST · 6 GiB limit per generator · one trial each',color='#bccbdd',fontsize=10)
fig.text(.06,.065,'SwarmGo: 0 HTTP failures, 0.48% missed starts. oha quiet: no client success report.',color='#bccbdd',fontsize=10)
fig.text(.06,.025,'Replay of recorded samples at 25x speed. This is not a screen recording.',color='#7d93ab',fontsize=10)
colors={'swarmgo':'#75debd','oha':'#ffb574'}
records={};lines={};labels={}
clock=fig.text(.69,.73,'',color='#f0f6fc',fontsize=20,weight='bold')
for tool,y in [('swarmgo',.60),('oha',.38)]:
 d=json.loads((ROOT/f'recorded-endurance/{tool}/results.json').read_text());r=d['result'];samples=d['samples']
 times=np.array([s['process_seconds'] for s in samples]+[r['process_seconds']])
 counts=np.array([s['server']['body_bytes']/1024/1e6 for s in samples]+[r['server']['body_bytes']/1024/1e6])
 memory=np.array([s['generator']['memory_peak_bytes']/1024**2 for s in samples]+[r['generator']['memory_peak_bytes']/1024**2])
 records[tool]=(times,counts,memory)
 lines[tool],=ax.plot([],[],color=colors[tool],lw=2.8)
 fig.text(.69,y,'SwarmGo' if tool=='swarmgo' else 'oha 1.16.0 · quiet',color=colors[tool],fontsize=14,weight='bold')
 labels[tool]=fig.text(.69,y-.035,'',color='#f0f6fc',fontsize=11,linespacing=1.6,va='top')
stop,=ax.plot([],[],'x',color=colors['oha'],ms=10,mew=2.5)
frames=[305.0]*10+list(np.linspace(0,305,62))+[305.0]*15

def update(t):
 clock.set_text(f'{int(min(t,302))//60}:{int(min(t,302))%60:02d} elapsed')
 for tool in ['swarmgo','oha']:
  times,counts,memory=records[tool];j=np.searchsorted(times,t,side='right')
  rates=np.diff(counts)*1000/np.diff(times)
  lines[tool].set_data(times[1:j],rates[:max(0,j-1)])
  n=0 if j==0 else counts[j-1];m=0 if j==0 else memory[j-1]
  if tool=='oha' and t>=times[-1]:status='Stopped: memory limit'
  elif tool=='swarmgo' and t>=times[-1]:status='Five-minute load finished'
  else:status='Running' if t>0 else 'Starting'
  mem=f'{m/1024:.1f} GiB' if m>=1024 else f'{m:.0f} MiB'
  labels[tool].set_text(f'{n:.1f}m POST bodies received\n{mem} peak memory\n{status}')
 times,counts,_=records['oha']
 final_rate=(counts[-1]-counts[-2])*1000/(times[-1]-times[-2])
 stop.set_data([times[-1]],[final_rate]) if t>=times[-1] else stop.set_data([],[])
 return []

animation=FuncAnimation(fig,update,frames=frames,interval=200,blit=False)
animation.save(ROOT.parents[1]/'assets/endurance.gif',writer=PillowWriter(fps=5),dpi=100)
