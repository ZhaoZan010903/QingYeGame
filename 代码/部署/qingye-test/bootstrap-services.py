#!/usr/bin/env python3
"""Provision isolated test data services. Run as root with a private secrets JSON."""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--secrets', required=True)
args = parser.parse_args()
secrets = json.loads(Path(args.secrets).read_text())
assert os.geteuid() == 0
for key in ('db_password', 'redis_password', 'session_secret'):
    assert re.fullmatch(r'[A-Za-z0-9_-]{32,128}', secrets[key]), 'Unexpected secret encoding'

def psql(sql):
    result = subprocess.run(['runuser', '-u', 'postgres', '--', 'psql', '-XAt', '-v', 'ON_ERROR_STOP=1'],
                            input=sql, text=True, capture_output=True, cwd='/tmp')
    if result.returncode:
        error = result.stderr
        for key in ('db_password', 'redis_password', 'session_secret'):
            error = error.replace(secrets[key], '[REDACTED]')
        raise RuntimeError(error)
    return result.stdout.strip()

if not psql("SELECT 1 FROM pg_roles WHERE rolname='qingye_test';"):
    psql("CREATE ROLE qingye_test LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '" + secrets['db_password'] + "';")
for database in ('qingye_test', 'qingye_test_checks'):
    if not psql(f"SELECT 1 FROM pg_database WHERE datname='{database}';"):
        psql(f'CREATE DATABASE {database} OWNER qingye_test;')

# Some distributions default loopback TCP to ident. Add only our two databases
# and dedicated role, preserving every existing application's authentication.
hba = Path(psql('SHOW hba_file;'))
hba_rule = 'host qingye_test,qingye_test_checks qingye_test 127.0.0.1/32 scram-sha-256'
original_hba = hba.read_text()
if hba_rule not in original_hba:
    backup = hba.with_name(hba.name + '.before-qingye-test')
    if not backup.exists():
        shutil.copy2(hba, backup)
    hba.write_text('# QingYe isolated test databases only\n' + hba_rule + '\n' + original_hba)
    psql('SELECT pg_reload_conf();')

def private_file(path, text, mode=0o600):
    target = Path(path); target.write_text(text); target.chmod(mode)

private_file('/etc/qingye-test/app.env', '\n'.join([
    'APP_SURFACE=public',
    'QINGYE_ADDR=127.0.0.1:5180',
    f"DATABASE_URL=postgres://qingye_test:{secrets['db_password']}@127.0.0.1:5432/qingye_test?sslmode=disable",
    f"REDIS_URL=redis://:{secrets['redis_password']}@127.0.0.1:6381/0",
    'MEDIA_DIR=/var/lib/qingye-test/media',
    'PUBLIC_BASE_URL=http://152.136.252.204:5179',
    'ALLOWED_ORIGINS=http://152.136.252.204:5179',
    f"SESSION_SECRET={secrets['session_secret']}",
    'SECURE_COOKIES=false', 'SMS_MODE=disabled', 'MAX_UPLOAD_BYTES=10485760',
]) + '\n')

private_file('/etc/qingye-test/redis.conf', '\n'.join([
    'bind 127.0.0.1', 'protected-mode yes', 'port 6381',
    'dir /var/lib/qingye-test/redis', 'dbfilename qingye-test.rdb',
    'appendonly yes', 'appendfsync everysec', 'save 900 1',
    'maxmemory 64mb', 'maxmemory-policy noeviction', 'maxclients 100',
    'requirepass ' + secrets['redis_password'], 'daemonize no', 'logfile ""',
]) + '\n', 0o640)
subprocess.run(['chown', 'root:qingye-test', '/etc/qingye-test/redis.conf'], check=True)
private_file('/etc/systemd/system/qingye-test-redis.service', '''[Unit]
Description=QingYe isolated test Redis
After=network.target

[Service]
User=qingye-test
Group=qingye-test
ExecStart=/usr/bin/redis-server /etc/qingye-test/redis.conf
Restart=on-failure
RestartSec=3
MemoryMax=160M
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/qingye-test/redis
PrivateTmp=true

[Install]
WantedBy=multi-user.target
''', 0o644)
subprocess.run(['systemctl', 'daemon-reload'], check=True)
subprocess.run(['systemctl', 'enable', '--now', 'qingye-test-redis'], check=True)
print('Dedicated databases and Redis configured; credentials not printed.')
