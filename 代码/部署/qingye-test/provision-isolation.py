#!/usr/bin/env python3
"""Separate the admin service and provision disposable acceptance resources.

This changes only QingYe configuration, its dedicated databases and Redis 6382.
"""
import argparse
import json
import os
from pathlib import Path
import pwd
import re
import shutil
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--acceptance-secrets', required=True)
args = parser.parse_args()
assert os.geteuid() == 0
secret = json.loads(Path(args.acceptance_secrets).read_text())
for key in ('db_password', 'redis_password', 'session_secret'):
    assert re.fullmatch(r'[A-Za-z0-9_-]{32,128}', secret[key])

def psql(sql):
    result = subprocess.run(['runuser', '-u', 'postgres', '--', 'psql', '-XAt', '-v', 'ON_ERROR_STOP=1'],
                            input=sql, text=True, capture_output=True, cwd='/tmp')
    if result.returncode:
        message = result.stderr
        for key in ('db_password', 'redis_password', 'session_secret'):
            message = message.replace(secret[key], '[REDACTED]')
        raise RuntimeError(message)
    return result.stdout.strip()

def environment(path):
    return dict(line.split('=', 1) for line in Path(path).read_text().splitlines()
                if line.strip() and not line.startswith('#'))

def write_environment(path, values):
    target = Path(path)
    target.write_text(''.join(f'{key}={value}\n' for key, value in values.items()))
    target.chmod(0o600)

public = environment('/etc/qingye-test/app.env')
public.update(APP_SURFACE='public', QINGYE_ADDR='127.0.0.1:5180',
              ALLOWED_ORIGINS='http://152.136.252.204:5179')
write_environment('/etc/qingye-test/app.env', public)
admin = dict(public, APP_SURFACE='admin', QINGYE_ADDR='127.0.0.1:5182',
             ALLOWED_ORIGINS='http://152.136.252.204:5181', PUBLIC_BASE_URL='http://152.136.252.204:5181')
write_environment('/etc/qingye-test/admin.env', admin)

name = 'qingye_acceptance'
if not psql(f"SELECT 1 FROM pg_roles WHERE rolname='{name}'"):
    psql(f"CREATE ROLE {name} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '{secret['db_password']}'")
if not psql(f"SELECT 1 FROM pg_database WHERE datname='{name}'"):
    psql(f'CREATE DATABASE {name} OWNER {name}')
hba = Path(psql('SHOW hba_file'))
rule = f'host {name} {name} 127.0.0.1/32 scram-sha-256'
current = hba.read_text()
if rule not in current:
    backup = hba.with_name(hba.name + '.before-qingye-acceptance')
    if not backup.exists():
        shutil.copy2(hba, backup)
    hba.write_text('# QingYe isolated acceptance only\n' + rule + '\n' + current)
    psql('SELECT pg_reload_conf()')

try:
    account = pwd.getpwnam('qingye-acceptance')
except KeyError:
    subprocess.run(['useradd', '--system', '--home-dir', '/var/lib/qingye-acceptance', '--shell', '/sbin/nologin', 'qingye-acceptance'], check=True)
    account = pwd.getpwnam('qingye-acceptance')
for suffix in ('', '/media', '/redis'):
    directory = Path('/var/lib/qingye-acceptance' + suffix)
    directory.mkdir(exist_ok=True, parents=True)
    directory.chmod(0o750)
    os.chown(directory, account.pw_uid, account.pw_gid)

values = dict(public, DATABASE_URL=f"postgres://{name}:{secret['db_password']}@127.0.0.1:5432/{name}?sslmode=disable",
              REDIS_URL=f"redis://:{secret['redis_password']}@127.0.0.1:6382/0",
              MEDIA_DIR='/var/lib/qingye-acceptance/media', SESSION_SECRET=secret['session_secret'],
              QINGYE_ADDR='127.0.0.1:5184', ALLOWED_ORIGINS='http://127.0.0.1:5186',
              PUBLIC_BASE_URL='http://127.0.0.1:5186')
write_environment('/etc/qingye-test/acceptance-public.env', values)
values.update(APP_SURFACE='admin', QINGYE_ADDR='127.0.0.1:5185',
              ALLOWED_ORIGINS='http://127.0.0.1:5187', PUBLIC_BASE_URL='http://127.0.0.1:5187')
write_environment('/etc/qingye-test/acceptance-admin.env', values)

redis = Path('/etc/qingye-test/acceptance-redis.conf')
redis.write_text('\n'.join(['bind 127.0.0.1', 'protected-mode yes', 'port 6382',
    'dir /var/lib/qingye-acceptance/redis', 'dbfilename acceptance.rdb', 'appendonly yes',
    'appendfsync everysec', 'maxmemory 32mb', 'maxmemory-policy noeviction',
    'maxclients 100', 'daemonize no', 'logfile ""', 'requirepass ' + secret['redis_password']]) + '\n')
redis.chmod(0o640)
os.chown(redis, 0, account.pw_gid)
print('Separate admin configuration and isolated acceptance resources provisioned; no credentials printed.')
