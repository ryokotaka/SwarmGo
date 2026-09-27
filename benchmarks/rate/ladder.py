"""Constant-rate comparison: can a generator deliver a requested rate?

Each invocation runs one tool at one requested POST rate against an owned,
isolated local target and records whether the target received that rate.
The verdict uses only the target's own validated request counter, so every
tool is judged by the same measure regardless of how it reports itself.

--prepare downloads the pinned vegeta release and builds the pinned wrk2
image. It sends no load. SwarmGo, the target, k6 and oha come from
benchmarks/throughput/bin, prepared as described in that directory.
"""
from pathlib import Path
import argparse
import hashlib
import io
import json
import re
import signal
import subprocess
import sys
import tarfile
import time
import uuid

ROOT = Path(__file__).resolve().parent
THROUGHPUT = ROOT.parent / 'throughput'
sys.path.insert(0, str(ROOT.parent / 'arrival'))
from prepare import Docker, IMAGE, download, product_hash, sha256  # noqa: E402
from bench import resources  # noqa: E402

VEGETA_VERSION = '12.13.0'
VEGETA_SHA256 = {  # From the release's vegeta_12.13.0_checksums.txt.
    'arm64': '950381173a5575e25e8e086f36fc03bf65d61a2433329b48e41e1cb5e4133bba',
    'amd64': 'e8759ce45c14e18374bdccd3ba6068197bc3a9f9b7e484db3837f701b9d12e61',
}
WRK2_COMMIT = '44a94c17d8e6a0bac8559b53da76848e430cb7a7'
LUAJIT_COMMIT = 'c6ffc141a8762b41703f9287d63d93622a13dd8f'  # v2.1 branch.
WRK2_IMAGE = 'swarmgo-wrk2-local:' + WRK2_COMMIT[:7]
TOOLS = ['swarmgo', 'wrk2', 'vegeta', 'k6', 'oha']


def prepare(D):
    arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}.get(D.info['Architecture'])
    if arch is None:
        raise RuntimeError('Unsupported Docker architecture.')
    url = (f'https://github.com/tsenart/vegeta/releases/download/v{VEGETA_VERSION}/'
           f'vegeta_{VEGETA_VERSION}_linux_{arch}.tar.gz')
    archive = download(url, 64 * 1024 * 1024)
    if hashlib.sha256(archive).hexdigest() != VEGETA_SHA256[arch]:
        raise RuntimeError('vegeta archive differs from the pinned release checksum.')
    with tarfile.open(fileobj=io.BytesIO(archive), mode='r:gz') as tar:
        # No extract()/extractall(): only the regular executable entry is read.
        member = tar.getmember('vegeta')
        if not member.isfile():
            raise RuntimeError('Unexpected vegeta archive layout.')
        binary = tar.extractfile(member).read()
    (ROOT / 'bin').mkdir(exist_ok=True)
    (ROOT / 'bin' / 'vegeta').write_bytes(binary)
    (ROOT / 'bin' / 'vegeta').chmod(0o755)
    for name, repo, commit in [('wrk2-src', 'https://github.com/giltene/wrk2.git', WRK2_COMMIT),
                               ('luajit-src', 'https://github.com/LuaJIT/LuaJIT.git', LUAJIT_COMMIT)]:
        path = ROOT / name
        if not path.exists():
            subprocess.run(['git', 'clone', '--quiet', repo, str(path)], check=True, timeout=600)
        subprocess.run(['git', '-C', str(path), 'checkout', '--quiet', commit], check=True, timeout=60)
        head = subprocess.check_output(['git', '-C', str(path), 'rev-parse', 'HEAD'], text=True).strip()
        if head != commit:
            raise RuntimeError(f'{name} is not at the pinned commit.')
    D.run('build', '-t', WRK2_IMAGE, '-f', str(ROOT / 'Dockerfile.wrk2'), str(ROOT), timeout=1800)
    missing = [n for n in ['swarmgo', 'target', 'k6', 'oha'] if not (THROUGHPUT / 'bin' / n).exists()]
    if missing or not (THROUGHPUT / 'body.json').exists():
        raise RuntimeError('Prepare benchmarks/throughput first (see its README); missing: '
                           + ', '.join(missing or ['body.json']))
    print(json.dumps({'vegeta': f'v{VEGETA_VERSION} {arch}', 'wrk2_image': WRK2_IMAGE,
                      'wrk2_image_id': D.run('image', 'inspect', WRK2_IMAGE, '--format', '{{.Id}}')}))


p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
p.add_argument('--prepare', action='store_true', help='Download and build the pinned tools, then exit without load')
p.add_argument('--tool', choices=TOOLS)
p.add_argument('--rate', type=int, help='Requested POSTs per second')
p.add_argument('--seconds', type=int, default=60, help='Measured seconds after the warmup (default 60)')
p.add_argument('--warmup', type=int, default=5, help='Seconds after first activity before measuring (default 5)')
p.add_argument('--concurrency', type=int, default=1024,
               help='In-flight request ceiling: connections, workers or VUs (default 1024)')
p.add_argument('--client-cpus', default='0-3', help='Docker cpuset for the generator (default 0-3)')
p.add_argument('--target-cpus', default='4-9', help='Docker cpuset for the target (default 4-9)')
p.add_argument('--tolerance', type=float, default=0.001,
               help='Allowed shortfall of the delivered rate, as a fraction (default 0.001 = 99.9%% delivered)')
p.add_argument('--out', help='New directory name under results/')
a = p.parse_args()
if a.prepare:
    prepare(Docker())
    sys.exit(0)
if a.tool is None or a.rate is None or a.out is None:
    p.error('--tool, --rate and --out are required unless --prepare is used.')
cpuset = re.compile(r'^\d+(-\d+)?(,\d+(-\d+)?)*$')
if not (cpuset.match(a.client_cpus) and cpuset.match(a.target_cpus)):
    p.error('Use Docker cpusets such as 0-3 or 0,2,4.')
if not 1 <= a.rate <= 2_000_000 or not 10 <= a.seconds <= 600 or not 1 <= a.warmup <= 60:
    p.error('Use 1..2000000 requests/s, 10..600 seconds and 1..60 warmup seconds.')
if not 1 <= a.concurrency <= 20_000 or not 0 <= a.tolerance < 0.5:
    p.error('Use 1..20000 concurrency and a tolerance below 0.5.')
if Path(a.out).name != a.out or a.out in ['.', '..']:
    p.error('Use a new single directory name for --out.')


def cpus(spec):
    total = 0
    for part in spec.split(','):
        low, _, high = part.partition('-')
        total += int(high or low) - int(low) + 1
    return total


if not (ROOT / 'bin' / 'vegeta').exists():
    p.error('Run --prepare first.')
out = ROOT / 'results' / a.out
out.mkdir(parents=True, exist_ok=False)
D = Docker()
duration = a.warmup + a.seconds + 5  # Five seconds beyond the measured window.
network = 'swarmgo-rate-' + uuid.uuid4().hex[:10]
target, client = network + '-target', network + '-client'
threads = cpus(a.client_cpus)
created, proc = [], None
manifest = {
    'tool': a.tool, 'requested_rps': a.rate, 'measured_seconds': a.seconds, 'warmup_seconds': a.warmup,
    'native_duration_seconds': duration, 'concurrency': a.concurrency, 'tolerance': a.tolerance,
    'cpusets': {'client': a.client_cpus, 'target': a.target_cpus}, 'wrk2_threads': threads,
    'generator_memory_bytes': 6 * 1024**3, 'target_memory_bytes': 512 * 1024**2, 'image': IMAGE,
    'target': 'fasthttp HTTP/1.1; validates 1 KiB POST body; 1 KiB response; no delay',
    'swarmgo_source': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT.parents[1], text=True).strip(),
    'swarmgo_product_sha256': product_hash(),
    'sha256': {str(f.relative_to(ROOT.parent)): sha256(f) for f in
               [THROUGHPUT / 'target.go', THROUGHPUT / 'body.json', THROUGHPUT / 'wrk.lua', ROOT / 'rate.k6.js',
                ROOT / 'Dockerfile.wrk2', *sorted((THROUGHPUT / 'bin').iterdir()), *sorted((ROOT / 'bin').iterdir())]},
    'versions': {'wrk2': WRK2_COMMIT, 'luajit': LUAJIT_COMMIT, 'vegeta': VEGETA_VERSION, 'k6': '2.3.0', 'oha': '1.16.0'},
    'docker': {k: D.info.get(k) for k in ['ServerVersion', 'KernelVersion', 'NCPU', 'MemTotal', 'Architecture']},
    'internal_network': True, 'published_ports': [],
    'verdict_rule': 'held = target-validated POSTs per second over the measured window >= requested * (1 - tolerance), '
                    'with zero invalid requests in the window',
}
record = {'samples': []}


def save():
    (out / 'results.json').write_text(json.dumps({'manifest': manifest, 'result': record}, indent=2) + '\n')


def stats():
    return json.loads(D.run('exec', target, '/bench/bin/target', '-mode', 'stats'))


def interrupted(signum, frame):
    raise KeyboardInterrupt


signal.signal(signal.SIGTERM, interrupted)
try:
    D.run('network', 'create', '--internal', '--label', 'swarmgo.benchmark=rate', network)
    save()
    for name, memory, image, command, cpu in [
        (target, '512m', IMAGE, ['/bench/bin/target'], a.target_cpus),
        (client, '6g', WRK2_IMAGE if a.tool == 'wrk2' else IMAGE, ['sleep', 'infinity'], a.client_cpus),
    ]:
        created.append(name)
        D.run('run', '--pull=never', '-d', '--name', name, '--network', network,
              '--memory', memory, '--memory-swap', memory, '--cpuset-cpus', cpu,
              '-v', f'{THROUGHPUT}:/bench:ro', '-v', f'{ROOT}:/rate:ro', '-v', f'{out}:/results',
              image, *command)
    for _ in range(100):
        try:
            stats()
            break
        except subprocess.SubprocessError:
            time.sleep(.05)
    else:
        raise RuntimeError('Target did not start')
    address = D.run('inspect', '--format', '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}', target)
    url = f'http://{address}:8080/work'
    headers = ['Content-Type: application/json', 'Accept-Encoding: identity']
    if a.tool == 'swarmgo':
        # The load spike covers the whole run. Its one ordinary request per
        # second goes to /stats, which the target does not count as load.
        command = ['/bench/bin/swarmgo', 'resilience', '-url', url, '-method', 'POST', '-body-file', '/bench/body.json',
                   '-header', headers[0], '-header', headers[1], '-rate', str(a.rate), '-c', str(a.concurrency),
                   '-probe-url', f'http://{address}:8080/stats', '-probe-rate', '1', '-probe-c', '1',
                   '-baseline', '1s', '-spike', f'{duration}s', '-recovery', '2s', '-recovery-window', '1s',
                   '-output', '/results/native.json']
    elif a.tool == 'wrk2':
        command = ['wrk2', f'-t{threads}', f'-c{a.concurrency}', f'-d{duration}s', f'-R{a.rate}', '--latency',
                   '-s', '/bench/wrk.lua', url]
    elif a.tool == 'vegeta':
        attack = (f"echo 'POST {url}' | /rate/bin/vegeta attack -rate={a.rate}/s -duration={duration}s "
                  f"-body=/bench/body.json -header='{headers[0]}' -header='{headers[1]}' "
                  f"-max-workers={a.concurrency} | /rate/bin/vegeta report -type=json > /results/native.json")
        command = ['sh', '-c', attack]
    elif a.tool == 'k6':
        command = ['/bench/bin/k6', 'run', '--quiet', '/rate/rate.k6.js']
    else:
        command = ['/bench/bin/oha', '--no-tui', '--output-format', 'json', '-o', '/results/native.json',
                   '--http-version', '1.1', '--latency-correction', '-q', str(a.rate), '-z', f'{duration}s',
                   '-c', str(a.concurrency), '-m', 'POST', '-D', '/bench/body.json',
                   '-H', headers[0], '-H', headers[1], url]
    env = {'TARGET': url, 'RATE': str(a.rate), 'SECONDS': str(duration), 'CONCURRENCY': str(a.concurrency),
           'SUMMARY': '/results/native.json', 'K6_NO_USAGE_REPORT': 'true'}
    record.update({'command': command, 'environment': env})
    if a.tool == 'wrk2':
        record['image_id'] = D.run('image', 'inspect', WRK2_IMAGE, '--format', '{{.Id}}')
    started = time.monotonic()
    with (out / 'run.log').open('w') as log:
        proc = subprocess.Popen(D.prefix + ['exec', *[x for k, v in env.items() for x in ['-e', f'{k}={v}']], client]
                                + command, stdout=log, stderr=subprocess.STDOUT)
        while True:
            first = stats()
            if first['requests'] > 0:
                break
            if proc.poll() is not None or time.monotonic() - started > 30:
                raise RuntimeError(f'{a.tool} did not send load')
            time.sleep(.1)
        record['first_activity_process_seconds'] = time.monotonic() - started
        time.sleep(a.warmup)
        base = previous = stats()
        record['measurement_start'] = base
        end_at = time.monotonic() + a.seconds
        while True:
            time.sleep(min(5, max(0, end_at - time.monotonic())))
            observed = stats()
            elapsed = (observed['snapshot_unix_ns'] - previous['snapshot_unix_ns']) / 1e9
            record['samples'].append({
                'measurement_seconds': (observed['snapshot_unix_ns'] - base['snapshot_unix_ns']) / 1e9,
                'interval_seconds': elapsed,
                'target_rps': (observed['requests'] - previous['requests']) / elapsed,
                'server': observed, 'generator': resources(D, client), 'target_container': resources(D, target)})
            previous = observed
            save()
            if time.monotonic() >= end_at or proc.poll() is not None:
                break
        window = (observed['snapshot_unix_ns'] - base['snapshot_unix_ns']) / 1e9
        delivered = (observed['requests'] - base['requests']) / window
        invalid = observed['invalid'] - base['invalid']
        record.update({
            'measurement_seconds': window, 'target_posts': observed['requests'] - base['requests'],
            'delivered_rps': delivered, 'delivered_ratio': delivered / a.rate, 'invalid_in_window': invalid,
            'min_interval_ratio': min(s['target_rps'] for s in record['samples']) / a.rate,
            'completed_observation': window >= a.seconds and proc.poll() is None,
        })
        record['held'] = (record['completed_observation'] and invalid == 0
                          and delivered >= a.rate * (1 - a.tolerance))
        proc.wait(timeout=duration + 60)
    record['exit_code'] = proc.returncode
    record['process_seconds'] = time.monotonic() - started
    record['generator_final'] = resources(D, client)
    for _ in range(100):
        final = stats()
        if final['active_requests'] == 0:
            break
        time.sleep(.05)
    record['final_target'] = final
    record['target_valid'] = final['invalid'] == 0 and final['body_bytes'] == final['requests'] * 1024
    native = out / 'native.json'
    if native.exists() and native.stat().st_size:
        try:
            record['native'] = json.loads(native.read_text())
        except json.JSONDecodeError:
            record['native_unparsed'] = True
    save()
    print(json.dumps({k: record.get(k) for k in ['delivered_rps', 'delivered_ratio', 'min_interval_ratio', 'held',
                                                 'invalid_in_window', 'exit_code']} | {'tool': a.tool, 'rate': a.rate}))
finally:
    for name in reversed(created):
        try:
            D.run('rm', '-f', name)
        except subprocess.SubprocessError:
            pass
    if proc and proc.poll() is None:
        proc.terminate()
        proc.wait(timeout=10)
    try:
        D.run('network', 'rm', network)
    except subprocess.SubprocessError:
        pass
