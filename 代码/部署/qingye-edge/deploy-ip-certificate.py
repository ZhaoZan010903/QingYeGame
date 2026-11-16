#!/usr/bin/python3.11
"""原子部署现有 IP 证书，重载独立入口并刷新两端 WebTransport TLS。

继续使用生产 IP lineage，不修改域名或旧站证书。--install-only 只供服务
尚未启动时首次安装；已有证书相同则不操作服务。历史证书保留用于回退。
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
NGINX = '/opt/qingye-edge/current/sbin/nginx'
EDGE_SERVICE = 'qingye-edge.service'
API_SERVICES = ('qingye-production-api.service', 'qingye-production-admin-api.service')
SERVICES = (EDGE_SERVICE,) + API_SERVICES
FILES = ('fullchain.pem', 'privkey.pem')


def run(*command):
    return subprocess.run(command, check=True, timeout=45, stdin=subprocess.DEVNULL)


def public_key(*command):
    return subprocess.check_output(command, timeout=15, stdin=subprocess.DEVNULL)


def validate_pair(directory):
    cert, key = (str(directory / name) for name in FILES)
    run('openssl', 'x509', '-in', cert, '-checkend', '86400', '-noout')
    run('openssl', 'x509', '-in', cert, '-checkip', '8.140.230.149', '-noout')
    run('openssl', 'verify', '-purpose', 'sslserver', '-verify_ip',
        '8.140.230.149', '-untrusted', cert, cert)
    cert_key = public_key('openssl', 'x509', '-in', cert, '-pubkey', '-noout')
    key_public = public_key('openssl', 'pkey', '-in', key, '-passin', 'pass:', '-pubout')
    if cert_key != key_public:
        raise RuntimeError('证书与私钥不匹配')


def sync_directory(directory):
    descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def previous_certificate():
    current = BASE / 'current'
    if current.exists() and not current.is_symlink():
        raise RuntimeError('当前证书入口不是符号链接，拒绝覆盖')
    previous = current.resolve(strict=True) if current.is_symlink() else None
    if previous is not None and (previous.parent != BASE or not previous.is_dir()):
        raise RuntimeError('原证书不在清叶专用目录，拒绝切换')
    return previous


def point_to(target):
    if target.parent != BASE or target.is_symlink() or not target.is_dir():
        raise RuntimeError('目标证书不在清叶专用目录，拒绝切换')
    temporary = BASE / 'current.next'
    if temporary.exists() and not temporary.is_symlink():
        raise RuntimeError('临时证书指针不是符号链接，拒绝覆盖')
    temporary.unlink(missing_ok=True)
    temporary.symlink_to(target.name)
    temporary.replace(BASE / 'current')
    sync_directory(BASE)


def check_nginx():
    run(NGINX, '-t', '-p', '/var/lib/qingye-edge/', '-c', '/etc/qingye-edge/nginx.conf')


def check_api_health():
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
                raise RuntimeError(f'清叶 API {port} 健康检查未通过')
            time.sleep(1)


def activate():
    check_nginx()
    run('systemctl', 'reload', EDGE_SERVICE)
    for service in API_SERVICES:
        run('systemctl', 'restart', service)
    for service in SERVICES:
        run('systemctl', 'is-active', '--quiet', service)
    check_api_health()


def restore(previous, restore_services=True):
    point_to(previous)
    if not restore_services:
        return
    errors = []
    try:
        check_nginx()
        run('systemctl', 'reload', EDGE_SERVICE)
    except Exception as error:
        errors.append(f'{EDGE_SERVICE}: {error}')
    # 即使入口恢复失败，仍尝试把两端 WT 恢复到旧证书，避免混合状态。
    for service in API_SERVICES:
        try:
            run('systemctl', 'restart', service)
        except Exception as error:
            errors.append(f'{service}: {error}')
    for service in SERVICES:
        try:
            run('systemctl', 'is-active', '--quiet', service)
        except Exception as error:
            errors.append(f'{service}: {error}')
    try:
        check_api_health()
    except Exception as error:
        errors.append(str(error))
    if errors:
        raise RuntimeError('已恢复旧证书指针，但部分服务恢复失败：' + '; '.join(errors))


def any_services_active():
    # activating/reloading 也不能使用无服务刷新模式。
    for service in SERVICES:
        result = subprocess.run(('systemctl', 'show', '-p', 'ActiveState', '--value', service),
                                check=True, timeout=15, stdin=subprocess.DEVNULL,
                                stdout=subprocess.PIPE, text=True)
        if result.stdout.strip() not in ('inactive', 'failed'):
            return True
    return False


def install_locked(gid, install_only):
    previous = previous_certificate()
    if install_only:
        if any_services_active():
            raise RuntimeError('--install-only 仅供三个服务均未运行时使用')
    else:
        if previous is None:
            raise RuntimeError('首次安装请先停止相关服务并使用 --install-only')
        for service in SERVICES:
            run('systemctl', 'is-active', '--quiet', service)
    candidate = Path(tempfile.mkdtemp(prefix='cert-', dir=BASE))
    os.chown(candidate, 0, gid)
    os.chmod(candidate, 0o750)
    switched = False
    try:
        for name in FILES:
            with (LINEAGE / name).open('rb') as source, (candidate / name).open('xb') as output:
                os.fchmod(output.fileno(), 0o640)
                os.fchown(output.fileno(), 0, gid)
                shutil.copyfileobj(source, output)
                output.flush()
                os.fsync(output.fileno())
        sync_directory(candidate)
        validate_pair(candidate)
        if previous is not None and all((candidate / name).read_bytes() == (previous / name).read_bytes() for name in FILES):
            shutil.rmtree(candidate)
            print('清叶 IP 证书未变化，无需重载服务')
            return
        # point_to 最后一步 fsync 失败时链接也可能已切换，必须尝试回退。
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
            raise RuntimeError('首次证书切换未完成，请检查保留的证书目录及 current 指针') from activation_error
    except Exception:
        if not switched:
            shutil.rmtree(candidate)
        raise
    print('清叶 IP 证书已安装；未输出私钥内容')


def install(install_only=False):
    if os.geteuid() != 0:
        raise RuntimeError('证书部署需要 root 权限')
    if Path(os.environ.get('RENEWED_LINEAGE', str(LINEAGE))) != LINEAGE:
        raise RuntimeError('拒绝处理其他站点的证书 lineage')
    gid = grp.getgrnam('qingye-production').gr_gid
    BASE.mkdir(mode=0o750, parents=True, exist_ok=True)
    if BASE.is_symlink() or BASE.resolve() != BASE:
        raise RuntimeError('证书目录不在预定位置')
    os.chown(BASE, 0, gid)
    os.chmod(BASE, 0o750)
    descriptor = os.open(BASE / '.deploy.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, 'a') as lock:
        os.fchmod(lock.fileno(), 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        install_locked(gid, install_only)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--install-only', action='store_true', help='仅供相关服务全部停止时安装，不操作服务')
    install(parser.parse_args().install_only)
