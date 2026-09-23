from pathlib import Path
import subprocess,json,time,os,hashlib,argparse

ROOT=Path(__file__).resolve().parent
IMAGE='debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251'
PREFIX='swarmgo-perf-'+str(os.getpid())
def docker(*args):
 return subprocess.check_output(['docker',*args],text=True).strip()
def resources(name):
 text=docker('exec',name,'sh','-c','cat /sys/fs/cgroup/memory.peak /sys/fs/cgroup/cpu.stat /sys/fs/cgroup/memory.events')
 lines=text.splitlines();return {'memory_peak_bytes':int(lines[0]),**{k:int(v) for k,v in (x.split() for x in lines[1:])}}
def create(name,cores,cpus,memory,command):
 docker('run','-d','--name',name,'--network',PREFIX,'-v',str(ROOT)+':/bench','-e','K6_NO_USAGE_REPORT=true',IMAGE,*command)
def stats(target):return json.loads(docker('exec',target,'/bench/bin/target','stats'))

parser=argparse.ArgumentParser();parser.add_argument('--requests',type=int,default=100000);parser.add_argument('--repeats',type=int,default=2)
parser.add_argument('--concurrency',type=int,default=10000);parser.add_argument('--delay-ms',type=int,default=200);parser.add_argument('--k6-concurrency',type=int);parser.add_argument('--out',default='pilot');parser.add_argument('--tools',nargs='+',default=['swarmgo','k6']);parser.add_argument('--swarmgo-binary',default='swarmgo-improved');args=parser.parse_args()
out=ROOT/'results'/args.out;out.mkdir(parents=True,exist_ok=False)
(ROOT/'body.json').write_text('{"data":"'+'x'*1013+'"}');(ROOT/'empty').write_bytes(b'')
target=PREFIX+'-target';created=[];results=[]
manifest={'swarmgo_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT.parent/'SwarmGo',text=True).strip(),
          'generator_cpus':'all available in Docker VM','generator_cpu_quota':None,'generator_memory_bytes':None,'target_cpus':'shared with generator, all available in Docker VM','target_cpu_quota':None,'target_memory_bytes':None,
          'image':IMAGE,'requests':args.requests,'repeats':args.repeats,'workloads':[{'method':'POST','delay_ms':args.delay_ms,'concurrency':args.concurrency}],
          'k6_concurrency':args.k6_concurrency,'k6_version':'2.3.0','swarmgo_binary':args.swarmgo_binary,'go_build_info':{name:subprocess.check_output(['go','version','-m',str(ROOT/'bin'/(args.swarmgo_binary if name=='swarmgo' else name))],text=True) for name in ['swarmgo','k6','target']},
          'host_cpu':subprocess.check_output(['sysctl','-n','machdep.cpu.brand_string'],text=True).strip(),
          'docker':json.loads(docker('info','--format','{{json .}}')),
          'binary_sha256':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in (ROOT/'bin').iterdir() if p.name in [args.swarmgo_binary,'k6','target']}}
try:
 docker('network','create','--internal',PREFIX)
 create(target,'2-5',4,'2g',['/bench/bin/target']);created.append(target)
 for _ in range(30):
  try:stats(target);break
  except subprocess.CalledProcessError:time.sleep(.1)
 for case in manifest['workloads']:
  for repetition in range(args.repeats):
   for tool in (args.tools if repetition%2==0 else list(reversed(args.tools))):
    case={**case,'concurrency':args.k6_concurrency if tool=='k6' and args.k6_concurrency else args.concurrency}
    label=f"{case['method'].lower()}-{repetition+1}-{tool}";name=PREFIX+'-'+label
    create(name,'0-1',2,'2g',['sleep','infinity']);created.append(name)

    for _ in range(50):
     if stats(target)['active_requests']==0:break
     time.sleep(.1)
    else:raise RuntimeError('Previous requests are still active')
    docker('exec',target,'/bench/bin/target','reset');before=resources(target)
    summary=f'/bench/results/{args.out}/{label}.json'
    env={'SWARMGO_BINARY':args.swarmgo_binary,'REQUESTS':str(args.requests),'CONCURRENCY':str(case['concurrency']),'METHOD':case['method'],
         'TARGET':f'http://{target}:8080/work?delay_ms={case["delay_ms"]}','SUMMARY':summary,
         'BODY_FILE':'/bench/body.json' if case['method']=='POST' else '/bench/empty'}
    cmd=['docker','exec',*[x for k,v in env.items() for x in ['-e',f'{k}={v}']],name]
    cmd+=['sh','/bench/swarm.sh'] if tool=='swarmgo' else ['/bench/bin/k6','run','--quiet','/bench/k6.js']
    started=time.monotonic()
    with (out/(label+'.log')).open('w') as log:
     p=subprocess.run(cmd,stdout=log,stderr=subprocess.STDOUT,timeout=360)
    elapsed=time.monotonic()-started
    generator=resources(name);after=resources(target);observed=stats(target)
    record={'tool':tool,'case':case,'repetition':repetition+1,'process_seconds':elapsed,'exit_code':p.returncode,
            'generator':generator,'target_cpu_usec':after['usage_usec']-before['usage_usec'],'server':observed}
    if p.returncode==0:
     record['server_rps']=observed['requests']/observed['elapsed_seconds']
     raw=json.loads((out/(label+'.json')).read_text())
     if tool=='swarmgo':assert raw['success'] and raw['requests']['failed']==0 and raw['requests']['completed']==args.requests
     else:assert raw['metrics']['http_reqs']['values']['count']==args.requests and raw['metrics']['http_req_failed']['values']['rate']==0 and raw['metrics']['iterations']['values']['count']==args.requests
     record['summary']=raw
     assert observed['requests']==args.requests and observed['invalid']==0
     assert observed['body_bytes']==(args.requests*1024 if case['method']=='POST' else 0)
    results.append(record)
    (out/'results.json').write_text(json.dumps({'manifest':manifest,'results':results},indent=2)+'\n')
    print(json.dumps({k:v for k,v in record.items() if k not in ['summary','case']},ensure_ascii=False),flush=True)
    docker('rm','-f',name);created.remove(name)
    if p.returncode:print(label+' failed; recorded as an unsuccessful run',flush=True)
finally:
 for name in created:subprocess.run(['docker','rm','-f',name],capture_output=True)
 subprocess.run(['docker','network','rm',PREFIX],capture_output=True)

raise SystemExit(int(any(r["exit_code"] for r in results)))
