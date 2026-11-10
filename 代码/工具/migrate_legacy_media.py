#!/usr/bin/env python3
"""Resumable, rate-limited migration of the owner's public image originals.

No API mutations. Metadata and downloaded bytes stay outside the repository.
The default reserve prevents this job from consuming the server's last 5 GiB.
"""
import argparse
import concurrent.futures
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import queue
import shutil
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

APPROVED_CDN = 'cdn.xuansiwei.com'
PATH_SAFE = '/%:@!$&\'()*+,;=-._~'


class ApprovedCDNRedirect(urllib.request.HTTPRedirectHandler):
    """The allowlist applies to every redirect, not just the first URL."""

    def redirect_request(self, request, response, code, message, headers, new_url):
        parsed = urllib.parse.urlsplit(new_url)
        if (parsed.scheme != 'https' or parsed.hostname != APPROVED_CDN
                or parsed.username or parsed.password or parsed.port not in (None, 443)):
            raise urllib.error.HTTPError(request.full_url, code,
                                         'Redirect outside the approved HTTPS CDN', headers, response)
        return super().redirect_request(request, response, code, message, headers, new_url)


def open_legacy_image(request, timeout=45):
    return urllib.request.build_opener(ApprovedCDNRedirect()).open(request, timeout=timeout)


def request_urls(original):
    """Preserve existing escapes first; only a 400 permits the literal-% fallback.

    The storage key and manifest always keep the untouched source URL. Encoding
    fallback changes the request path only, never OSS query parameters.
    """
    parsed = urllib.parse.urlsplit(original)
    if (parsed.scheme not in ('https', 'http') or parsed.hostname != APPROVED_CDN
            or parsed.username or parsed.password or parsed.port not in (None, 443)):
        raise ValueError('Source outside the approved legacy CDN')
    query = urllib.parse.quote(parsed.query, safe='%=&,:/+-._~')
    initial = urllib.parse.urlunsplit(('https', parsed.netloc,
        urllib.parse.quote(parsed.path, safe=PATH_SAFE), query, ''))
    fallback = None
    if '%' in parsed.path:
        fallback = urllib.parse.urlunsplit(('https', parsed.netloc,
            urllib.parse.quote(parsed.path, safe=PATH_SAFE.replace('%', '')), query, ''))
    return initial, fallback


def arguments():
    p = argparse.ArgumentParser()
    p.add_argument('--source', required=True)
    p.add_argument('--media-root', required=True)
    p.add_argument('--manifest', required=True)
    p.add_argument('--status', required=True)
    p.add_argument('--workers', type=int, default=6)
    p.add_argument('--rate', type=float, default=12)
    p.add_argument('--reserve-gib', type=float, default=5)
    p.add_argument('--max-file-mib', type=int, default=128)
    return p.parse_args()

def main():
    args = arguments()
    root = Path(args.media_root).resolve(); (root/'legacy').mkdir(parents=True, exist_ok=True)
    manifest = Path(args.manifest); status_path = Path(args.status)
    products = json.loads(Path(args.source).read_text(encoding='utf-8'))
    urls = []
    # Cover images first, then galleries, so test-page checks can start early.
    galleries = []
    for product in products:
        import re
        gallery = [s.strip() for s in re.split(r',(?=\s*(?:https?:)?//)', product.get('content', '')) if s.strip()]
        galleries.append(gallery)
    for gallery in galleries:
        if gallery: urls.append(gallery[0])
    for gallery in galleries: urls.extend(gallery[1:])
    urls = list(dict.fromkeys('https:'+u if u.startswith('//') else u for u in urls))
    prior = {}
    if manifest.exists():
        for line in manifest.read_text(encoding='utf-8').splitlines():
            try:
                item = json.loads(line)
                if item.get('state') == 'ready': prior[item['key']] = item
            except ValueError: pass  # A truncated last line is retried, never counted as complete.
    lock = threading.Lock(); throttle = threading.Lock(); stop = threading.Event()
    pending = queue.Queue(maxsize=args.workers*3)
    started = time.monotonic(); next_request = [0.0]
    state = {'total': len(urls), 'ready': 0, 'downloaded': 0, 'resumed': 0, 'failed': 0,
             'downloadedBytes': 0, 'stopReason': None, 'failures': [], 'complete': False}
    reserve = int(args.reserve_gib * 1024**3); max_bytes = args.max_file_mib * 1024**2

    def save_status():
        with lock:
            value = dict(state); value['failures'] = state['failures'][:100]
            value.update(updatedAt=datetime.now(timezone.utc).isoformat(),
                         elapsedSeconds=round(time.monotonic()-started),
                         freeBytes=shutil.disk_usage(root).free)
        temporary = status_path.with_suffix('.tmp')
        temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding='utf-8')
        temporary.replace(status_path)
        return value

    def rate_limit():
        with throttle:
            wait = max(0, next_request[0]-time.monotonic())
            next_request[0] = max(time.monotonic(), next_request[0]) + 1/args.rate
        if stop.wait(wait): raise InterruptedError('stopped')

    def fetch(original):
        key = 'legacy/' + hashlib.sha256(original.encode()).hexdigest()
        destination = root/key; partial = destination.with_name(destination.name+'.part')
        old = prior.get(key)
        if old and destination.is_file() and destination.stat().st_size == old['bytes']:
            with lock: state['ready'] += 1; state['resumed'] += 1
            return
        encoded, fallback = request_urls(original)
        error = None
        for attempt in range(3):
            if stop.is_set(): raise InterruptedError('stopped')
            if shutil.disk_usage(root).free < reserve + 16*1024**2:
                with lock: state['stopReason'] = 'insufficient_disk'
                stop.set(); raise InterruptedError('disk reserve reached')
            try:
                rate_limit()
                request = urllib.request.Request(encoded, headers={
                    'User-Agent': 'QingYeMigration/0.7 (+https://qingyeyouxi.com)',
                    'Referer': 'https://qingyeyouxi.com/', 'Accept-Encoding': 'identity',
                })
                sha = hashlib.sha256(); length = 0; first = b''
                with open_legacy_image(request, timeout=45) as response, partial.open('wb') as target:
                    expected = int(response.headers.get('Content-Length') or 0)
                    if expected > max_bytes: raise ValueError('Image exceeds migration byte limit')
                    content_type = response.headers.get('Content-Type', '').split(';')[0]
                    if not content_type.startswith('image/'): raise ValueError('Non-image response')
                    while True:
                        chunk = response.read(256*1024)
                        if not chunk: break
                        if not first: first = chunk[:32]
                        length += len(chunk)
                        if length > max_bytes: raise ValueError('Image exceeds migration byte limit')
                        if stop.is_set(): raise InterruptedError('stopped')
                        if shutil.disk_usage(root).free < reserve:
                            with lock: state['stopReason'] = 'insufficient_disk'
                            stop.set(); raise InterruptedError('disk reserve reached')
                        target.write(chunk); sha.update(chunk)
                recognized = first.startswith((b'\xff\xd8\xff', b'\x89PNG\r\n\x1a\n', b'GIF87a', b'GIF89a', b'BM')) or (first[:4] == b'RIFF' and first[8:12] == b'WEBP')
                if not recognized or not length: raise ValueError('Unrecognized image signature')
                if expected and length != expected: raise ValueError('Truncated download')
                partial.replace(destination); destination.chmod(0o640)
                item = {'key':key, 'source_url':original, 'bytes':length, 'sha256':sha.hexdigest(),
                        'mime_type':content_type, 'state':'ready', 'downloaded_at':datetime.now(timezone.utc).isoformat()}
                with lock:
                    with manifest.open('a', encoding='utf-8') as log: log.write(json.dumps(item,ensure_ascii=False)+'\n')
                    state['ready'] += 1; state['downloaded'] += 1; state['downloadedBytes'] += length
                return
            except InterruptedError: raise
            except Exception as exc:
                error = type(exc).__name__ + ': ' + str(exc)
                if isinstance(exc, urllib.error.HTTPError):
                    exc.close()
                if isinstance(exc, urllib.error.HTTPError) and exc.code == 400 and fallback:
                    encoded, fallback = fallback, None
                    continue
                if isinstance(exc, urllib.error.HTTPError) and exc.code in (401,403,404): break
                stop.wait(min(8, 2**attempt))
            finally:
                if partial.exists(): partial.unlink()
        raise RuntimeError(error)

    def worker():
        while True:
            item = pending.get()
            if item is None: pending.task_done(); return
            try:
                if not stop.is_set(): fetch(item)
            except InterruptedError: pass
            except Exception as error:
                with lock:
                    state['failed'] += 1
                    state['failures'].append({'source_url':item,'error':str(error)})
                with lock:
                    with manifest.open('a',encoding='utf-8') as log:
                        log.write(json.dumps({'source_url':item,'state':'failed','error':str(error)},ensure_ascii=False)+'\n')
            finally: pending.task_done()

    threads = [threading.Thread(target=worker,daemon=True) for _ in range(args.workers)]
    for thread in threads: thread.start()
    def produce():
        for url in urls:
            if stop.is_set(): break
            pending.put(url)
        for _ in threads: pending.put(None)
    producer = threading.Thread(target=produce,daemon=True); producer.start()
    while producer.is_alive() or any(thread.is_alive() for thread in threads):
        time.sleep(5)
        value = save_status()
        print(json.dumps({k:value[k] for k in ('ready','total','failed','downloadedBytes','freeBytes','stopReason')}),flush=True)
    state['complete'] = state['ready'] == state['total'] and state['failed'] == 0
    save_status()
    if not state['complete']: raise SystemExit(75 if state['stopReason']=='insufficient_disk' else 1)

if __name__ == '__main__': main()
