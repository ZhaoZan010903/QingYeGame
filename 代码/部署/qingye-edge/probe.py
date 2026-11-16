#!/usr/bin/env python3
"""仅 GET 检查独立入口；始终验证 HTTPS 信任链与目标名称，不登录、不提交业务数据。"""
from __future__ import annotations

import argparse
import base64
import http.client
import http.cookies
import json
import re
import socket
import ssl
import sys
import uuid

from generate import IP, PUBLIC_HOST, ADMIN_HOST, LEGACY_HOSTS

PROTOCOL = "qy-r24-aesgcm-v1"
INIT_PATH = "/api/v1/response-crypto/init"
WIRE_TYPE = "application/vnd.qingye.encrypted"
MAX_BODY = 4 * 1024 * 1024


class DirectHTTPSConnection(http.client.HTTPSConnection):
    def __init__(self, hostname, port, connect_host):
        super().__init__(hostname, port, timeout=12, context=ssl.create_default_context())
        self.connect_host = connect_host

    def connect(self):
        raw = socket.create_connection((self.connect_host, self.port), self.timeout)
        try:
            self.sock = self._context.wrap_socket(raw, server_hostname=self.host)
        except BaseException:
            raw.close()
            raise


def request(host, port, path, connect_host, tls=True, extra_headers=None, cookie_jar=None):
    connection = DirectHTTPSConnection(host, port, connect_host) if tls else http.client.HTTPConnection(connect_host, port, timeout=12)
    try:
        sent = {"Host": host, "Connection": "close", **(extra_headers or {})}
        if cookie_jar:
            sent["Cookie"] = "; ".join(f"{name}={value}" for name, value in cookie_jar.items())
        connection.request("GET", path, headers=sent)
        response = connection.getresponse()
        raw_headers = response.getheaders()
        status, headers = response.status, {k.lower(): v for k, v in raw_headers}
        if cookie_jar is not None:
            for name, value in raw_headers:
                if name.lower() == "set-cookie":
                    parsed = http.cookies.SimpleCookie()
                    parsed.load(value)
                    cookie_jar.update({key: item.value for key, item in parsed.items()})
        body = response.read(MAX_BODY + 1)
        if len(body) > MAX_BODY:
            raise ValueError("response exceeds probe body limit")
        return status, headers, body
    finally:
        connection.close()


def unbase64(value):
    if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z0-9_-]+", value):
        raise ValueError("invalid R24 base64 framing")
    return base64.b64decode(value + "=" * (-len(value) % 4), altchars=b"-_", validate=True)


def decrypt_response(body, headers, status, path, context, request_id):
    """沿用 R24 GET 协议；认证请求路径/状态/AAD，不把 428 当业务验收通过。"""
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM
    expected = {"x-qy-protocol": PROTOCOL, "x-qy-context": context["context_id"],
                "x-qy-key-id": context["key_id"], "x-qy-request-id": request_id}
    if any(headers.get(name) != value for name, value in expected.items()):
        raise ValueError("encrypted response binding mismatch")
    if headers.get("content-type") != WIRE_TYPE:
        raise ValueError("encrypted response content type missing")
    original = headers.get("x-qy-content-type", "")
    if not original or len(original) > 200 or "\r" in original or "\n" in original:
        raise ValueError("invalid R24 original content type")
    # 此检查客户端不协商 gzip，只接受 identity，避免偷偷放宽解压/公开路径规则。
    if headers.get("x-qy-content-encoding") != "identity":
        raise ValueError("unnegotiated encrypted response encoding")
    key, nonce = unbase64(context["key"]), unbase64(headers.get("x-qy-nonce"))
    if len(key) != 32 or len(nonce) != 12 or not 16 <= len(body) <= MAX_BODY:
        raise ValueError("invalid encrypted response length")
    aad = "\n".join(["QY-R24-HTTP", "1", context["surface"], context["context_id"],
                     context["key_id"], request_id, "GET", path, str(status), original, "identity"]).encode()
    plaintext = AESGCM(key).decrypt(nonce, body, aad)
    if original.split(";", 1)[0].strip() != "application/json":
        raise ValueError("API probe expected authenticated JSON")
    return json.loads(plaintext)


def encrypted_get(host, port, path, connect_host, surface):
    # 独立匿名 Cookie 仅保存在这次 GET 检查的内存里，不读取或保存会员/管理员凭据。
    cookies = {}
    status, _, body = request(host, port, INIT_PATH, connect_host,
                             extra_headers={"X-QY-Init": "1", "Accept": "application/json"}, cookie_jar=cookies)
    if status != 200:
        raise ValueError(f"R24 initialization HTTP {status}")
    context = json.loads(body).get("data", {})
    if (context.get("enabled") is not True or context.get("protocol") != PROTOCOL
            or context.get("surface") != surface):
        raise ValueError("R24 initialization policy mismatch")
    for field in ("context_id", "key_id"):
        if not isinstance(context.get(field), str) or not re.fullmatch(r"[A-Za-z0-9_-]{1,160}", context[field]):
            raise ValueError("invalid R24 context identifier")
    if len(unbase64(context.get("key"))) != 32:
        raise ValueError("invalid R24 initialization key")
    request_id = uuid.uuid4().hex
    headers = {"X-QY-Protocol": PROTOCOL, "X-QY-Context": context["context_id"],
               "X-QY-Key-Id": context["key_id"], "X-QY-Request-Id": request_id}
    status, received, body = request(host, port, path, connect_host, extra_headers=headers, cookie_jar=cookies)
    data = decrypt_response(body, received, status, path, context, request_id)
    return status, data


def run(connect_host, staging=False, without_domains=False):
    http_port, tls_port, public_ip_port, admin_ip_port = (18080, 18443, 15179, 15181) if staging else (80, 443, 5179, 5181)
    outcomes = []

    def check(name, action):
        try:
            action()
        except Exception as error:
            outcomes.append({"check": name, "ok": False, "error": f"{type(error).__name__}: {error}"})
        else:
            outcomes.append({"check": name, "ok": True})

    def equal(actual, expected):
        if actual != expected:
            raise AssertionError(f"expected {expected!r}, got {actual!r}")

    targets = [(IP, public_ip_port, "清叶", "public"), (IP, admin_ip_port, "清叶", "admin")]
    if not without_domains:
        targets += [(PUBLIC_HOST, tls_port, "清叶", "public"), (ADMIN_HOST, tls_port, "清叶", "admin")]
    for host, port, title, surface in targets:
        def page_check(host=host, port=port, title=title, surface=surface):
            status, headers, body = request(host, port, "/", connect_host)
            equal(status, 200)
            equal(headers.get("x-content-type-options"), "nosniff")
            equal(headers.get("x-frame-options"), "DENY")
            csp = headers.get("content-security-policy", "")
            wt_port = 5181 if surface == "admin" else 5179
            if f"https://{IP}:{wt_port}" not in csp:
                raise AssertionError("missing explicit WebTransport origin")
            if title not in body.decode("utf-8"):
                raise AssertionError("unexpected page title/content")
            if surface == "admin":
                equal(headers.get("cache-control"), "no-store")
            # 验证同一个页面引用的构建文件存在，避免入口 200 而资源 404。
            asset = re.search(rb'(?:src|href)="(/assets/[^"?]+\.(?:js|css))', body)
            if asset is None:
                raise AssertionError("page has no build asset")
            status, asset_headers, _ = request(host, port, asset.group(1).decode(), connect_host)
            equal(status, 200)
            if surface == "public" and "immutable" not in asset_headers.get("cache-control", ""):
                raise AssertionError("missing immutable public asset cache")
        check(f"page+asset+headers {host}:{port}", page_check)
        check(f"hidden-file denied {host}:{port}", lambda h=host, p=port: equal(request(h, p, "/.git/config", connect_host)[0], 403))
        check(f"SPA fallback {host}:{port}", lambda h=host, p=port: equal(request(h, p, "/login", connect_host)[0], 200))
        if surface == "public":
            check(f"admin API blocked {host}:{port}", lambda h=host, p=port: equal(request(h, p, "/api/v1/admin/products", connect_host)[0], 404))
            check(f"public auth capability (R24 authenticated) {host}:{port}", lambda h=host, p=port: equal(encrypted_get(h, p, "/api/v1/auth/capabilities", connect_host, "public")[0], 200))
        else:
            check(f"admin auth enforced (R24 authenticated) {host}:{port}", lambda h=host, p=port: equal(encrypted_get(h, p, "/api/v1/admin/products", connect_host, "admin")[0], 401))

    for host in LEGACY_HOSTS:
        def legacy_check(host=host):
            status, headers, body = request(host, tls_port, "/", connect_host)
            equal(status, 200)
            equal(headers.get("x-content-type-options"), "nosniff")
            equal(headers.get("x-frame-options"), "SAMEORIGIN")
            if "飞衡手游交易服务平台" not in body.decode("utf-8"):
                raise AssertionError("legacy title changed")
        check(f"legacy page {host}", legacy_check)
        check(f"legacy hidden-file denied {host}", lambda h=host: equal(request(h, tls_port, "/.git/config", connect_host)[0], 404))

    hosts = [PUBLIC_HOST, ADMIN_HOST, "qingyegame.com", *LEGACY_HOSTS, IP]
    for host in hosts:
        def http_check(host=host):
            status, headers, _ = request(host, http_port, "/login?edge_probe=1", connect_host, tls=False)
            if without_domains and host in [PUBLIC_HOST, ADMIN_HOST, "qingyegame.com"]:
                equal(status, 503)
            else:
                equal(status, 308)
                target = f"{IP}:5179" if host == IP else PUBLIC_HOST if host == "qingyegame.com" else host
                equal(headers.get("location"), f"https://{target}/login?edge_probe=1")
            equal(request(host, http_port, "/.well-known/acme-challenge/qingye-readonly-nonexistent", connect_host, tls=False)[0], 404)
            equal(request(host, http_port, "/.well-known/acme-challenge/forbidden.php", connect_host, tls=False)[0], 404)
        check(f"HTTP redirect/maintenance and ACME {host}", http_check)

    def unknown_http():
        try:
            request("unconfigured.invalid", http_port, "/", connect_host, tls=False)
        except (http.client.RemoteDisconnected, ConnectionResetError):
            return
        raise AssertionError("unknown HTTP host was served")
    check("unknown HTTP host rejected", unknown_http)

    def unknown_tls(host="unconfigured.invalid"):
        try:
            request(host, tls_port, "/", connect_host)
        except ssl.SSLCertVerificationError:
            raise AssertionError("wrong certificate offered instead of rejecting TLS")
        except ssl.SSLError:
            return
        raise AssertionError("unknown TLS name was served")
    check("unknown TLS name rejected", unknown_tls)
    if without_domains:
        check("unissued domain certificate never substituted", lambda: unknown_tls(PUBLIC_HOST))
    else:
        def apex():
            status, headers, _ = request("qingyegame.com", tls_port, "/?edge_probe=1", connect_host)
            equal(status, 308)
            equal(headers.get("location"), "https://www.qingyegame.com/?edge_probe=1")
        check("apex HTTPS canonical redirect", apex)
    return outcomes


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--connect-host", default="127.0.0.1", help="连接到此地址，SNI/Host 始终按实际站点设置")
    parser.add_argument("--staging", action="store_true")
    parser.add_argument("--without-domains", action="store_true")
    args = parser.parse_args()
    results = run(args.connect_host, args.staging, args.without_domains)
    print(json.dumps({"passed": sum(x["ok"] for x in results), "total": len(results), "checks": results}, ensure_ascii=False, indent=2))
    return 0 if all(x["ok"] for x in results) else 1


if __name__ == "__main__":
    sys.exit(main())
