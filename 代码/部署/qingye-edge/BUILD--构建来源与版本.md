# Nginx / OpenSSL 构建来源

本记录对应2026年11月16日8.140生产入口升级。程序安装于独立版本目录，没有覆盖宝塔二进制或系统OpenSSL。Go使用自身TLS实现，不依赖这份OpenSSL。

| 源材料 | 来源 | SHA-256 |
| --- | --- | --- |
| Nginx 1.30.6 | `https://nginx.org/download/nginx-1.30.6.tar.gz` | `3cf46f5ed7504d460125866b7bea99fb357f31d887e2e63b35503310e9b8b6c8` |
| OpenSSL 3.5.9 | `https://github.com/openssl/openssl/releases/download/openssl-3.5.9/openssl-3.5.9.tar.gz` | `603f5602e2eef00d77fbd429d34dcd5822bb301757a1bc9cdb24c670f1eb859a` |

源包散列和官方PGP分离签名已验证。Nginx签名者指纹为 `D6786CE303D9A9022998DC6CC8464D549AF75C0A`；OpenSSL使用官方发布公钥验证。原始签名回执保存在服务器私有运维目录，源包与二进制不提交Git。

编译器为阿里云Linux3的GCC10.2.1，使用已安装的PCRE2和zlib开发包；OpenSSL通过Nginx的 `--with-openssl` 静态构建。实际参数如下，OpenSSL源码路径按受控工作目录解析：

```text
--prefix=/opt/qingye-edge/releases/nginx-1.30.6-openssl-3.5.9
--user=www --group=www
--with-http_ssl_module --with-http_v2_module
--with-http_realip_module --with-http_stub_status_module
--with-http_gzip_static_module --with-http_sub_module --with-threads
--with-openssl=<受控工作目录>/openssl-3.5.9
--with-openssl-opt='no-tests --openssldir=/etc/qingye-edge/openssl'
--with-cc-opt='-O2 -fstack-protector-strong -D_FORTIFY_SOURCE=2'
--with-ld-opt='-Wl,-z,relro -Wl,-z,now'
```

本次使用单编译任务，设置CPU/内存上限，避免抢占现有站点资源。`ldd`检查没有动态依赖系统 `libssl` / `libcrypto`；实际运行二进制SHA为 `b2e911321a82584b5280dec69e000b297dea8f88e4e1a92c1df59ccaaae5b9d1`。不同构建路径或工具链可能产生不同散列，不能仅凭版本字符串替代源码与产物验证。

后续升级应使用新版本目录，先核验来源、编译和暂存入口，再切换 `current` 与服务。不得对当前运行目录直接执行覆盖安装。更新系统CA后，重新运行上游专用CA准备工具并检查Nginx配置；不要以关闭上游TLS校验处理证书链变化。
