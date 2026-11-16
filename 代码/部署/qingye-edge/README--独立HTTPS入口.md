# 独立 HTTPS 入口

本目录维护生产服务器的独立 Nginx 配置，不操作数据库和业务内容。配置生成、软件安装、证书签发和服务切换分开执行；生成成功不代表已部署。

**本轮采用 `--without-domains`：用户已确认新域名尚未备案。** 独立入口先承接旧站与现有 IP 入口；新域名仅准备配置，不启用新域名 HTTPS，不切换应用环境中的 Origin、Turnstile 或管理端跳转地址。新域名证书续期 timer 保持 disabled。下面的域名地址表描述完整模式，不能当作本轮已上线结果。

2026-11-16 12:36:39（北京时间），8.140.230.149 已切换到独立 Nginx 1.30.6 / OpenSSL 3.5.9，旧站和两个 IP 入口验收通过。实际部署、遗留服务停用与未完成事项见[本轮验收](../../../文档/业务验收与审查记录/OPS-20261116--独立Nginx与TLS升级验收.md)。新域名公网仍受到备案拦截，本记录不表示其已经上线。

| 地址 | 内容 |
| --- | --- |
| `www.qingyegame.com:443` | 用户端，API 直接转发本机 5180 |
| `admin.qingyegame.com:443` | 管理端，API 直接转发本机 5182 |
| `qingyegame.com:443` | 固定跳转到 www |
| `8.140.230.149:5179 / :5181` | 保留用户端与管理端 IP 入口 |
| `qingyeyouxi.com / www.qingyeyouxi.com:443` | 原静态站和 SaaS API/CDN 转发 |

WebTransport 继续由 Go 监听 UDP 5179/5181，沿用 IP 证书；新域名的 CSP 显式允许对应 IP 入口。WebSocket 使用各页面同源路径。新 Nginx 直接转发到 Go API，不经过旧 Nginx，以免把真实访客地址改成回环地址。

生产程序路径为 `/opt/qingye-edge/current/sbin/nginx`。生成配置引用同一目录的 `conf/mime.types`，不依赖宝塔 Lua、统计 Cookie 或宝塔配置 include。旧站 API/CDN 上游已改为 HTTPS，固定 Host/SNI，开启证书校验和深度 4 的链校验；API 保留不带尾斜杠的 URI 语义，CDN 保留带尾斜杠的前缀替换语义。上游 SaaS 仍由原供应商维护，这次入口升级不会转移其数据或系统控制权。

上游证书链使用 `/etc/qingye-edge/upstream-ca.pem`。`prepare-upstream-ca.py` 仅读取系统 CA，补入经过固定 DER 指纹、有效期和可信链校验的 Let's Encrypt 官方 Root YR-by-X1 交叉签名证书，并原子输出独立 CA 文件；不修改系统信任库、不关闭证书校验。新机器安装时先准备此文件，再执行 Nginx 检查：

```sh
python3.11 prepare-upstream-ca.py --output /etc/qingye-edge/upstream-ca.pem
```

网络受限时可用 `--intermediate` 指定已下载的同一官方 PEM，仍会进行指纹和链校验。上游更换证书链、官方材料变更或系统 CA 更新后，应重新核验；不能通过移除校验来绕过错误。

## 生成及检查

使用 Python 3.11 或更新版本。生成器仅依赖标准库；运行 `probe.py` 和全套测试还需要 `cryptography`，用于按现有 R24 协议验证 API 加密响应。可复用已具备该依赖的验收环境，不要为检查改动服务器系统 Python。输出父目录须已存在。生成只写指定配置，不发送任何 Nginx 信号。

```sh
python3.11 generate.py --staging --output /etc/qingye-edge/staging.conf
python3.11 generate.py --output /etc/qingye-edge/nginx.conf
python3.11 -m unittest discover -s . -p 'test_*.py' -v
```

若新域名因备案拦截或证书尚未签发，增加 `--without-domains`。此时没有新域名的 443 站点，不借用旧站或 IP 证书冒充；新域名 HTTP 仅提供 ACME 验证路径，其余返回 503。旧站和 IP 入口继续使用各自证书。解除外部限制、签发证书并同步应用/Turnstile 配置后，重新生成完整模式再验证。

`--staging` 只监听 `127.0.0.1:18080 / 18443 / 15179 / 15181`，PID 为 `/run/qingye-edge/staging.pid`。正式模式监听 TCP `80 / 443 / 5179 / 5181`。两种模式都会读取真实站点文件与证书，但静态预验不得触发业务写操作。

运行真实 Nginx 配置验证和只读检查后，才能切换：

```sh
/opt/qingye-edge/current/sbin/nginx -t -p /var/lib/qingye-edge/ -c /etc/qingye-edge/staging.conf
python3.11 probe.py --staging --connect-host 127.0.0.1
```

受限模式的两条命令均按实际情况增加 `--without-domains`（仅生成器和 probe 支持此参数）。`probe.py` 只发送 GET，验证真实证书链、SNI、页面构建文件、安全头、路由隔离、ACME 目录和未知域名拒绝；不忽略 TLS 校验，不登录，不发送短信，不写商品或聊天。API 检查会先建立短期匿名 R24 加密上下文，只在进程内保留 Cookie 和密钥，随后认证并解密响应；公开登录能力必须返回 200，未登录的管理商品接口必须返回 401，缺少协议的 428 不算通过。它不替代浏览器操作、真实验证码、带身份的聊天协议验收。

## 启动、切换与恢复边界

`qingye-edge.service` 使用独立 PID 和 `Type=forking`，先检查配置再启动/重载；停止时向 master 发送 SIGQUIT，最多等待 90 秒。它不会自动关闭占用原端口的宝塔或历史独立 Nginx。切换程序必须先备份三组原配置、运行参数、证书和服务状态；新入口预验成功后协调端口交接，失败恢复原入口。不能将停止旧程序再启动新程序描述成严格零中断。

证书续期需要同步修改：原 IP 证书 hook 不得继续检查或重载已经退役的两个旧 Nginx 服务；应校验新总配置、重载 edge，并保留 Go WebTransport 的证书加载流程。域名证书与 IP 证书的验证名称、lineage 和部署权限不同，不能混用原 `checkip` 脚本。

## IP 证书续期接续

`deploy-ip-certificate.py` 继续读取 `/etc/qingye-production/letsencrypt/live/qingye-production-ip`，使用原 `/etc/qingye-production/tls/current` 原子版本指针。验证通过后检查独立入口配置、reload `qingye-edge.service`，再分别 restart 两个 Go API，使 WebTransport 加载新证书。证书相同不重载；切换失败保留材料并恢复原证书与服务。正常运行期间不得使用 `--install-only`，它只允许相关服务全部停止时安装。

原 IP 续期 timer 保留。其 service 启用了 `ProtectSystem=strict`，必须安装本目录的可写路径补充配置，否则证书 hook 中的独立 Nginx 检查可能无法打开日志或运行目录。补充配置只追加路径，不清空原有 `ReadWritePaths`。

安装顺序：先确认独立入口已切换并验收，再在维护窗口中暂停原 timer、确认没有正在运行的续期任务，并将原 hook 与 unit 状态保存在服务器私有回退目录。随后安装：

```sh
install -d -m 0755 /etc/systemd/system/qingye-production-certbot-renew.service.d
install -m 0644 qingye-production-certbot-renew-edge.conf \
  /etc/systemd/system/qingye-production-certbot-renew.service.d/20-edge-paths.conf
install -m 0755 deploy-ip-certificate.py \
  /usr/local/lib/qingye-production/deploy-certificate.py
systemctl daemon-reload
systemctl show qingye-production-certbot-renew.service -p ReadWritePaths
```

原 service 的 deploy hook 和 `ExecStartPost` 均使用上述保留的安装路径，无需另建 IP timer。核对合并后的目录权限和证书部署检查后，恢复原 timer 的启用与运行状态。需要真实触发续期或部署时，选择可接受聊天短暂重连的维护时段；不能把 Go 重启描述为连接完全不中断。

本轮已实际启动原 IP 续期 service，证书未变化，因此没有重载入口或重启 Go。2026-11-16 12:39:49 在相同 `ProtectSystem=strict` 与可写目录限制下完成 Certbot dry-run，全部模拟续期成功；该检查未运行 deploy hooks，也未替换正式证书。两项结果分别证明已有证书的部署检查与模拟续期可用，不混写为发生了正式换证。

若回退到原三个 Nginx 进程，还要恢复原证书 hook 和对应 unit 配置，并核对 timer 状态；不能只恢复网页入口。

## 域名证书准备与旧站证书

新域名使用独立目录 `/etc/qingye-edge/letsencrypt`、`/var/lib/qingye-edge/letsencrypt`、`/var/log/qingye-edge/letsencrypt`，lineage 固定为 `qingye-domains`，证书须同时包含 `qingyegame.com`、`www.qingyegame.com`、`admin.qingyegame.com`。以下操作仅安装维护配置，不签发证书，也不启用 timer：

```sh
install -d -m 0700 /etc/qingye-edge/letsencrypt
install -d -m 0755 /usr/local/lib/qingye-edge
install -d -m 0700 /var/lib/qingye-edge/letsencrypt /var/log/qingye-edge/letsencrypt
install -m 0755 renew-domain-certificates.sh /usr/local/lib/qingye-edge/renew-domain-certificates.sh
install -m 0644 qingye-edge-certbot-renew.service /etc/systemd/system/qingye-edge-certbot-renew.service
install -m 0644 qingye-edge-certbot-renew.timer /etc/systemd/system/qingye-edge-certbot-renew.timer
systemctl daemon-reload
systemctl is-enabled qingye-edge-certbot-renew.timer
```

本轮预期最后一项为 `disabled`；不要执行 `enable --now`。解除备案限制后，先签发证书，完成应用域名配置、完整模式生成及预验，再运行 `renew-domain-certificates.sh --deploy-hook` 检查并加载域名证书，验收通过后才启用 timer。脚本只 reload 入口，不重启使用 IP 证书的 Go；它会拒绝缺少明确 SAN、证书与密钥不匹配、未启用域名 TLS 或其他 lineage。首次签发时尚未启用域名 TLS 属于待部署状态，不代表证书签发失败，启用配置后应再次执行部署钩子。

**旧站目前复制保留的 Certum 证书有效至 2027-04-14，仍属手动维护，并未完成自动续期接入。** 入口读取 `/etc/qingye-edge/tls/legacy/{fullchain,privkey}.pem`。到期前需单独更新并验收；若未来改用 `qingye-legacy` lineage，应另外实现对这个固定读取位置的成对原子发布及回退。当前域名续期脚本明确拒绝 `qingye-legacy`，不能把新域名 timer 当成旧站续期保障。

## 独立入口日志轮换

将 `qingye-edge.logrotate` 安装到 `/etc/logrotate.d/qingye-edge`。规则每天检查，保留 14 份轮换文件、延迟压缩，并在检查时发现超过 100 MiB 后提前轮换；该阈值不是实时硬上限。轮换后只向独立入口 master 发送 USR1 重开日志，不重载配置、不重启 API。

```sh
install -m 0644 qingye-edge.logrotate /etc/logrotate.d/qingye-edge
logrotate --debug /etc/logrotate.d/qingye-edge
```

调试检查不执行轮换。日志规则仅匹配 `/var/log/qingye-edge/*.log`，不包含旧宝塔日志、Certbot 子目录或生产备份。首次安装不执行强制轮换；旧日志与运维遗留的清理另行确认。

新域名上线前，后端准确增加各自 `ALLOWED_ORIGINS` 和 `TURNSTILE_HOSTNAMES`，保留原 IP 来源；Cloudflare 组件允许的主机名也要同步。管理端需按 `VITE_PUBLIC_SITE_URL=https://www.qingyegame.com` 重新构建。Cookie 继续使用 host-only，不共享用户端和管理端 Cookie。**这些域名环境变更本轮不执行。**

本目录的离线测试只校验配置边界；服务器实际安装版本、证书、HTTP/浏览器验收及最终部署回执另行记录。
