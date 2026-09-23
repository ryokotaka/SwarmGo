"""Local, bounded HTTP workload; whole-duration results include cold start."""
from pathlib import Path
import argparse, hashlib, json, subprocess, sys, time, uuid
ROOT=Path(__file__).resolve().parent
ARRIVAL=ROOT.parent/'SwarmGo/benchmarks/arrival'
sys.path.insert(0,str(ARRIVAL))
from prepare import Docker, IMAGE
from bench import resources
p=argparse.ArgumentParser()
p.add_argument('--tool',choices=['swarmgo','oha'],required=True)
p.add_argument('--seconds',type=int,default=300)
p.add_argument('--concurrency',type=int,default=2048)
p.add_argument('--out',required=True)
a=p.parse_args()
if not 1<=a.seconds<=300 or not 1<=a.concurrency<=20000 or Path(a.out).name!=a.out: p.error('invalid bounds')
D=Docker();out=ROOT/'results'/a.out;out.mkdir(exist_ok=False)
network='swarmgo-endurance-'+uuid.uuid4().hex[:10];client=network+'-client';target=network+'-target'
created=[];proc=None;probe=None;rows=[]
manifest={'tool':a.tool,'seconds':a.seconds,'requested_rate':200000,'planned_load':200000*a.seconds,'concurrency':a.concurrency,'generator_memory_limit_bytes':6*1024**3,'target_memory_limit_bytes':512*1024**2,'cpu_limits':None,'image':IMAGE,'swarmgo_source':'607ff795fd8f05436e5875656a5e8c3b406c6bdc','oha':json.loads((ROOT/'oha-source.json').read_text()),'sha256':{name:hashlib.sha256((ROOT/'bin'/name).read_bytes()).hexdigest() for name in ['oha','swarmgo-resilience','target']},'docker':{k:D.info.get(k) for k in ['ServerVersion','KernelVersion','NCPU','MemTotal','Architecture']},'internal_network':True,'published_ports':[],'note':'Same local Docker VM. One 10/s GET probe stream begins ~1s before POST load, ends ~1s after it. 6 GiB generator cap and 512 MiB target cap leave room for Docker; no extra swap. No CPU caps. Counts include startup; no warmup is discarded.'}
def save(extra=None):
 data={'manifest':manifest,'samples':rows}
 if extra:data.update(extra)
 (out/'results.json').write_text(json.dumps(data,indent=2)+'\n')
try:
 D.run('network','create','--internal',network)
 for name,memory,cmd in [(target,'512m',['/bench/bin/target']),(client,'6g',['sleep','infinity'])]:
  created.append(name)
  D.run('run','--pull=never','-d','--name',name,'--network',network,'--memory',memory,'--memory-swap',memory,'-v',str(ROOT)+':/bench:ro','-v',str(out)+':/results',IMAGE,*cmd)
 for _ in range(100):
  try:D.run('exec',target,'/bench/bin/target','stats');break
  except subprocess.SubprocessError:time.sleep(.05)
 address=D.run('inspect','--format','{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}',target)
 url=f'http://{address}:8080/work'
 if a.tool=='swarmgo':
  cmd=['/bench/bin/swarmgo-resilience','resilience','-url',url,'-method','POST','-body-file','/bench/body.json','-header','Content-Type: application/json','-header','Accept-Encoding: identity','-rate','200000','-c',str(a.concurrency),'-baseline','1s','-spike',f'{a.seconds}s','-recovery','1s','-recovery-window','1s','-probe-rate','10','-probe-c','32','-max-p99','250ms','-request-timeout','5s','-max-start-delay','50ms','-output','/results/native.json']
 else:
  common=['/bench/bin/oha','--no-tui','--output-format','quiet','--http-version','1.1','-w','-t','5s','--latency-correction','-H','Accept-Encoding: identity']
  probe_cmd=common+['-c','32','-q','10','-z',f'{a.seconds+2}s','-o','/results/probe.json',url]
  with (out/'probe.log').open('w') as f:probe=subprocess.Popen(D.prefix+['exec',client]+probe_cmd,stdout=f,stderr=subprocess.STDOUT)
  time.sleep(1)
  cmd=common+['-o','/results/native.json','-m','POST','-D','/bench/body.json','-T','application/json','-c',str(a.concurrency),'-q','200000','-z',f'{a.seconds}s',url]
 manifest['command']=cmd;save()
 started=time.monotonic();next_sample=started
 with (out/'run.log').open('w') as f:proc=subprocess.Popen(D.prefix+['exec',client]+cmd,stdout=f,stderr=subprocess.STDOUT)
 timed_out=False
 while proc.poll() is None:
  now=time.monotonic()
  if now-started>a.seconds+35:
   timed_out=True;D.run('kill',client);proc.wait(timeout=10);break
  if now>=next_sample:
   sample={'process_seconds':now-started,'generator':resources(D,client),'server':json.loads(D.run('exec',target,'/bench/bin/target','stats'))}
   rows.append(sample);save();print(json.dumps({'tool':a.tool,'seconds':round(now-started,1),'post_count':sample['server']['body_bytes']//1024,'memory_peak_mib':round(sample['generator']['memory_peak_bytes']/1024**2,1)}),flush=True)
   next_sample=now+5
  time.sleep(.2)
 elapsed=time.monotonic()-started
 if probe:
  if proc.returncode==0: probe.wait(timeout=10)
  else:
   # Release the local exec handle; finally removes the container and probe.
   probe.terminate()
   probe.wait(timeout=10)
 result={'exit_code':proc.returncode,'timed_out':timed_out,'process_seconds':elapsed,'generator':resources(D,client),'server':json.loads(D.run('exec',target,'/bench/bin/target','stats'))}
 native=out/'native.json'
 if native.exists() and native.stat().st_size:
  try:result['native']=json.loads(native.read_text())
  except ValueError:result['native_parse_error']=True
 save({'result':result});print(json.dumps({k:v for k,v in result.items() if k!='native'}),flush=True)
finally:
 for name in reversed(created):
  try:D.run('rm','-f',name)
  except subprocess.SubprocessError:pass
 if proc and proc.poll() is None:proc.terminate()
 if probe and probe.poll() is None:probe.terminate()
 D.run('network','rm',network)
