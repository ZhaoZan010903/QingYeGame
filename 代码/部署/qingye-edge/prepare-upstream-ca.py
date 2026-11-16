#!/usr/bin/env python3
"""为旧 SaaS 上游补齐公开中间证书，不修改系统信任库或重载服务。

默认输出 /etc/qingye-edge/upstream-ca.pem，父目录必须已经存在。
--intermediate 接受已下载的单份 PEM；省略时仅从下面的官方 HTTPS 下载。
先核对 DER 指纹、有效期和原系统 CA 链，再原子写入系统 CA 的副本及中间证书。

来源：https://letsencrypt.org/certificates/
官方链：cn4.caihongjianzhan.com -> YR1 -> Root YR -> ISRG Root X1。
本次补齐的是 2026-05-13 签发、2032-09-02 到期的 Root YR-by-X1，
不是直接信任一个新根或上游叶证书；指纹变化须审查后更新脚本。
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import ssl
import stat
import subprocess
import tempfile
import urllib.parse
import urllib.request


OFFICIAL_URL = "https://letsencrypt.org/certs/gen-y/root-yr-by-x1.pem"
EXPECTED_DER_SHA256 = "072639d0b140d5bffae16ad9c3f6cc6086040621f51ee61a6d46a8915c07cf76"
DEFAULT_OUTPUT = Path("/etc/qingye-edge/upstream-ca.pem")
SYSTEM_CA_CANDIDATES = (
    Path("/etc/pki/tls/certs/ca-bundle.crt"),
    Path("/etc/ssl/certs/ca-certificates.crt"),
)
CERT_LIMIT = 64 * 1024
CA_LIMIT = 8 * 1024 * 1024
PEM_CERT = re.compile(
    rb"\s*-----BEGIN CERTIFICATE-----\s+([A-Za-z0-9+/=\s]+)"
    rb"-----END CERTIFICATE-----\s*"
)


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def read_bounded(path: Path, limit: int) -> bytes:
    if not path.is_file():
        raise ValueError(f"不是可读取的普通文件：{path}")
    with path.open("rb") as stream:
        data = stream.read(limit + 1)
    if not data or len(data) > limit:
        raise ValueError(f"文件为空或超过大小限制：{path}")
    return data


def find_system_ca(explicit: Path | None) -> Path:
    candidates = (explicit,) if explicit is not None else SYSTEM_CA_CANDIDATES
    for path in candidates:
        if path is not None and path.is_file():
            return path.resolve(strict=True)
    raise ValueError("未找到系统 CA，请使用 --system-ca 指定现有系统 CA bundle")


class OfficialRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        url = urllib.parse.urlsplit(newurl)
        if (url.scheme != "https" or url.hostname != "letsencrypt.org"
                or url.username or url.password or url.port not in (None, 443)):
            raise ValueError("官方证书下载被重定向到未允许的地址")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def download_intermediate(system_ca: Path) -> bytes:
    context = ssl.create_default_context(cafile=str(system_ca))
    opener = urllib.request.build_opener(
        urllib.request.ProxyHandler({}),
        urllib.request.HTTPSHandler(context=context),
        OfficialRedirect(),
    )
    request = urllib.request.Request(OFFICIAL_URL, headers={"User-Agent": "QingYe-CA-Prepare/1"})
    with opener.open(request, timeout=20) as response:
        if response.status != 200:
            raise ValueError("官方证书下载未返回 200")
        data = response.read(CERT_LIMIT + 1)
    if not data or len(data) > CERT_LIMIT:
        raise ValueError("官方证书响应为空或超过大小限制")
    return data


def openssl_run(program: str, *arguments: str, data: bytes) -> bytes:
    result = subprocess.run(
        [program, *arguments], input=data, capture_output=True, timeout=20, check=False
    )
    if result.returncode:
        detail = result.stderr.decode("utf-8", "replace").strip()[-1000:]
        raise ValueError(f"OpenSSL 证书校验失败：{detail}")
    return result.stdout


def validate_intermediate(pem: bytes, system_ca: Path, openssl: str) -> bytes:
    if len(pem) > CERT_LIMIT or PEM_CERT.fullmatch(pem) is None:
        raise ValueError("中间证书必须是单份 PEM，不能附带其他证书、私钥或文本")
    der = openssl_run(openssl, "x509", "-inform", "PEM", "-outform", "DER", data=pem)
    if digest(der) != EXPECTED_DER_SHA256:
        raise ValueError("中间证书 DER 指纹与审查版本不一致，拒绝加入 CA 副本")
    # 目标是已固定指纹的 CA，不能按服务端叶证书目的验证；上游叶证书
    # 的 sslserver 用途及域名仍由 Nginx 的 strict verify + SNI 检查。
    # 此处只用原系统 CA 验证，不能拿即将加入的证书自证可信。
    openssl_run(
        openssl, "verify", "-CAfile", str(system_ca), "-no-CApath",
        "-purpose", "any", data=pem,
    )
    openssl_run(openssl, "x509", "-noout", "-checkend", "86400", data=pem)
    return ssl.DER_cert_to_PEM_cert(der).encode("ascii")


def check_output(output: Path, system_ca: Path, intermediate: Path | None) -> None:
    if not output.is_absolute() or output.parent.resolve(strict=True) != output.parent:
        raise ValueError("输出须使用现有、无符号链接父目录下的绝对路径")
    if not output.parent.is_dir() or output.is_symlink():
        raise ValueError("输出父目录不存在或目标是符号链接")
    if output.exists() and not stat.S_ISREG(output.stat().st_mode):
        raise ValueError("输出目标不是普通文件")
    resolved = output.resolve()
    if resolved == system_ca or (intermediate is not None and resolved == intermediate.resolve()):
        raise ValueError("输出不能覆盖系统 CA 或输入中间证书")
    if output.exists():
        if os.path.samefile(output, system_ca):
            raise ValueError("输出不能与系统 CA 共用同一文件")
        if intermediate is not None and os.path.samefile(output, intermediate):
            raise ValueError("输出不能与输入中间证书共用同一文件")


def atomic_write(output: Path, data: bytes) -> bool:
    if output.exists() and read_bounded(output, CA_LIMIT + CERT_LIMIT) == data:
        return False
    descriptor, name = tempfile.mkstemp(prefix=f".{output.name}.", dir=output.parent)
    temporary = Path(name)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            os.fchmod(stream.fileno(), 0o644)
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, output)
        directory = os.open(output.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        temporary.unlink(missing_ok=True)
    return True


def prepare(intermediate: Path | None, output: Path, system_ca: Path | None,
            openssl: str) -> dict:
    source_ca = find_system_ca(system_ca)
    check_output(output, source_ca, intermediate)
    original_ca = read_bounded(source_ca, CA_LIMIT)
    if b"-----BEGIN CERTIFICATE-----" not in original_ca:
        raise ValueError("系统 CA bundle 不包含 PEM 证书")
    pem = (read_bounded(intermediate, CERT_LIMIT) if intermediate is not None
           else download_intermediate(source_ca))
    validated = validate_intermediate(pem, source_ca, openssl)
    # 防止验证期间系统 CA 更新导致输出与已验证的系统信任源不一致。
    if read_bounded(source_ca, CA_LIMIT) != original_ca:
        raise ValueError("验证期间系统 CA 已发生变化，请重新运行")
    bundle = original_ca.rstrip() + b"\n" + validated
    check_output(output, source_ca, intermediate)
    changed = atomic_write(output, bundle)
    return {
        "output": str(output),
        "changed": changed,
        "system_ca": str(source_ca),
        "system_ca_sha256": digest(original_ca),
        "intermediate_source": str(intermediate) if intermediate is not None else OFFICIAL_URL,
        "official_source": OFFICIAL_URL,
        "intermediate_der_sha256": EXPECTED_DER_SHA256,
        "system_chain_verified": True,
        "bundle_sha256": digest(bundle),
        "bytes": len(bundle),
    }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--intermediate", type=Path, help="已下载的官方 Root YR-by-X1 PEM")
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument("--system-ca", type=Path, help="现有系统 CA bundle，仅读取")
    parser.add_argument("--openssl", default="/usr/bin/openssl", help="用于校验的 OpenSSL 程序")
    args = parser.parse_args()
    try:
        result = prepare(args.intermediate, args.output, args.system_ca, args.openssl)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        parser.exit(1, f"准备失败：{error}\n")
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
