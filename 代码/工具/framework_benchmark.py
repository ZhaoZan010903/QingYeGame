#!/usr/bin/env python3
"""Bounded, read-only API comparison on the isolated acceptance deployment."""
import argparse
import concurrent.futures
import datetime
import hashlib
import http.client
import json
import math
from pathlib import Path
import statistics
import subprocess
import threading
import time
import urllib.parse


def process_state():
    result = subprocess.run(['systemctl', 'show', 'qingye-acceptance@public', '-p', 'MainPID', '--value'], capture_output=True, text=True, check=True)
    pid = int(result.stdout)
    stat = Path(f'/proc/{pid}/stat').read_text().split()
    memory = {}
    for line in Path(f'/proc/{pid}/status').read_text().splitlines():
        if line.startswith(('VmRSS:', 'VmHWM:', 'Threads:')):
            key, value = line.split(':', 1)
            memory[key] = value.strip()
    return {'pid': pid, 'cpu_ticks': int(stat[13]) + int(stat[14]), **memory}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--url', default='http://127.0.0.1:5184')
    p.add_argument('--label', required=True)
    p.add_argument('--output', required=True)
    args = p.parse_args()
    url = urllib.parse.urlsplit(args.url)
    if url.scheme != 'http' or url.hostname != '127.0.0.1' or url.port != 5184 or url.path not in ('', '/') or url.query or url.fragment or url.username:
        p.error('Only the isolated loopback acceptance API on port 5184 is allowed')
    local = threading.local()
    paths = ['/api/v1/products?limit=24&sort=newest', '/api/v1/products?limit=24&sort=hot', '/api/v1/home', '/api/v1/categories']
    def request(index):
        if not hasattr(local, 'connection'):
            local.connection = http.client.HTTPConnection(url.hostname, url.port, timeout=20)
        path = paths[index % len(paths)]
        start = time.perf_counter()
        try:
            local.connection.request('GET', path)
            response = local.connection.getresponse()
            body = response.read()
            return {'ms': (time.perf_counter()-start)*1000, 'status': response.status, 'bytes': len(body), 'path': path}
        except Exception as error:
            local.connection.close()
            del local.connection
            return {'ms': (time.perf_counter()-start)*1000, 'status': 0, 'error': type(error).__name__, 'path': path}
    # 固定预热次数；前后版本使用相同查询和连接复用方式。
    for i in range(40):
        sample = request(i)
        if sample['status'] != 200:
            raise RuntimeError(f"warmup failed: {sample}")
    cases = []
    for workers, count in ((20, 300), (50, 500)):
        before = process_state()
        start = time.perf_counter()
        with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
            samples = list(pool.map(request, range(count)))
        elapsed = time.perf_counter()-start
        after = process_state()
        values = sorted(s['ms'] for s in samples)
        cases.append({'workers': workers, 'requests': count, 'errors': sum(s['status'] != 200 for s in samples), 'seconds': round(elapsed, 3), 'rps': round(count/elapsed, 2), 'median_ms': round(statistics.median(values), 2), 'p95_ms': round(values[math.ceil(count*.95)-1], 2), 'p99_ms': round(values[math.ceil(count*.99)-1], 2), 'max_ms': round(max(values), 2), 'process_before': before, 'process_after': after, 'samples': samples})
    report = {'label': args.label, 'at_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'script_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), 'scope': 'same isolated API, GET only, 40 warmups, persistent HTTP connections; includes client scheduling and same-host contention', 'cases': cases}
    Path(args.output).write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n', encoding='utf-8')
    print(json.dumps({**report, 'cases': [{k:v for k,v in case.items() if k != 'samples'} for case in cases]}, ensure_ascii=False))
    if any(case['errors'] for case in cases):
        raise SystemExit(1)


if __name__ == '__main__':
    main()
