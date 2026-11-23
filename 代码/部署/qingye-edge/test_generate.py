"""离线检查入口边界；真实 nginx -t 与 HTTP/TLS 检查在部署主机另行执行。"""
import re
import unittest

from generate import generate


def block(config, name):
    return config.split(f"# begin {name}\n", 1)[1].split(f"# end {name}", 1)[0]


class EdgeConfigTests(unittest.TestCase):
    def setUp(self):
        self.config = generate()

    def test_staging_never_binds_public_interfaces_or_live_pid(self):
        stage = generate(staging=True)
        listeners = re.findall(r"\blisten\s+([^;]+);", stage)
        self.assertTrue(listeners)
        self.assertTrue(all(item.startswith("127.0.0.1:") for item in listeners))
        self.assertEqual({item.split()[0] for item in listeners}, {
            "127.0.0.1:18080", "127.0.0.1:18443", "127.0.0.1:15179", "127.0.0.1:15181"
        })
        self.assertIn("pid /run/qingye-edge/staging.pid;", stage)
        self.assertNotIn("pid /run/qingye-edge/nginx.pid;", stage)

    def test_only_expected_tcp_ports_and_no_nginx_quic(self):
        addresses = re.findall(r"\blisten\s+([^\s;]+)", self.config)
        self.assertEqual(set(addresses), {"0.0.0.0:80", "0.0.0.0:443", "0.0.0.0:5179", "0.0.0.0:5181"})
        self.assertNotRegex(self.config, r"listen[^;]+\bquic\b")

    def test_unknown_hosts_are_rejected(self):
        self.assertIn("default_server", block(self.config, "http-default"))
        self.assertIn("return 444", block(self.config, "http-default"))
        self.assertIn("ssl_reject_handshake on", block(self.config, "https-default"))
        for name in ("domain-public", "domain-admin", "ip-public", "ip-admin"):
            self.assertIn("if ($host !=", block(self.config, name))

    def test_http_redirects_never_reflect_host(self):
        for host in ("www.qingyegame.com", "admin.qingyegame.com", "qingyegame.com", "qingyeyouxi.com", "www.qingyeyouxi.com", "8.140.230.149"):
            section = block(self.config, "http-" + host)
            self.assertIn("location ^~ /.well-known/acme-challenge/", section)
            self.assertIn("try_files $uri =404", section)
            self.assertIn("[A-Za-z0-9_-]+$", section)
            self.assertIn("limit_except GET", section)
            self.assertNotIn("https://$host", section)
        self.assertIn("https://www.qingyegame.com$request_uri", block(self.config, "apex-redirect"))

    def test_new_hosts_use_domain_cert_and_ip_hosts_keep_ip_cert(self):
        for surface in ("public", "admin"):
            self.assertIn("/letsencrypt/live/qingye-domains/fullchain.pem", block(self.config, "domain-" + surface))
            self.assertIn("/etc/qingye-production/tls/current/fullchain.pem", block(self.config, "ip-" + surface))

    def test_frontends_cannot_be_sent_to_other_surface_backend(self):
        for prefix in ("domain-", "ip-"):
            public = block(self.config, prefix + "public")
            admin = block(self.config, prefix + "admin")
            self.assertIn("location /api/v1/admin { return 404; }", public)
            self.assertEqual(public.count("proxy_pass http://127.0.0.1:5180;"), 2)
            self.assertNotIn("127.0.0.1:5182", public)
            self.assertEqual(admin.count("proxy_pass http://127.0.0.1:5182;"), 2)
            self.assertNotIn("127.0.0.1:5180", admin)

    def test_same_origin_ws_and_explicit_cross_origin_wt(self):
        for surface, host, port in (("public", "www.qingyegame.com", 5179), ("admin", "admin.qingyegame.com", 5181)):
            section = block(self.config, "domain-" + surface)
            self.assertIn(f"connect-src 'self' wss://{host} https://8.140.230.149:{port}", section)
            self.assertIn('proxy_set_header Upgrade $http_upgrade;', section)
            self.assertIn('proxy_set_header Connection "upgrade";', section)
            self.assertIn("proxy_read_timeout 75s;", section)

    def test_security_headers_do_not_depend_on_baota(self):
        self.assertNotIn("/www/server/", self.config)
        self.assertNotIn("lua_package_path", self.config)
        self.assertNotIn("site_total", self.config)
        self.assertIn("/opt/qingye-edge/current/conf/mime.types", self.config)
        self.assertIn("ssl_protocols TLSv1.2 TLSv1.3;", self.config)
        self.assertIn("ssl_session_tickets off;", self.config)

    def test_upload_and_client_ip_preserve_security_budget(self):
        for name in ("domain-public", "domain-admin", "ip-public", "ip-admin"):
            section = block(self.config, name)
            self.assertEqual(section.count("proxy_set_header X-Forwarded-For $remote_addr;"), 2)
            self.assertNotIn("$proxy_add_x_forwarded_for", section)
            self.assertIn("proxy_request_buffering off;", section)
            self.assertIn("location ~ /\\. { deny all; }", section)

    def test_query_tickets_are_not_in_access_log(self):
        log_format = re.search(r"log_format qingye ([^;]+);", self.config).group(1)
        self.assertIn("$uri", log_format)
        for field in ("$request_uri", "$request ", "$args", "$query_string", "$http_cookie"):
            self.assertNotIn(field, log_format)

    def test_legacy_api_and_rewrite_contract_are_retained(self):
        legacy = block(self.config, "legacy")
        self.assertIn("root /www/wwwroot/qingyeyouxi.com;", legacy)
        self.assertIn("proxy_pass https://cn4.caihongjianzhan.com;", legacy)
        self.assertIn("proxy_pass https://cdn.xuansiwei.com/;", legacy)
        self.assertIn("proxy_set_header X-Forward-For $remote_addr;", legacy)
        self.assertEqual(len(re.findall(r"^\s*rewrite ", legacy, re.M)), 9)
        self.assertNotIn("fastcgi_pass", legacy)

    def test_legacy_upstreams_require_named_verified_https_and_keep_uri_semantics(self):
        legacy = block(self.config, "legacy")
        for location, host, suffix in (("/api/", "cn4.caihongjianzhan.com", ""),
                                       ("/jiaogeiwo_cdn/", "cdn.xuansiwei.com", "/")):
            section = legacy.split(f"location ^~ {location} {{", 1)[1].split("}", 1)[0]
            # API 传递完整 /api/...；CDN 用 / 替换 /jiaogeiwo_cdn/ 前缀。
            self.assertEqual(re.findall(r"proxy_pass ([^;]+);", section), [f"https://{host}{suffix}"])
            self.assertIn(f"proxy_set_header Host {host};", section)
            self.assertIn("proxy_ssl_server_name on;", section)
            self.assertIn(f"proxy_ssl_name {host};", section)
            self.assertIn("proxy_ssl_verify on;", section)
            self.assertIn("proxy_ssl_verify_depth 4;", section)
            self.assertIn("proxy_ssl_trusted_certificate /etc/qingye-edge/upstream-ca.pem;", section)
            self.assertIn("proxy_ssl_protocols TLSv1.2 TLSv1.3;", section)
            self.assertNotIn("proxy_ssl_verify off", section)
            self.assertNotIn("http://", section)

    def test_legacy_sensitive_files_are_denied_before_static_regex(self):
        legacy = block(self.config, "legacy")
        self.assertLess(legacy.index("location ~ /\\."), legacy.index("gif|jpg|jpeg"))
        self.assertIn("php|phtml|phar|jsp|py|lua|sh|sql|bak", legacy)
        self.assertIn("add_header Cache-Control $qingye_legacy_cache_control always;", legacy)

    def test_missing_domain_certificate_mode_keeps_legacy_and_ip_working(self):
        config = generate(without_domains=True)
        self.assertNotIn("/etc/qingye-edge/letsencrypt/live/qingye-domains", config)
        for name in ("domain-public", "domain-admin", "apex-redirect"):
            self.assertNotIn("# begin " + name + "\n", config)
        for name in ("ip-public", "ip-admin", "legacy"):
            self.assertIn("ssl_certificate ", block(config, name))
        for host in ("www.qingyegame.com", "admin.qingyegame.com", "qingyegame.com"):
            section = block(config, "http-" + host)
            self.assertIn("return 503", section)
            self.assertIn("location ^~ /.well-known/acme-challenge/", section)
        self.assertIn("return 308", block(config, "http-www.qingyeyouxi.com"))

    def test_legacy_new_site_redirect_uses_public_entry_for_active_mode(self):
        for without_domains, target in (
            (True, "https://8.140.230.149:5179/"),
            (False, "https://www.qingyegame.com/"),
        ):
            with self.subTest(without_domains=without_domains):
                config = generate(without_domains=without_domains)
                for path in ("/new", "/new/"):
                    rule = f"location = {path} {{ return 302 {target}; }}"
                    self.assertIn(rule, block(config, "legacy"))
                    self.assertEqual(config.count(rule), 1)


if __name__ == "__main__":
    unittest.main()
