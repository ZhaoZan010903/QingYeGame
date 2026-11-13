#!/usr/bin/env python3
"""Import bundled avatars through authenticated admin APIs; keep existing entries."""
import argparse
import atexit
import hashlib
import http.cookies
import json
import os
from pathlib import Path
import sys
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


ASSETS = [
    ('anime-spring.jpg', '日漫·春晴'), ('anime-starlight.jpg', '日漫·星野'),
    ('xianxia-jade.jpg', '仙侠·青璃'), ('xianxia-cloud.jpg', '仙侠·云归'),
    ('qingye-spring.png', '线绘·春芽'), ('qingye-summer.png', '线绘·夏晴'),
    ('qingye-autumn.png', '线绘·秋叶'), ('qingye-winter.png', '线绘·冬雪'),
    ('qingye-moon.png', '线绘·月光'), ('qingye-star.png', '线绘·星辰'),
]


def acquire_seed_lock(base, secret_path):
    # OS advisory locks release on process exit; a crashed import cannot leave a stale lock.
    key = hashlib.sha256(base.encode()).hexdigest()[:16]
    path = Path(secret_path).resolve().parent / ('avatar-seed-' + key + '.lock')
    handle = path.open('a+b')
    os.chmod(path, 0o600)
    if path.stat().st_size == 0:
        handle.write(b'0'); handle.flush()
    handle.seek(0)
    try:
        if os.name == 'nt':
            import msvcrt
            msvcrt.locking(handle.fileno(), msvcrt.LK_NBLCK, 1)
        else:
            import fcntl
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        handle.close()
        raise RuntimeError('Another avatar seed is running for this target') from None
    atexit.register(handle.close)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', required=True, help='Explicit admin API origin')
    parser.add_argument('--origin', required=True, help='Configured admin browser origin')
    parser.add_argument('--secrets', required=True, help='Private JSON containing accounts')
    parser.add_argument('--assets', type=Path, default=Path(__file__).resolve().parents[1] / '素材' / '头像库')
    args = parser.parse_args()
    base = args.base.rstrip('/')
    target = urllib.parse.urlsplit(base)
    if target.username or target.password or target.path or target.query or target.fragment:
        raise ValueError('Base must be an origin without credentials or path')
    if target.scheme != 'https' and not (target.scheme == 'http' and target.hostname in ('127.0.0.1', 'localhost', '::1')):
        raise ValueError('HTTP is only allowed for loopback')
    acquire_seed_lock(base, args.secrets)
    cookies = {}
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def api(path, method='GET', body=None, raw=None, mime='application/json'):
        if not path.startswith('/api/v1/'):
            raise ValueError('Unexpected API path')
        headers = {'Origin': args.origin, 'Content-Type': mime,
                   'Cookie': '; '.join(k + '=' + v for k, v in cookies.items())}
        data = json.dumps(body, ensure_ascii=False).encode() if body is not None else raw
        request = urllib.request.Request(base + path, data=data, method=method, headers=headers)
        try:
            response = opener.open(request, timeout=90)
        except urllib.error.HTTPError as error:
            # Keep credentials, upload tickets and server error bodies out of logs.
            raise RuntimeError(f'{method} {path.split("?")[0]} returned HTTP {error.code}') from None
        with response:
            for value in response.headers.get_all('Set-Cookie', []):
                cookie = http.cookies.SimpleCookie(); cookie.load(value)
                for key, item in cookie.items(): cookies[key] = item.value
            payload = response.read()
            return json.loads(payload) if payload else {}

    accounts = json.loads(Path(args.secrets).read_text(encoding='utf-8'))['accounts']
    admin = next(account for account in accounts if account['role'] == 'admin')
    api('/api/v1/auth/login', 'POST', {key: admin[key] for key in ('phone', 'password')})
    created, skipped = [], []
    try:
        existing = api('/api/v1/admin/avatar-library')['data']
        known = {row['name'] for row in existing}
        for index, (filename, name) in enumerate(ASSETS, 1):
            if name in known:
                skipped.append(name)
                continue
            data = (args.assets / filename).read_bytes()
            if not 0 < len(data) <= 2 * 1024 * 1024: raise ValueError('Avatar size exceeds upload policy')
            mime = 'image/png' if filename.endswith('.png') else 'image/jpeg'
            ticket = api('/api/v1/uploads', 'POST', {'filename': filename, 'mime_type': mime,
                'byte_size': len(data), 'purpose': 'avatar'})['data']
            upload = urllib.parse.urlsplit(ticket['upload_url'])
            if upload.scheme or upload.netloc or not upload.path.startswith('/api/v1/uploads/'):
                raise ValueError('Unexpected upload target')
            api(ticket['upload_url'], 'PUT', raw=data, mime=mime)
            api('/api/v1/uploads/' + urllib.parse.quote(ticket['media_id'], safe='') + '/complete', 'POST')
            api('/api/v1/admin/avatar-library', 'POST', {'name': name, 'media_id': ticket['media_id'],
                'sort_order': index * 10, 'enabled': True})
            created.append(name)
    finally:
        try:
            api('/api/v1/auth/logout', 'POST')
        except Exception:
            print('Logout failed; import result or original error is preserved.', file=sys.stderr)
    print(json.dumps({'created': created, 'existing': skipped, 'created_count': len(created), 'bundled_count': len(ASSETS)}, ensure_ascii=False))


if __name__ == '__main__':
    main()
