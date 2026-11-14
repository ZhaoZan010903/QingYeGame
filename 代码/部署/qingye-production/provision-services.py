#!/usr/bin/python3.11
"""Prepare isolated production data services; never starts the application APIs.

Requires Python 3.11, PostgreSQL 16, Redis, and the checksum-verified SeaweedFS binary.
Provider credentials are supplied in a root-only JSON file, never printed.
"""
import argparse
import base64
import grp
import hashlib
import json
import os
from pathlib import Path
import pwd
import secrets
import subprocess

BASE = Path('/var/lib/qingye-production')
ETC = Path('/etc/qingye-production')
PRIVATE = Path('/root/qingye-private/production-20261114')
PG = '/usr/pgsql-16/bin/'


def run(command, **kwargs):
    result = subprocess.run(command, capture_output=True, text=True, **kwargs)
    if result.returncode:
        # Service output and SQL errors can contain connection credentials.
        raise RuntimeError('command failed: ' + command[0])
    return result.stdout.strip()


def account(name):
    try:
        return pwd.getpwnam(name)
    except KeyError:
        run(['useradd', '--system', '--home-dir', '/nonexistent', '--shell', '/sbin/nologin', name])
        return pwd.getpwnam(name)


def directory(path, user='root', mode=0o750):
    path = Path(path)
    path.mkdir(parents=True, exist_ok=True)
    owner = pwd.getpwnam(user)
    os.chown(path, owner.pw_uid, owner.pw_gid)
    path.chmod(mode)


def write(path, content, user='root', group=None, mode=0o600):
    path = Path(path)
    # A rerun is allowed only for the exact same generated configuration.
    if path.exists() and path.read_text() != content:
        raise ValueError('existing configuration differs: ' + str(path))
    if not path.exists():
        path.write_text(content)
    os.chown(path, pwd.getpwnam(user).pw_uid, grp.getgrnam(group or user).gr_gid)
    path.chmod(mode)


def service(name, body):
    write('/etc/systemd/system/' + name + '.service', body, mode=0o644)


def psql(statement):
    return run(['runuser', '-u', 'postgres', '--', PG + 'psql', '-h', '/run/qingye-production-postgres',
                '-p', '5433', '-d', 'postgres', '-XAt', '-v', 'ON_ERROR_STOP=1'],
               input=statement, cwd='/tmp')


def main(args):
    if os.geteuid() != 0:
        raise ValueError('root required')
    os.umask(0o077)
    storage_binary = Path('/usr/local/lib/qingye-production/weed-4.53')
    with storage_binary.open('rb') as handle:
        if hashlib.file_digest(handle, 'sha256').hexdigest() != 'fa936e61bfb11f33c1f823045c4007e7da4b8b38bb6adc3b970f132d832e805b':
            raise ValueError('staging binary checksum mismatch or incomplete transfer')
    provider = json.loads(Path(args.provider_config).read_text())
    if provider.get('MEDIA_OSS_BUCKET') != 'qingye-images-20261':
        raise ValueError('unexpected provider bucket')
    for name in ['qingye-production', 'qingye-production-storage']:
        account(name)
    # Traverse-only access is necessary for the isolated PostgreSQL/storage users;
    # secret files and each service data directory retain their own restrictive modes.
    directory(ETC, mode=0o755)
    directory(BASE, mode=0o755)
    directory(PRIVATE, mode=0o700)
    secret_path = PRIVATE / 'service-secrets.json'
    if secret_path.exists():
        private = json.loads(secret_path.read_text())
    else:
        private = {key: secrets.token_urlsafe(36) for key in
                   ['database', 'redis', 'session_public', 'session_admin', 'stage_key', 'stage_secret', 'storage_admin']}
        private['product_key'] = provider.get('PRODUCT_CREDENTIAL_KEY') or base64.b64encode(secrets.token_bytes(32)).decode()
        secret_path.write_text(json.dumps(private, indent=2))
        secret_path.chmod(0o600)
    for name in ['media', 'redis']:
        directory(BASE / name, 'qingye-production', 0o700)
    directory(BASE / 'postgres', 'postgres', 0o700)
    directory(BASE / 'storage', 'qingye-production-storage', 0o700)
    # Capacity checks need traversal but not object reads across the two service identities.
    run(['usermod', '-a', '-G', 'qingye-production-storage', 'qingye-production'])
    (BASE / 'storage').chmod(0o750)
    if not (BASE / 'postgres/PG_VERSION').exists():
        run(['runuser', '-u', 'postgres', '--', PG + 'initdb', '-D', str(BASE / 'postgres'),
             '--encoding=UTF8', '--locale=C.UTF-8', '--auth-local=peer', '--auth-host=scram-sha-256'], cwd='/tmp')
    if (BASE / 'postgres/PG_VERSION').read_text().strip() != '16':
        raise ValueError('unexpected postgres cluster')
    config = """listen_addresses = '127.0.0.1'
port = 5433
unix_socket_directories = '/run/qingye-production-postgres'
max_connections = 40
# 首批约4.8万媒体键在同一导入事务中保留引用锁；不关闭媒体保护触发器。
max_locks_per_transaction = 2048
shared_buffers = 128MB
effective_cache_size = 512MB
work_mem = 4MB
maintenance_work_mem = 64MB
password_encryption = 'scram-sha-256'
log_statement = 'none'
log_min_error_statement = 'panic'
log_parameter_max_length_on_error = 0
"""
    write(ETC / 'postgresql.conf', config, group='postgres', mode=0o640)
    service('qingye-production-postgres', """[Unit]
Description=QingYe production PostgreSQL on loopback 5433
After=network.target
[Service]
Type=simple
User=postgres
Group=postgres
RuntimeDirectory=qingye-production-postgres
RuntimeDirectoryMode=0755
ExecStart=/usr/pgsql-16/bin/postgres -D /var/lib/qingye-production/postgres -c config_file=/etc/qingye-production/postgresql.conf
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
TimeoutStopSec=90
KillSignal=SIGINT
MemoryHigh=384M
MemoryMax=512M
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/qingye-production/postgres /run/qingye-production-postgres
[Install]
WantedBy=multi-user.target
""")
    run(['systemctl', 'daemon-reload'])
    run(['systemctl', 'enable', '--now', 'qingye-production-postgres'])
    # pg_isready polling is handled by systemd-independent readiness, never by starting an API.
    import time
    for _ in range(20):
        probe = subprocess.run([PG+'pg_isready','-h','127.0.0.1','-p','5433'], capture_output=True)
        if probe.returncode == 0:
            break
        time.sleep(0.25)
    if not psql("SELECT 1 FROM pg_roles WHERE rolname='qingye_production'"):
        psql("CREATE ROLE qingye_production LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '" + private['database'] + "'")
    if not psql("SELECT 1 FROM pg_database WHERE datname='qingye_production'"):
        psql('CREATE DATABASE qingye_production OWNER qingye_production')
    write(ETC / 'redis.conf', '\n'.join([
        'bind 127.0.0.1', 'protected-mode yes', 'port 6383', 'daemonize no', 'logfile ""',
        'dir /var/lib/qingye-production/redis', 'dbfilename qingye-production.rdb',
        'appendonly yes', 'appendfsync everysec', 'save 900 1', 'maxmemory 64mb',
        'maxmemory-policy noeviction', 'maxclients 128', 'requirepass '+private['redis']])+'\n',
        group='qingye-production', mode=0o640)
    service('qingye-production-redis', """[Unit]
Description=QingYe production Redis on loopback 6383
After=network.target
[Service]
User=qingye-production
Group=qingye-production
ExecStart=/usr/bin/redis-server /etc/qingye-production/redis.conf
Restart=on-failure
MemoryMax=128M
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/qingye-production/redis
[Install]
WantedBy=multi-user.target
""")
    write(ETC / 's3.json', json.dumps({'identities':[{'name':'qingye-production', 'credentials':[
        {'accessKey':private['stage_key'],'secretKey':private['stage_secret']}],
        'actions':['Read:qingye-production','Write:qingye-production','List:qingye-production','Tagging:qingye-production']}]}),
        group='qingye-production-storage', mode=0o640)
    options = ['dir=/var/lib/qingye-production/storage', 'ip=127.0.0.1', 'ip.bind=127.0.0.1',
        'master.telemetry=false', 'master.defaultReplication=000', 'master.volumeSizeLimitMB=512',
        'volume.max=32', 'volume.index=memory', 'volume.fileSizeLimitMB=64',
        'volume.compactionMBps=20', 'volume.maintenanceMBps=20', 'filer.port=8889',
        'filer.port.grpc=18889','filer.disableDirListing=true','filer.exposeDirectoryData=false',
        'filer.concurrentFileUploadLimit=4','filer.concurrentUploadLimitMB=128',
        's3.config=/etc/qingye-production/s3.json','s3.port.iceberg=0','s3.port.lance=0',
        's3.iam=false','s3.cacheCapacityMB=0','s3.readerCacheSizeMB=16',
        's3.concurrentFileUploadLimit=4','s3.concurrentUploadLimitMB=128','webdav=false',
        'admin.port.legacy=0','admin.worker.ip=127.0.0.1','admin.user=admin',
        'bucket=qingye-production', 'admin.password='+private['storage_admin']]
    write(ETC / 'mini.options', '\n'.join(options)+'\n', group='qingye-production-storage',mode=0o640)
    service('qingye-production-storage', """[Unit]
Description=QingYe private production media staging
After=network.target
[Service]
User=qingye-production-storage
Group=qingye-production-storage
WorkingDirectory=/var/lib/qingye-production/storage
Environment=GOMEMLIMIT=224MiB
Environment=GOGC=50
ExecStart=/usr/local/lib/qingye-production/weed-4.53 mini -options=/etc/qingye-production/mini.options
Restart=on-failure
RestartSec=5
TimeoutStopSec=45
UMask=0077
MemoryHigh=320M
MemoryMax=448M
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/qingye-production/storage
LimitNOFILE=65536
[Install]
WantedBy=multi-user.target
""")
    common = {key: provider[key] for key in ['SMS_ACCESS_KEY_ID','SMS_ACCESS_KEY_SECRET','SMS_SIGN_NAME',
        'SMS_REGISTER_TEMPLATE','TURNSTILE_SITE_KEY','TURNSTILE_SECRET','MEDIA_OSS_BUCKET',
        'MEDIA_OSS_ACCESS_KEY_ID','MEDIA_OSS_ACCESS_KEY_SECRET'] if key in provider}
    common.update(APP_ENV='production',SECURE_COOKIES='true',MEDIA_WORKERS_ENABLED='false',
        MEDIA_DIR=str(BASE/'media'), DATABASE_URL='postgres://qingye_production:'+private['database']+'@127.0.0.1:5433/qingye_production?sslmode=disable',
        REDIS_URL='redis://:'+private['redis']+'@127.0.0.1:6383/0', PRODUCT_CREDENTIAL_KEY=private['product_key'],
        MEDIA_R20_ENABLED='true', MEDIA_R20_ENVIRONMENT='production',MEDIA_LEGACY_OSS_READ_ENABLED='true',
        MEDIA_LEGACY_OBJECT_PREFIX='qingye-test/media/',MEDIA_LEGACY_OSS_DELETE_ENABLED='true',
        MEDIA_R20_MIN_FREE_BYTES='2147483648', MEDIA_STAGE_CAPACITY_PATH=str(BASE/'storage'),
        MEDIA_STAGE_ENDPOINT='http://127.0.0.1:8333',MEDIA_STAGE_BUCKET='qingye-production',
        MEDIA_STAGE_ACCESS_KEY_ID=private['stage_key'],MEDIA_STAGE_ACCESS_KEY_SECRET=private['stage_secret'],
        MEDIA_OSS_ENDPOINT='https://oss-cn-beijing-internal.aliyuncs.com',
        MEDIA_SAFETY_ENABLED='true',TURNSTILE_HOSTNAMES='8.140.230.149',
        SMS_ACCESS_MODE='public',SMS_ALLOWED_PHONES='',SMS_REGISTER_ENABLED='false',SMS_LOGIN_ENABLED='false',
        SMS_REGISTER_SCHEME='qingye-prod-register',SMS_LOGIN_SCHEME='qingye-prod-login',
        SMS_DAILY_LIMIT=provider.get('SMS_DAILY_LIMIT','20'),MAX_UPLOAD_BYTES='10485760',
        DB_MAX_CONNS='5', REDIS_POOL_SIZE='5',MAX_CONCURRENT_REQUESTS='64')
    for surface, port, web in [('public','5180','5179'),('admin','5182','5181')]:
        values = dict(common, APP_SURFACE=surface,QINGYE_ADDR='127.0.0.1:'+port,
            PUBLIC_BASE_URL='https://8.140.230.149:'+web, ALLOWED_ORIGINS='https://8.140.230.149:'+web,
            SESSION_SECRET=private['session_'+surface],CHAT_WT_ADDR='0.0.0.0:'+web,
            CHAT_WT_URL='https://8.140.230.149:'+web+'/chat',
            CHAT_WT_CERT='/etc/qingye-production/tls/current/fullchain.pem',
            CHAT_WT_KEY='/etc/qingye-production/tls/current/privkey.pem',CHAT_WT_PIN_CERTIFICATE='false')
        if any('\n' in value or '\r' in value or '"' in value or '\\' in value for value in values.values()):
            raise ValueError('environment contains unsupported characters')
        write(ETC/('app.env' if surface=='public' else 'admin.env'),
              ''.join(key+'="'+value+'"\n' for key,value in sorted(values.items())))
    run(['systemctl','daemon-reload'])
    run(['systemctl','enable','--now','qingye-production-redis','qingye-production-storage'])
    print(json.dumps({'data_services':'prepared','database_port':5433,'redis_port':6383,
                      'apis_started':False,'media_workers':False,'sms_sending':False}))


if __name__ == '__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--provider-config',required=True)
    options=parser.parse_args()
    try:
        main(options)
    except Exception as error:
        print(json.dumps({'status':'failed','type':type(error).__name__,
                          'reason':str(error) if isinstance(error,ValueError) else 'private diagnostics required'}))
        raise SystemExit(1)
