#!/usr/bin/env python3
"""Install only QingYe's certificate pair atomically, then reload its services.

Certbot deploy hook. Keys stay outside the repository. --install-only prepares
the first certificate before nginx/API configuration is switched.
"""
import argparse
import grp
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument('--install-only', action='store_true')
args = parser.parse_args()
lineage = Path(os.environ.get('RENEWED_LINEAGE', '/etc/letsencrypt/live/qingye-test-ip'))
if lineage != Path('/etc/letsencrypt/live/qingye-test-ip'):
    raise SystemExit('Refusing a certificate for a different site')
subprocess.run(['openssl', 'x509', '-in', str(lineage / 'fullchain.pem'), '-checkend', '86400', '-noout'], check=True)
subprocess.run(['openssl', 'x509', '-in', str(lineage / 'fullchain.pem'), '-checkip', '152.136.252.204', '-noout'], check=True)
cert_pub = subprocess.check_output(['openssl', 'x509', '-in', str(lineage / 'fullchain.pem'), '-pubkey', '-noout'])
key_pub = subprocess.check_output(['openssl', 'pkey', '-in', str(lineage / 'privkey.pem'), '-pubout'])
if cert_pub != key_pub:
    raise SystemExit('Certificate/key mismatch')
base = Path('/etc/qingye-test/tls')
base.mkdir(mode=0o750, parents=True, exist_ok=True)
gid = grp.getgrnam('qingye-test').gr_gid
os.chown(base, 0, gid)
os.chmod(base, 0o750)
version = Path(tempfile.mkdtemp(prefix='cert-', dir=base))
os.chown(version, 0, gid)
os.chmod(version, 0o750)
for name in ['fullchain.pem', 'privkey.pem']:
    dest = version / name
    shutil.copyfile(lineage / name, dest)
    os.chown(dest, 0, gid)
    os.chmod(dest, 0o640)
current = base / 'current'
previous = current.resolve() if current.is_symlink() else None
def point(target):
    candidate = base / 'current.next'
    candidate.unlink(missing_ok=True)
    candidate.symlink_to(target)
    candidate.replace(current)
point(version)
if not args.install_only:
    try:
        for config in ['nginx.conf', 'admin-nginx.conf']:
            subprocess.run(['/usr/sbin/nginx', '-t', '-c', '/etc/qingye-test/' + config], check=True)
        for service in ['qingye-test-web', 'qingye-test-admin-web']:
            subprocess.run(['systemctl', 'reload', service], check=True)
        for service in ['qingye-test-api', 'qingye-test-admin-api']:
            subprocess.run(['systemctl', 'restart', service], check=True)
    except Exception:
        if previous:
            point(previous)
            for service in ['qingye-test-web', 'qingye-test-admin-web']:
                subprocess.run(['systemctl', 'reload', service])
            for service in ['qingye-test-api', 'qingye-test-admin-api']:
                subprocess.run(['systemctl', 'restart', service])
        raise
print('QingYe certificate installed; private key was not printed')
