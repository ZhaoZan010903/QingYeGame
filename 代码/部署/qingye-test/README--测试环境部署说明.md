# 152服务器：前后端测试联调

## R23当前发布

2026年11月14日20:55:46（北京时间），152用户/管理静态与双API已发布`20261114-product-r23`，源码`87d7d6931b1e48bed004e74b59c4948a57e3c0c8`已推送GitHub并核对。0020已备份执行，9张原业务表旧字段摘要一致，全部现有商品加分为0、推荐版本为1，没有给业务商品调分或修改业务账号。运行配置、短信能力及8.140生产站保持，5178持续masked。

Go全量/race各565、专项105、用户198/管理160、Python7及HTTP功能420→并发55→安全168通过，vet/build和源码/二进制govuln通过。管理隔离浏览器14组、公网管理只读7组和公网搜索6组通过；临时管理员会话已注销并复查401。

回执`/root/qingye-private/integration/20261114-product-r23-receipt.json`，备份`/root/qingye-private/r23-20261114/test-backup-20261114-125537`。`code_commit`绑定源码/产物，`github_commit`绑定最终文档。0020与旧API结构兼容，但旧API不支持加分；回退程序不代表撤销数据库迁移或运营设置。详见[R23验收](../../../文档/业务验收与审查记录/UI-R23--界面排版与推荐加分验收.md)。

> 当前用户/管理静态及双API为`20261114-admin-r22-mobile-r213`，2026年11月14日19:20:11（北京时间）已部署，源码`21c4ac57bd98e9bfaaad03e0742a8d143110361a`已推送GitHub并核对。R22提供完整会员手机号及同权管理员管理，R21-3放大手机卡片；0019新增管理员版本与HMAC幂等表，备份后核对9张旧业务表摘要一致，没有修改3会员/2管理员资料。25+7静态资源TLS/SHA、双API健康、管理员公网10组及手机卡片公网8组通过，临时会话19:21:11注销401。配置/短信及5178 masked保持。回执`/root/qingye-private/integration/20261114-admin-r22-mobile-r213-receipt.json`的`code_commit`绑定源码，`github_commit`绑定最终文档；详见[R22验收](../../../文档/业务验收与审查记录/ADMIN-R22--会员信息与管理员管理验收.md)、[R21-3](../../../文档/业务验收与审查记录/CARDS-R21-3--手机商品卡片图片放大.md)及[发布记录](../../../文档/计划/RELEASE--测试环境发布记录.md)。后续已使用管理员版本/改密后，不应回退到允许旧接口绕版本的API；以下均为历史发布。

> 当前双API及管理静态为`20261114-trash-purge`，2026年11月14日18:33:46（北京时间）已部署，源码`da12bc3b9be3a27bdb50dcc647262ecf64f00d5b`已推送GitHub并核对；用户静态保持`20261114-rental-quotes-r212`。新增0018，48345条已校验历史OSS副本已登记；停双API备份后核对9张业务表旧字段摘要一致，没有执行业务永久删除。清理任务、退休键及永久删除商品均为0，回收站5件。管理7个静态文件TLS/SHA、双API健康及公网7组只读验收通过，临时管理员会话已撤销。配置、短信能力及5178 masked保持。回执`/root/qingye-private/integration/20261114-trash-purge-receipt.json`中`code_commit`绑定源码，`github_commit`绑定最终文档；详见[验收](../../../文档/业务验收与审查记录/TRASH-PURGE--回收站永久删除验收.md)及[发布记录](../../../文档/计划/RELEASE--测试环境发布记录.md)。实际永久删除后，不能仅回退旧程序恢复资料和文件；旧`oss_read`仍关闭，不重启历史复制服务。下方看板及更早版本为历史。

> 当前双API及管理静态为`20261114-admin-dashboard`，2026年11月14日17:41:59（北京时间）已部署；用户静态仍为`20261114-rental-quotes-r212`。源码`a9a80458562de9093b7c42925af895b2ffe6920f`已推送GitHub并核对。本轮无迁移，0017及两份运行配置保持，5178持续masked。服务器7个静态资源SHA256/TLS、双API健康、7/30天各3种排行及独立SQL核对通过：商品576、出售45、租赁531、可见576。公网Chrome正常TLS下6项只读检查通过，1440/390/320视口无横向溢出，无页面错误、5xx和非预期控制台错误。网络层拦截5个聊天票据写请求，临时测试管理员会话于17:43:10注销并复查401。私有回执`/root/qingye-private/integration/20261114-admin-dashboard-receipt.json`的`code_commit`对应上述源码，`github_commit`对应最终文档提交，接手须与远端核对。详见[看板验收](../../../文档/业务验收与审查记录/ADMIN-DASHBOARD--运营数据看板验收.md)及[发布记录](../../../文档/计划/RELEASE--测试环境发布记录.md)。

> 此前R21-2发布时三端为`20261114-rental-quotes-r212`。0017与531条旧租号拆价已备份执行；出现自定义时段后不能只切回旧API。部署与产物证据见[R21-2验收](../../../文档/业务验收与审查记录/RENTAL-R21-2--租赁报价与发布展示验收.md)及[发布记录](../../../文档/计划/RELEASE--测试环境发布记录.md)。下方版本段落保留历史上下文，5178持续masked。

> R7已获用户授权并部署；新增WebTransport聊天、独立管理重构、最新双主题和12张预览图集。可信IP/HTTPS已配置，公网UDP放行状态查最新R7验收记录。

此目录部署 `0.8.0-test.1` 测试环境，不切换现有生产站；5178历史原型已停用，不得在业务部署时恢复。主机上的其他数据库、Redis及网站继续使用原有配置。

| 组件 | 地址或路径 |
| --- | --- |
| React 用户站 | `https://152.136.252.204:5179`，源码`代码/前端/用户端` |
| React 管理站 | `https://152.136.252.204:5181`，源码`代码/前端/管理端`，独立构建与登录 |
| public API | `127.0.0.1:5180`，`APP_SURFACE=public`，仅本机监听 |
| admin API | `127.0.0.1:5182`，`APP_SURFACE=admin`，仅本机监听 |
| 浏览器接口 | 用户站`/api/`只代理5180，管理站`/api/`只代理5182；就绪检查在API的`/healthz` |
| PostgreSQL | 复用本机15服务，独立角色 `qingye_test`、业务库 `qingye_test` |
| 自动化检查库 | `qingye_test_checks`；测试独立schema并自动清理 |
| Redis | 独立实例 `127.0.0.1:6381`，独立认证、AOF、64MB上限 |
| 业务系统用户 | `qingye-test`，两个API进程共享业务数据库和受控媒体 |
| 原图与上传 | `/var/lib/qingye-test/media`，不映射为Nginx静态目录 |
| 私有原始快照 | `/var/lib/qingye-test/import` |
| public配置 | `/etc/qingye-test/app.env`（root 0600），不进入源码包 |
| admin配置 | `/etc/qingye-test/admin.env`（root 0600），不进入源码包 |
| API版本 | `/opt/qingye-test/releases/<release>/qingye-api`，`current`符号链接 |
| 用户静态版本 | `/var/www/qingye-test/releases/<release>`，`current`符号链接 |
| 管理静态版本 | `/var/www/qingye-admin/releases/<release>`，`current`符号链接 |
| 私有发布资料 | `/root/qingye-private/integration` |

旧HTML、React原型代码已从当前仓库清理，Git历史查阅方式见[历史说明](../../../文档/原型代码/README--历史原型说明.md)。5178服务已停止并禁止启动，详情见[停用说明](../qingye-preview/README--历史原型部署说明.md)。当前`代码/前端/用户端`与`代码/前端/管理端`是两个业务前端项目；原型的本机数据和模拟会话不导入业务环境。

## 入口与配置隔离

两个API共用一个构建产物，以不同环境文件启动。public只开放公开及会员业务，admin提供管理员业务入口。用户站不打包管理模块，也不代理管理API。

| 配置 | public | admin |
| --- | --- | --- |
| `APP_SURFACE` | `public` | `admin` |
| `QINGYE_ADDR` | `127.0.0.1:5180` | `127.0.0.1:5182` |
| `ALLOWED_ORIGINS` | `https://152.136.252.204:5179` | `https://152.136.252.204:5181` |
| `PUBLIC_BASE_URL` | `https://152.136.252.204:5179` | `https://152.136.252.204:5181` |
| 会话Cookie | `qingye_session` | `qingye_admin_session` |
| 数据库会话audience | `public` | `admin` |

Cookie名、audience、角色、账户状态及写请求来源由服务端联合校验；上传票据也限制在创建它的入口。**同一IP的不同端口不是不同Cookie域**，不能把端口或前端角色判断当作会话隔离保证。R7已启用可信IP/HTTPS，`SECURE_COOKIES=true`，Origin精确匹配各自HTTPS入口。旧HTTP链接308跳转到同端口HTTPS。API5180、5182及PostgreSQL、Redis均不开放外网。

## 首次配置

先检查可用端口、内存、磁盘及已有服务。创建系统用户 `qingye-test` 与上述目录；媒体和Redis数据由该用户持有。Nginx临时目录使用 `nginx` 用户。不得对已有其他项目目录递归修改权限。

`bootstrap-services.py --secrets <私有JSON>` 创建专用数据库及Redis。JSON包含 `db_password`、`redis_password`、`session_secret`，值使用足够长度的随机URL安全字符串。另有 `accounts` 数组用于专用测试账户，每项含 `phone`、`nickname`、`password`、`role`。密码不作为命令行参数。

脚本仅为清叶两库、专用角色及本机IPv4添加SCRAM认证规则，先备份 `pg_hba.conf.before-qingye-test`，不改变其他应用的规则，也不开放5432或6381到外网。

`provision-isolation.py --acceptance-secrets <验收专用私有JSON>`读取已有业务配置，将public改为本机监听，生成独立admin配置，并创建下述验收资源。验收JSON含独立的`db_password`、`redis_password`和`session_secret`，不要复用业务凭据。脚本不会自动切换正在运行的API；安装服务配置后需明确重启相应清叶服务。

将API、用户/管理web、Redis和媒体任务的systemd配置放入 `/etc/systemd/system`。业务public API以非root用户运行，内存上限384MB、Go目标256MiB；admin上限256MB、Go目标180MiB。新上传与缩略图解码在各自进程内使用单并发队列；原始迁移文件不被缩略图处理覆盖。Nginx的用户与管理站分别使用`nginx.conf`、`admin-nginx.conf`及独立临时目录。

## 构建与切换

用户端在`代码/前端/用户端`安装锁定依赖，构建时设置：

```sh
VITE_DATA_MODE=api
VITE_API_BASE_URL=/api/v1
VITE_ENVIRONMENT_LABEL='测试环境 · 旧站商品快照'
VITE_SMS_ENABLED=false
```

管理端在`代码/前端/管理端`独立执行`pnpm install --frozen-lockfile`，构建配置使用`VITE_API_BASE_URL=/api/v1`、`VITE_PUBLIC_SITE_URL=https://152.136.252.204:5179`。所有`VITE_`变量都是浏览器可见内容，不能放服务器凭据。两个前端分别执行`pnpm test`、`pnpm build`，分别发布各自`dist`，不混用依赖或构建目录。

后端执行单元和真实数据库/Redis检查、`go vet -tags=nomsgpack ./...`，再以`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags=nomsgpack -trimpath -o qingye-api ./cmd/qingye`构建。R14使用Go1.27.4、Gin1.12.0。具体本地启动及测试变量见[后端说明](../../后端/README--后端开发说明.md)。先在独立验收环境完成业务、并发、安全验证，再发布业务测试站。

上传产物到新的版本目录并核对SHA256，分别验证用户与管理Nginx配置。API、用户静态页、管理静态页各自切换`current`链接；每个链接用原子替换，三个链接不是一个跨目录事务，需按接口兼容性安排顺序并完成切换后核验。首次业务部署执行`maintenance.py migrate`、`seed-users`、`import-legacy`、`reconcile-media`；随后启动`qingye-test-api`、`qingye-test-admin-api`、`qingye-test-web`、`qingye-test-admin-web`。导入以旧站源ID去重，重复执行不会覆盖管理员后续修改。

维护脚本默认读取业务环境`app.env`。操作验收资源必须显式传入验收`--env`及`--binary`，避免误用业务库。定时推荐只由public进程执行，admin保留人工刷新；两个进程不得分别启动周期重算。

2026年11月11日确认的报价规则随R5迁移应用：出售元、时日租号元/日、周月租号元/月。先备份并在独立环境验证，再用新二进制执行 `migrate`；有序迁移记录会阻止重复回填，不覆盖人工报价、重分类、原始载荷或历史审核快照。发布后按三类分别核对价格、单位、预算筛选和排序。已部署R4与R5补发的散列、迁移数量和复验状态以[发布记录](../../../文档/计划/RELEASE--测试环境发布记录.md)为准，不能仅凭文档里的新规则认为服务器已完成迁移。

构建产物不是源码交接。GitHub保存源码、依赖锁、配置示例、测试和文档；私有源数据、真实配置、日志和图片不提交。

## 独立验收资源与执行顺序

| 资源 | 配置 |
| --- | --- |
| API | `127.0.0.1:5184` public、`127.0.0.1:5185` admin，仅本机 |
| 入口Origin | `http://127.0.0.1:5186` public、`http://127.0.0.1:5187` admin；仅供受控本地浏览器或验收脚本，不对外发布 |
| PostgreSQL | 数据库及角色`qingye_acceptance`，与`qingye_test`、`qingye_test_checks`分开 |
| Redis | 独立认证实例`127.0.0.1:6382`，32MB上限；目录`/var/lib/qingye-acceptance/redis` |
| 系统用户 | `qingye-acceptance` |
| 媒体 | `/var/lib/qingye-acceptance/media`，不共享业务图库 |
| 配置 | `/etc/qingye-test/acceptance-public.env`、`acceptance-admin.env`；root 0600 |
| API产物 | `/opt/qingye-test/acceptance-current/qingye-api`，独立于业务`current` |
| 服务 | `qingye-acceptance@public`、`qingye-acceptance@admin`、`qingye-acceptance-redis` |

验收环境使用专用账户和测试商品，并导入576件公开商品快照供查询规模验证；原图不重复复制到验收媒体目录。首次先使用验收环境文件执行数据库迁移和专用账户初始化，再启动两个验收API。隔离的是数据库、Redis实例、操作系统用户和媒体路径，仍与业务环境同机共享CPU、内存和磁盘，不能视为独立机器的性能结果。

`代码/工具/acceptance_suite.py`按`functional`→`concurrency`→`security`执行，三阶段共用私有`--state`文件，分别输出`--output`报告。调用时明确指定`--public-url http://127.0.0.1:5184`、`--admin-url http://127.0.0.1:5185`、对应Origin和`--account-file`。脚本拒绝非本机目标及5178—5182业务端口，安全阶段要求并发阶段已完成；不要绕过目标校验在业务站执行写测试。

每轮修复后重建验收产物，复核业务路径，再进行受控并发与安全验证。记录请求成功率、响应延迟、服务器资源和失败原因；本机接口计时与外网链路计时分开，外网测试注明gzip与并发条件。至少三轮代码/业务审阅记录保留发现、修复、复测及尚未覆盖的范围，不以“无已知问题”承诺绝对无缺陷。最终结果另记开发进度和验收报告。

## 原图迁移

当前已导入576条旧商品及48,332条有序图片关联，**原图已全量完成，逐文件SHA256和逐商品有序关联核验通过**。应分别核对源引用数、本地文件校验成功数和数据库ready数；pending文件的存在不改变商品原始图集总数。

`qingye-media-migration.service` 从已授权的旧站图片CDN读取，先封面后图集，默认24个下载线程、总请求速率上限12次/秒。图片原始字节写入 `media/legacy/<URL的SHA256>`；进度、失败和内容SHA256写入私有清单。保留至少5GiB磁盘余量，触及阈值退出75并等待空间处理；不会删除其他项目、自动改成压缩图或清除已完成文件。

```sh
systemctl start --no-block qingye-media-migration
systemctl status qingye-media-migration
journalctl -u qingye-media-migration -n 10
```

进度文件为 `import/media-status.json`，逐文件清单为 `import/media-manifest.jsonl`。恢复时同一文件大小匹配已完成记录才跳过，不完整文件重新下载。失败项保留来源，可重跑修复。

`qingye-test-media-reconcile.timer` 每两分钟核验新增本地文件的类型、尺寸信息和内容SHA256，更新数据库可用状态；不会从外部回源。网站图片始终经过Go权限校验，下架、撤回或分类停用后不能通过旧图片地址继续读取。

本次48,332张原图全量完成并核验后，已停止并禁用此轮询timer，避免继续空扫描；后续新一批迁移可按需要重新启动。常规会员上传在完成接口中直接核验，不依赖此timer。

小图使用 `?variant=thumb`，原图仍可在详情查看。小图是额外的派生缓存，不替代原始迁移文件。

## 检查、备份和回退

首次在systemd服务启动前手动执行 `nginx -t`，需先创建 `/run/qingye-test-web` 与 `/run/qingye-test-admin-web`；正常服务启动由unit的 `RuntimeDirectory` 创建。缺少目录导致配置校验失败时，不切换current。发布版本与校验值见[当前发布记录](../../../文档/计划/RELEASE--测试环境发布记录.md)。

- `/healthz` 同时检查数据库和Redis。服务进程active不等于接口可用。
- 发布后只读核对目录总量、原始字段保留及已确认的元/元每日/元每月报价、图片顺序及就绪情况、同单位搜索筛选排序和前端资源；异常报价单独核对。会话跨入口、上传提交审核、下架、配置保存和安全写测试使用独立验收环境。
- 使用专用测试会员，不迁移旧会员密码，不发送短信；短信未接入返回明确503。
- 备份 `qingye_test` 数据库、完整媒体目录和私有导入清单；凭据单独加密保管。图片约数十GB，服务器磁盘内复制一份不适合作为完整异地备份。
- 回退应用时分别将API、用户端和管理端`current`指向已验证的兼容版本，再重启对应测试服务。数据库结构变更需单独评审，不自动用旧库覆盖新数据。
- 停止业务测试环境仅操作对应`qingye-test-*`及`qingye-media-migration`服务；停止验收只操作`qingye-acceptance@public`、`qingye-acceptance@admin`与`qingye-acceptance-redis`。不停止系统PostgreSQL、6379 Redis或其他站点，也不恢复已停用的5178原型。

当前为HTTP测试环境。正式业务切换仍需域名HTTPS、短信服务、经营规则确认、备份恢复演练和生产验收。

## R7 HTTPS、WebTransport与证书续期

- TCP5179、TCP5181：独立Nginx HTTPS站点；UDP5179、UDP5181：两个Go进程的HTTP/3。腾讯云安全组／防火墙需分别允许这两个UDP端口。
- 证书使用Let’s Encrypt短期IP证书，Certbot5.10.0安装在`/opt/qingye-certbot`。线上关闭开发证书指纹模式，不关闭TLS校验。
- `qingye-certbot-renew.timer`每日00/12点检查，随机延迟不超过30分钟；`renew --dry-run`演练通过。证书仅留服务器，原始lineage为`/etc/letsencrypt/live/qingye-test-ip`。
- `deploy-chat-certificate.py`验证证书有效期、IP及公私钥配对，复制到root:qingye-test受控目录，以符号链接原子切换整对证书；重载两个Nginx、重启两个API。更新异常保留原证书路径并回退。
- 首次安装可用`--install-only`，不得在业务运行中用此参数代替正常部署hook。首次HTTP切换HTTPS时，Nginx reload是异步动作，健康检查需等待新worker接管，不能在HUP后立刻断言失败。
- 本轮私有回退材料位于`/root/qingye-private/consultation-release-20261111/20261111-gallery-chat-r7-backup`。保留旧R5/R6符号链接目标、原环境文件及数据库dump。聊天迁移为新增表，应用回退不删除表或覆盖新数据。

可信IP证书采用短有效期机制，续期不能停用。依据：[Let’s Encrypt IP证书与Certbot说明](https://letsencrypt.org/2026/03/11/shorter-certs-certbot.html)。
