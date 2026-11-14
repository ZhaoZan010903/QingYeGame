#!/usr/bin/python3.11
"""校验证书并原子切换，仅更新清叶正式站四个独立服务。

使用Python 3.11作为Certbot deploy hook。首次签发后用--install-only安装证书，
再启用站点；正常续期要求四个服务已运行。历史证书保留用于回退。
"""
import argparse
import fcntl
import grp
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


LINEAGE = Path('/etc/qingye-production/letsencrypt/live/qingye-production-ip')
BASE = Path('/etc/qingye-production/tls')
NGINX = '/www/server/nginx/sbin/nginx'
WEB_SERVICES = ('qingye-production-web.service', 'qingye-production-admin-web.service')
API_SERVICES = ('qingye-production-api.service', 'qingye-production-admin-api.service')
FILES = ('fullchain.pem', 'privkey.pem')


def run(*command):
    return subprocess.run(command, check=True, timeout=45, stdin=subprocess.DEVNULL)


def public_key(*command):
    return subprocess.check_output(command, timeout=15, stdin=subprocess.DEVNULL)


def validate_pair(directory):
    cert = str(directory / 'fullchain.pem')
    key = str(directory / 'privkey.pem')
    # 六天证书仍须至少有一天剩余有效期，拒绝临期或不属于本IP的材料。
    run('openssl', 'x509', '-in', cert, '-checkend', '86400', '-noout')
    run('openssl', 'x509', '-in', cert, '-checkip', '8.140.230.149', '-noout')
    run('openssl', 'verify', '-purpose', 'sslserver', '-verify_ip', '8.140.230.149', '-untrusted', cert, cert)
    cert_key = public_key('openssl', 'x509', '-in', cert, '-pubkey', '-noout')
    private_key_public_part = public_key('openssl', 'pkey', '-in', key, '-passin', 'pass:', '-pubout')
    if cert_key != private_key_public_part:
        raise RuntimeError('证书与私钥不匹配')


def sync_directory(directory):
    descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def point_to(target):
    temporary = BASE / 'current.next'
    if temporary.exists() and not temporary.is_symlink():
        raise RuntimeError('临时证书指针不是符号链接，拒绝覆盖')
    temporary.unlink(missing_ok=True)
    temporary.symlink_to(target.name)
    temporary.replace(BASE / 'current')
    sync_directory(BASE)


def check_nginx():
    for surface, config in (('public', 'nginx.conf'), ('admin', 'admin-nginx.conf')):
        run(NGINX, '-t', '-p', f'/var/lib/qingye-production/nginx/{surface}/', '-c', f'/etc/qingye-production/{config}')


def check_api_health():
    # 仅访问本机API，忽略环境中的HTTP代理。
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    for port in (5180, 5182):
        for attempt in range(10):
            try:
                with opener.open(f'http://127.0.0.1:{port}/healthz', timeout=2) as response:
                    if response.status == 200:
                        break
            except (OSError, urllib.error.URLError):
                pass
            if attempt == 9:
                raise RuntimeError(f'清叶API {port} 健康检查未通过')
            time.sleep(1)


def activate():
    check_nginx()
    for service in WEB_SERVICES:
        run('systemctl', 'reload', service)
    for service in API_SERVICES:
        run('systemctl', 'restart', service)
    for service in WEB_SERVICES + API_SERVICES:
        run('systemctl', 'is-active', '--quiet', service)
    check_api_health()


def restore(previous, restore_services=True):
    point_to(previous)
    if not restore_services:
        return
    errors = []
    # 尽力恢复全部清叶服务，不因单个服务失败跳过其余恢复操作。
    for action, services in (('reload', WEB_SERVICES), ('restart', API_SERVICES)):
        for service in services:
            try:
                run('systemctl', action, service)
            except Exception as error:
                errors.append(f'{service}: {error}')
    if not errors:
        try:
            check_api_health()
        except Exception as error:
            errors.append(str(error))
    if errors:
        raise RuntimeError('已恢复旧证书指针，但部分服务恢复失败：' + '; '.join(errors))


def install(install_only):
    if os.geteuid() != 0:
        raise RuntimeError('证书部署需要root权限')
    if Path(os.environ.get('RENEWED_LINEAGE', str(LINEAGE))) != LINEAGE:
        raise RuntimeError('拒绝处理其他站点的证书lineage')
    gid = grp.getgrnam('qingye-production').gr_gid
    BASE.mkdir(mode=0o750, parents=True, exist_ok=True)
    if BASE.is_symlink() or BASE.resolve() != BASE:
        raise RuntimeError('证书目录不在预定位置')
    os.chown(BASE, 0, gid)
    os.chmod(BASE, 0o750)
    with (BASE / '.deploy.lock').open('a') as lock:
        os.chmod(BASE / '.deploy.lock', 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        current = BASE / 'current'
        if current.exists() and not current.is_symlink():
            raise RuntimeError('当前证书入口不是符号链接，拒绝覆盖')
        previous = current.resolve(strict=True) if current.is_symlink() else None
        if previous is not None and (previous.parent != BASE or not previous.is_dir()):
            raise RuntimeError('原证书不在清叶专用目录，拒绝切换')
        if not install_only:
            if previous is None:
                raise RuntimeError('首次安装请先使用--install-only，再启动清叶服务')
            for service in WEB_SERVICES + API_SERVICES:
                run('systemctl', 'is-active', '--quiet', service)
        candidate = Path(tempfile.mkdtemp(prefix='cert-', dir=BASE))
        os.chown(candidate, 0, gid)
        os.chmod(candidate, 0o750)
        switched = False
        try:
            # 先固定成对副本，再验证；不直接把Certbot可变链接交给运行服务。
            for name in FILES:
                destination = candidate / name
                with (LINEAGE / name).open('rb') as source, destination.open('xb') as output:
                    os.fchmod(output.fileno(), 0o640)
                    os.fchown(output.fileno(), 0, gid)
                    shutil.copyfileobj(source, output)
                    output.flush()
                    os.fsync(output.fileno())
            sync_directory(candidate)
            validate_pair(candidate)
            if previous is not None and all((candidate / name).read_bytes() == (previous / name).read_bytes() for name in FILES):
                shutil.rmtree(candidate)
                print('清叶证书未变化，无需重载服务')
                return
            switched = True
            try:
                point_to(candidate)
                if not install_only:
                    activate()
            except Exception as activation_error:
                if previous is not None:
                    try:
                        restore(previous, restore_services=not install_only)
                    except Exception as rollback_error:
                        raise RuntimeError(f'证书启用失败：{activation_error}；{rollback_error}') from activation_error
                    raise RuntimeError('证书启用失败，已恢复原有证书状态') from activation_error
                raise RuntimeError('首次证书切换未完成，请检查保留的证书目录及current指针') from activation_error
        except Exception:
            # 切换后的目录保留供故障核对，避免清理仍被服务使用的证书。
            if not switched:
                shutil.rmtree(candidate)
            raise
    print('清叶正式站证书已安装；未输出私钥内容')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--install-only', action='store_true', help='仅安装成对证书，不操作服务')
    arguments = parser.parse_args()
    install(arguments.install_only)
