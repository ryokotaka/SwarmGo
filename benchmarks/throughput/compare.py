"""Compare uncapped HTTP generators against one owned, isolated local target."""
from pathlib import Path
import argparse
import hashlib
import json
import re
import signal
import subprocess
import sys
import time
import uuid

ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT.parent / 'arrival'))
from prepare import Docker, IMAGE, product_hash
from bench import resources

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--tools', nargs='+', choices=['swarmgo', 'wrk', 'oha', 'k6'], default=['swarmgo', 'wrk', 'oha', 'k6'])
p.add_argument('--seconds', type=int, default=30)
p.add_argument('--concurrency', type=int, default=256)
p.add_argument('--out', required=True)
p.add_argument('--gomaxprocs', type=int, help='Set GOMAXPROCS for the generator process (Go tools only); unset by default')
p.add_argument('--client-cpus', help='Docker cpuset for the generator container, e.g. 0-3; unset shares all CPUs')
p.add_argument('--target-cpus', help='Docker cpuset for the target container, e.g. 4-9; unset shares all CPUs')
p.add_argument('--wrk-threads', type=int, default=8, help='wrk threads (default 8)')
a = p.parse_args()
cpuset = re.compile(r'^\d+(-\d+)?(,\d+(-\d+)?)*$')
for value in [a.client_cpus, a.target_cpus]:
    if value is not None and not cpuset.match(value):
        p.error('Use a Docker cpuset such as 0-3 or 0,2,4.')
if not 1 <= a.wrk_threads <= a.concurrency:
    p.error('Use 1..concurrency wrk threads.')
if a.gomaxprocs is not None and not 1 <= a.gomaxprocs <= 256:
    p.error('Use 1..256 for --gomaxprocs.')
if not 5 <= a.seconds <= 300 or not 8 <= a.concurrency <= 4096 or a.concurrency % 8:
    p.error('Use 5..300 seconds and 8..4096 concurrency divisible by 8.')
if Path(a.out).name != a.out or a.out in ['.', '..']:
    p.error('Use a new single directory name for --out.')
out = ROOT / 'results' / a.out
out.mkdir(parents=True, exist_ok=False)
D = Docker()
duration = a.seconds + 10  # Five seconds warmup and five seconds beyond the observation.
network = 'swarmgo-max-' + uuid.uuid4().hex[:10]
created = []
records = []
proc = None
manifest = {
    'requested_rps': None, 'measurement_seconds': a.seconds, 'warmup_seconds': 5,
    'native_duration_seconds': duration, 'concurrency': a.concurrency, 'wrk_threads': a.wrk_threads,
    'generator_memory_bytes': 6 * 1024**3, 'target_memory_bytes': 512 * 1024**2,
    'cpu_limits': None, 'extra_swap': False, 'image': IMAGE,
    # Pinning isolates the generator's own capacity; unset, both share the host.
    'cpusets': {'client': a.client_cpus, 'target': a.target_cpus},
    'target': 'fasthttp HTTP/1.1; 1 KiB POST and response; no delay; no probe stream',
    'swarmgo_source': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT.parents[1], text=True).strip(),
    'swarmgo_product_sha256': product_hash(),
    'sha256': {str(f.relative_to(ROOT)): hashlib.sha256(f.read_bytes()).hexdigest()
               for f in [ROOT/'target.go', ROOT/'body.json', ROOT/'swarm.sh', ROOT/'wrk.lua', ROOT/'k6.js', *sorted((ROOT/'bin').iterdir())]},
    'docker': {k: D.info.get(k) for k in ['ServerVersion', 'KernelVersion', 'NCPU', 'MemTotal', 'Architecture']},
    'versions': {'wrk': '4.2.0', 'oha': '1.16.0', 'k6': '2.3.0'},
    'internal_network': True, 'published_ports': [], 'tool_order': a.tools, 'gomaxprocs': a.gomaxprocs,
    'note': 'All rates use target-validated POST counter deltas and target snapshot-clock deltas. Native stop/report phases are outside the measurement. SwarmGo is deliberately deadline-stopped and its incomplete report is retained, not called a successful fixed-count run.',
}

def save():
    (out/'results.json').write_text(json.dumps({'manifest': manifest, 'results': records}, indent=2)+'\n')

def stats(name):
    return json.loads(D.run('exec', name, '/bench/bin/target', '-mode', 'stats'))

def interrupted(signum, frame):
    raise KeyboardInterrupt

signal.signal(signal.SIGTERM, interrupted)
try:
    D.run('network', 'create', '--internal', '--label', 'swarmgo.benchmark=max', network)
    save()
    for tool in a.tools:
        target, client = network+'-target', network+'-client'
        trial = out/tool
        trial.mkdir()
        for name, memory, image, command, cpus in [
            (target, '512m', IMAGE, ['/bench/bin/target'], a.target_cpus),
            (client, '6g', 'swarmgo-wrk-local:4.2.0' if tool == 'wrk' else IMAGE, ['sleep', 'infinity'], a.client_cpus),
        ]:
            created.append(name)
            D.run('run', '--pull=never', '-d', '--name', name, '--network', network,
                  '--memory', memory, '--memory-swap', memory, *(['--cpuset-cpus', cpus] if cpus else []),
                  '-v', str(ROOT)+':/bench:ro', '-v', str(trial)+':/results', image, *command)
        for _ in range(100):
            try:
                stats(target)
                break
            except subprocess.SubprocessError:
                time.sleep(.05)
        else:
            raise RuntimeError('Target did not start')
        address = D.run('inspect', '--format', '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}', target)
        url = f'http://{address}:8080/work'
        env = {'TARGET': url, 'DURATION': str(duration), 'CONCURRENCY': str(a.concurrency),
               'SUMMARY': '/results/native.json', 'K6_NO_USAGE_REPORT': 'true'}
        if a.gomaxprocs is not None:
            env['GOMAXPROCS'] = str(a.gomaxprocs)
        if tool == 'swarmgo':
            command = ['sh', '/bench/swarm.sh']
        elif tool == 'wrk':
            command = ['wrk', f'-t{a.wrk_threads}', f'-c{a.concurrency}', f'-d{duration}s', '--timeout', '30s', '-s', '/bench/wrk.lua', url]
        elif tool == 'oha':
            command = ['/bench/bin/oha', '--no-tui', '--output-format', 'json', '-o', '/results/native.json',
                       '--http-version', '1.1', '-w', '-t', '30s', '-m', 'POST', '-D', '/bench/body.json',
                       '-T', 'application/json', '-H', 'Accept-Encoding: identity', '-c', str(a.concurrency), '-z', f'{duration}s', url]
        else:
            command = ['/bench/bin/k6', 'run', '--quiet', '/bench/k6.js']
        record = {'tool': tool, 'command': command, 'environment': env, 'samples': []}
        if tool == 'wrk':
            record['binary_sha256'] = D.run('exec', client, 'sha256sum', '/usr/local/bin/wrk').split()[0]
            record['image_id'] = D.run('image', 'inspect', 'swarmgo-wrk-local:4.2.0', '--format', '{{.Id}}')
        records.append(record)
        started = time.monotonic()
        with (trial/'run.log').open('w') as log:
            proc = subprocess.Popen(D.prefix+['exec', *[part for k, v in env.items() for part in ['-e', f'{k}={v}']], client]+command, stdout=log, stderr=subprocess.STDOUT)
            while True:
                first = stats(target)
                if first['requests'] > 0:
                    break
                if proc.poll() is not None or time.monotonic()-started > 30:
                    raise RuntimeError(f'{tool} did not send load')
                time.sleep(.1)
            record['first_activity_process_seconds'] = time.monotonic()-started
            record['first_activity_snapshot'] = first
            time.sleep(5)
            base = stats(target)
            record['measurement_start'] = base
            previous = base
            end_at = time.monotonic()+a.seconds
            while True:
                time.sleep(min(5, max(0, end_at-time.monotonic())))
                observed = stats(target)
                elapsed = (observed['snapshot_unix_ns']-previous['snapshot_unix_ns'])/1e9
                sample = {'process_seconds': time.monotonic()-started,
                          'measurement_seconds': (observed['snapshot_unix_ns']-base['snapshot_unix_ns'])/1e9,
                          'interval_seconds': elapsed,
                          'target_rps': (observed['requests']-previous['requests'])/elapsed,
                          'server': observed, 'generator': resources(D, client),
                          # Target CPU shows whether the shared host was saturated.
                          'target_container': resources(D, target)}
                record['samples'].append(sample)
                previous = observed
                save()
                print(json.dumps({'tool': tool, 'measured_s': round(sample['measurement_seconds'], 1),
                                  'rps': round(sample['target_rps']), 'memory_mib': round(sample['generator']['memory_peak_bytes']/1024**2)}), flush=True)
                if time.monotonic() >= end_at or proc.poll() is not None:
                    break
            record['measurement_seconds'] = (observed['snapshot_unix_ns']-base['snapshot_unix_ns'])/1e9
            record['target_posts'] = observed['requests']-base['requests']
            record['target_rps'] = record['target_posts']/record['measurement_seconds']
            record['invalid_in_window'] = observed['invalid']-base['invalid']
            record['completed_observation'] = record['measurement_seconds'] >= a.seconds and proc.poll() is None
            proc.wait(timeout=max(10, duration+40-(time.monotonic()-started)))
        record['exit_code'] = proc.returncode
        record['process_seconds'] = time.monotonic()-started
        record['generator'] = resources(D, client)
        for _ in range(100):
            final = stats(target)
            if final['active_requests'] == 0:
                break
            time.sleep(.05)
        record['final_target'] = final
        record['target_valid'] = final['invalid'] == 0 and final['active_requests'] == 0 and final['body_bytes'] == final['requests']*1024
        native = trial/'native.json'
        if native.exists() and native.stat().st_size:
            record['native'] = json.loads(native.read_text())
        save()
        print(json.dumps({k: record[k] for k in ['tool', 'target_rps', 'measurement_seconds', 'completed_observation', 'target_valid', 'exit_code']}), flush=True)
        for name in list(reversed(created)):
            D.run('rm', '-f', name)
            created.remove(name)
        proc = None
finally:
    for name in reversed(created):
        try:
            D.run('rm', '-f', name)
        except subprocess.SubprocessError:
            pass
    if proc and proc.poll() is None:
        proc.terminate()
        proc.wait(timeout=10)
    D.run('network', 'rm', network)
