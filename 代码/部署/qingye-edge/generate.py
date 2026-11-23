#!/usr/bin/env python3
"""生成独立 Nginx 入口；不安装、不重启服务、不修改应用配置。"""
from __future__ import annotations

import argparse
import os
from pathlib import Path
import tempfile
import textwrap


IP = "8.140.230.149"
ACME_ROOT = "/var/lib/qingye-production/acme"
DOMAIN_CERT = "/etc/qingye-edge/letsencrypt/live/qingye-domains"
LEGACY_CERT = "/etc/qingye-edge/tls/legacy"
IP_CERT = "/etc/qingye-production/tls/current"
PUBLIC_HOST = "www.qingyegame.com"
ADMIN_HOST = "admin.qingyegame.com"
LEGACY_HOSTS = ("qingyeyouxi.com", "www.qingyeyouxi.com")


def tidy(value: str) -> str:
    return textwrap.dedent(value).strip()


def indent(value: str, width: int = 4) -> str:
    return textwrap.indent(value, " " * width)


def normalize_indent(value: str) -> str:
    """按实际配置块缩进；引号、转义和注释中的花括号不参与计数。"""
    level = 0
    result = []
    for original in value.splitlines():
        line = original.strip()
        if not line:
            result.append("")
            continue
        result.append("    " * max(0, level - int(line.startswith("}"))) + line)
        quote = None
        escaped = False
        for char in line:
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif quote:
                if char == quote:
                    quote = None
            elif char in ("'", '"'):
                quote = char
            elif char == "#":
                break
            elif char == "{":
                level += 1
            elif char == "}":
                level -= 1
                if level < 0:
                    raise ValueError("配置包含多余的闭合括号")
    if level != 0:
        raise ValueError("配置包含未闭合的配置块")
    return "\n".join(result) + "\n"


def listen(port: int, staging: bool, suffix: str = "") -> str:
    ports = {80: 18080, 443: 18443, 5179: 15179, 5181: 15181}
    address = f"127.0.0.1:{ports[port]}" if staging else f"0.0.0.0:{port}"
    return f"listen {address}{(' ' + suffix) if suffix else ''};"


def acme_location() -> str:
    # 仅接受 ACME 文件名字符，验证目录不能用来分发脚本或备份。
    return tidy(r'''
        location ^~ /.well-known/acme-challenge/ {
            if ($uri !~ "^/\.well-known/acme-challenge/[A-Za-z0-9_-]+$") { return 404; }
            limit_except GET { deny all; }
            root /var/lib/qingye-production/acme;
            default_type text/plain;
            add_header X-Content-Type-Options nosniff always;
            add_header Cache-Control no-store always;
            try_files $uri =404;
        }
    ''')


def certificates(path: str) -> str:
    return f"ssl_certificate {path}/fullchain.pem;\nssl_certificate_key {path}/privkey.pem;"


def http_redirect(host: str, destination: str, staging: bool, maintenance: bool = False) -> str:
    response = (
        'default_type text/plain; charset utf-8; add_header Retry-After 3600 always; return 503 "域名接入准备中，请稍后访问。\\n";'
        if maintenance else f"return 308 https://{destination}$request_uri;"
    )
    return tidy(f'''
        # begin http-{host}
        server {{
            {listen(80, staging)}
            server_name {host};
        {indent(acme_location())}
            location / {{ {response} }}
        }}
        # end http-{host}
    ''')


def forwarded_headers() -> str:
    return tidy('''
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-Proto $scheme;
        # 单一真实地址；禁止接受客户端传来的 XFF 链。
        proxy_set_header X-Forwarded-For $remote_addr;
    ''')


def app_server(surface: str, *, domain: bool, staging: bool) -> str:
    admin = surface == "admin"
    host = (ADMIN_HOST if admin else PUBLIC_HOST) if domain else IP
    port = (5181 if admin else 5179)
    api_port = 5182 if admin else 5180
    root = f"/var/www/qingye-production/{'admin' if admin else 'public'}/current"
    socket = "/api/v1/admin/chat/socket" if admin else "/api/v1/chat/socket"
    endpoint = host if domain else f"{IP}:{port}"
    cert = DOMAIN_CERT if domain else IP_CERT
    cache = "no-store" if admin else "$qingye_cache_control"
    referrer = "same-origin" if admin else "strict-origin-when-cross-origin"
    # WebSocket 保持页面同源；WebTransport 仍连接独立 Go UDP 的 IP 证书入口。
    csp = (
        "default-src 'self'; script-src 'self' https://challenges.cloudflare.com; "
        "frame-src https://challenges.cloudflare.com; style-src 'self' 'unsafe-inline'; "
        "img-src 'self' blob: data: https://qingye-images-20261.oss-cn-beijing.aliyuncs.com; "
        f"connect-src 'self' wss://{endpoint} https://{IP}:{port}; "
        "font-src 'self'; base-uri 'none'; form-action 'self'; object-src 'none'; frame-ancestors 'none'"
    )
    hsts = 'add_header Strict-Transport-Security "max-age=31536000" always;' if domain else ""
    public_deny = "location /api/v1/admin { return 404; }" if not admin else ""
    name = f"{'domain' if domain else 'ip'}-{surface}"
    return tidy(f'''
        # begin {name}
        server {{
            {listen(443 if domain else port, staging, 'ssl')}
            http2 on;
            server_name {host};
            if ($host != "{host}") {{ return 444; }}
        {indent(certificates(cert))}
            error_page 497 =308 https://{endpoint}$request_uri;
            root {root};
            index index.html;
            access_log /var/log/qingye-edge/{name}-access.log qingye;
            add_header X-Content-Type-Options nosniff always;
            add_header Referrer-Policy {referrer} always;
            add_header X-Frame-Options DENY always;
            {hsts}
            add_header Content-Security-Policy "{csp}" always;
            add_header Cache-Control {cache} always;
            location = /favicon.ico {{ access_log off; return 204; }}
            location /assets/ {{ try_files $uri =404; }}
            {public_deny}
            location = {socket} {{
                proxy_pass http://127.0.0.1:{api_port};
                proxy_http_version 1.1;
                proxy_set_header Upgrade $http_upgrade;
                proxy_set_header Connection "upgrade";
        {indent(forwarded_headers(), 8)}
                proxy_buffering off;
                proxy_connect_timeout 3s;
                proxy_read_timeout 75s;
                proxy_send_timeout 15s;
            }}
            location /api/ {{
                proxy_pass http://127.0.0.1:{api_port};
                proxy_http_version 1.1;
                # Go 先验证上传凭据与额度，不先落入 Nginx 请求临时文件。
                proxy_request_buffering off;
        {indent(forwarded_headers(), 8)}
                proxy_connect_timeout 3s;
                proxy_read_timeout 30s;
                proxy_send_timeout 30s;
            }}
            location / {{ try_files $uri $uri/ /index.html; }}
            location ~ /\\. {{ deny all; }}
        }}
        # end {name}
    ''')


def legacy_server(staging: bool, without_domains: bool) -> str:
    new_site = f"https://{IP}:5179/" if without_domains else f"https://{PUBLIC_HOST}/"
    # API/CDN 的 HTTPS 已核验；保留原路径与尾斜杠语义，固定上游身份并验证信任链。
    return tidy(r'''
        # begin legacy
        server {
            LISTEN_PLACEHOLDER
            http2 on;
            server_name qingyeyouxi.com www.qingyeyouxi.com;
            if ($host !~ "^(www\.)?qingyeyouxi\.com$") { return 444; }
            CERT_PLACEHOLDER
            root /www/wwwroot/qingyeyouxi.com;
            index index.html index.htm default.html default.htm;
            access_log /var/log/qingye-edge/legacy-access.log qingye;
            error_log /var/log/qingye-edge/legacy-error.log warn;
            error_page 404 /404.html;
            add_header X-Frame-Options SAMEORIGIN always;
            add_header X-Content-Type-Options nosniff always;
            add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
            add_header Referrer-Policy strict-origin-when-cross-origin always;
            add_header Permissions-Policy "geolocation=(), camera=(), microphone=(), usb=(), bluetooth=(), accelerometer=(), gyroscope=(), magnetometer=(), payment=(self), fullscreen=(self)" always;
            add_header Cache-Control $qingye_legacy_cache_control always;

            rewrite ^/article/(.*)$ /article_$1.html;
            rewrite ^/(\w*)/article/([^\/]*)$ /$1/article_$2.html;
            rewrite ^/product/(.*)$ /product_$1.html;
            rewrite ^/(\w*)/product/([^\/]*)$ /$1/product_$2.html;
            rewrite ^/article-category/(.*)$ /article_category_$1.html;
            rewrite ^/(\w*)/article-category/(.*)$ /$1/article_category_$2.html;
            rewrite ^/product-category/(.*)$ /product_category_$1.html;
            rewrite ^/(\w*)/product-category/(.*)$ /$1/product_category_$2.html;
            rewrite ^/(.*)/fenzhan/(.*)$ /$2.html;

            ACME_PLACEHOLDER
            location = /new { return 302 NEW_SITE_PLACEHOLDER; }
            location = /new/ { return 302 NEW_SITE_PLACEHOLDER; }
            location = /robots.txt {
                default_type text/plain;
                return 200 "User-Agent: *\nAllow: /";
            }
            location ^~ /api/ {
                proxy_pass https://cn4.caihongjianzhan.com;
                proxy_set_header Host cn4.caihongjianzhan.com;
                proxy_ssl_server_name on;
                proxy_ssl_name cn4.caihongjianzhan.com;
                proxy_ssl_verify on;
                proxy_ssl_verify_depth 4;
                proxy_ssl_trusted_certificate /etc/qingye-edge/upstream-ca.pem;
                proxy_ssl_protocols TLSv1.2 TLSv1.3;
                proxy_set_header X-Forward-For $remote_addr;
                proxy_set_header X-Forwarded-For $remote_addr;
                proxy_set_header X-Forwarded-Proto $scheme;
                proxy_connect_timeout 10s;
                proxy_read_timeout 60s;
            }
            location ^~ /jiaogeiwo_cdn/ {
                proxy_pass https://cdn.xuansiwei.com/;
                proxy_set_header Host cdn.xuansiwei.com;
                proxy_ssl_server_name on;
                proxy_ssl_name cdn.xuansiwei.com;
                proxy_ssl_verify on;
                proxy_ssl_verify_depth 4;
                proxy_ssl_trusted_certificate /etc/qingye-edge/upstream-ca.pem;
                proxy_ssl_protocols TLSv1.2 TLSv1.3;
                proxy_set_header X-Forwarded-For $remote_addr;
                proxy_connect_timeout 10s;
                proxy_read_timeout 60s;
                expires 365d;
            }
            location ~ /\. { return 404; }
            location ~* ^/(LICENSE|README(?:\.md)?|composer\.(?:json|lock)|package(?:-lock)?\.json)$ { return 404; }
            location ~* \.(?:php|phtml|phar|jsp|py|lua|sh|sql|bak)$ { return 404; }
            location ~* \.(?:gif|jpg|jpeg|png|bmp|swf)$ { expires 30d; access_log off; }
            location ~* \.(?:js|css)$ { expires 12h; access_log off; }
            location / { try_files $uri $uri/ =404; }
        }
        # end legacy
    ''').replace("LISTEN_PLACEHOLDER", listen(443, staging, "ssl")).replace(
        "CERT_PLACEHOLDER", certificates(LEGACY_CERT)
    ).replace("ACME_PLACEHOLDER", acme_location()).replace("NEW_SITE_PLACEHOLDER", new_site)


def generate(staging: bool = False, without_domains: bool = False) -> str:
    pid = "/run/qingye-edge/staging.pid" if staging else "/run/qingye-edge/nginx.pid"
    header = tidy(f'''
        # Generated by qingye-edge/generate.py. TCP only; Go owns WebTransport UDP.
        user www;
        worker_processes 2;
        pid {pid};
        error_log /var/log/qingye-edge/error.log warn;
        events {{ worker_connections 1024; }}
        http {{
            include /opt/qingye-edge/current/conf/mime.types;
            default_type application/octet-stream;
            server_tokens off;
            log_format qingye '$remote_addr [$time_local] "$request_method $uri" $status $body_bytes_sent $request_time $upstream_response_time';
            access_log /var/log/qingye-edge/access.log qingye;
            sendfile on;
            keepalive_timeout 30;
            client_max_body_size 11m;
            client_body_temp_path /var/lib/qingye-edge/client_temp;
            proxy_temp_path /var/lib/qingye-edge/proxy_temp;
            fastcgi_temp_path /var/lib/qingye-edge/fastcgi_temp;
            uwsgi_temp_path /var/lib/qingye-edge/uwsgi_temp;
            scgi_temp_path /var/lib/qingye-edge/scgi_temp;
            ssl_protocols TLSv1.2 TLSv1.3;
            ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305;
            ssl_prefer_server_ciphers off;
            ssl_session_cache shared:QingYeEdgeTLS:8m;
            ssl_session_timeout 10m;
            ssl_session_tickets off;
            gzip on;
            gzip_vary on;
            gzip_types text/css application/javascript application/json image/svg+xml text/plain;
            map $uri $qingye_cache_control {{
                default "no-store";
                ~^/assets/ "public, max-age=31536000, immutable";
            }}
            map $uri $qingye_legacy_cache_control {{
                default "";
                ~*\\.html$ "no-cache, no-store, must-revalidate";
                / "no-cache, no-store, must-revalidate";
            }}
    ''')
    blocks = [tidy(f'''
        # begin http-default
        server {{ {listen(80, staging, 'default_server')} server_name _; return 444; }}
        # end http-default
        # begin https-default
        server {{ {listen(443, staging, 'ssl default_server')} ssl_reject_handshake on; return 444; }}
        # end https-default
    ''')]
    for host in (PUBLIC_HOST, ADMIN_HOST, "qingyegame.com", *LEGACY_HOSTS, IP):
        target = f"{IP}:5179" if host == IP else PUBLIC_HOST if host == "qingyegame.com" else host
        maintenance = without_domains and host in (PUBLIC_HOST, ADMIN_HOST, "qingyegame.com")
        blocks.append(http_redirect(host, target, staging, maintenance))
    if not without_domains:
        blocks.append(tidy(f'''
        # begin apex-redirect
        server {{
            {listen(443, staging, 'ssl')}
            http2 on;
            server_name qingyegame.com;
            if ($host != "qingyegame.com") {{ return 444; }}
        {indent(certificates(DOMAIN_CERT))}
            return 308 https://{PUBLIC_HOST}$request_uri;
        }}
        # end apex-redirect
        '''))
    for surface in ("public", "admin"):
        for domain in (True, False):
            if domain and without_domains:
                continue
            blocks.append(app_server(surface, domain=domain, staging=staging))
    blocks.append(legacy_server(staging, without_domains))
    return normalize_indent(header + "\n\n" + "\n\n".join(indent(block) for block in blocks) + "\n}\n")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--staging", action="store_true", help="只监听回环临时端口，使用单独 PID")
    parser.add_argument("--without-domains", action="store_true", help="域名证书未就绪：仅运行旧站与 IP 入口，新域名 HTTP 返回 503，保留 ACME 路径")
    args = parser.parse_args()
    data = generate(args.staging, args.without_domains)
    # 原子替换配置文件；是否激活由部署流程执行 nginx -t 后决定。
    target = args.output.absolute()
    with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", newline="\n", dir=target.parent, prefix=".qingye-edge-", delete=False) as stream:
        temporary = Path(stream.name)
        try:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        except BaseException:
            temporary.unlink(missing_ok=True)
            raise
    try:
        os.chmod(temporary, 0o644)
        os.replace(temporary, target)
    finally:
        temporary.unlink(missing_ok=True)
    print(f"Generated {'staging' if args.staging else 'production'} configuration: {target}")


if __name__ == "__main__":
    main()
