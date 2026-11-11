# GIN R14 -- Gin分层与安全性能验收

日期：2026年11月11日。基线 `f24a3e1da7dcbc7b977e92e0e383b29250c8460f`。本轮将既有后端迁入 Gin，并细分领域、应用服务、基础设施、HTTP适配、启动及维护职责；不增加支付或订单功能。架构与关键取舍见[设计记录](../开发产出文档/ADR-GIN-LAYERS--Gin分层与安全性能设计.md)。

## 1. 实际变更

- Gin 1.12.0负责真实路由注册；沿用严格JSON解码、既有错误码、HEAD兼容以及public/admin两进程隔离。关闭自动路径重定向，不信任任意转发头。
- `internal/app`拆为`domain`、`application`、`infrastructure`、`transport/httpapi`、`config`、`bootstrap`及`maintenance`。依赖规则由Go语法树测试检查，应用服务不依赖Gin、HTTP、pgx或Redis实现。
- 保留会话audience/角色/Origin校验、商品审核版本、完整分页图集、聊天撤销与幂等规则。公开商品使用显式字段白名单。
- 新增可配置请求容量、请求期限、数据库及Redis普通命令池大小；密码验证最多并行2个，超额明确503。Redis限流失败关闭，Lua使用EVALSHA及首次回退。聊天PubSub保留受租约限制的额外长连接，不被普通池占满后误拒绝。
- 首页移除三个无用途的COUNT查询；列表不读取长描述；图片排序用一次批量UPDATE；单个HTTP请求内只加载一次身份；实时命令消除重复身份查询。
- 网络上传先落临时文件，再取得数据库事务并复核票据。保留图片像素、格式、字节数及处理槽限制；图片解码和最终入库仍有受控事务，不宣称消除了所有长事务。
- 修复分类配置删除与商品写入的竞态：商品写入锁定分类，配置修改在同一事务检查引用、修改和审计。并发创建同名分类返回一个成功和明确冲突。
- 关键代码加入简洁注释，说明权限、锁顺序、时钟、资源边界及失败恢复原因。

## 2. 三轮检查与修正

| 轮次 | 检查与发现 | 验证 |
| --- | --- | --- |
| 第一轮 | 按模块迁移身份、商品、会员管理、聊天和媒体；核对旧接口及事务行为 | 实际PostgreSQL/Redis回归105项通过，0失败、0跳过 |
| 第二轮 | 交叉检查边界：Gin路径重复解码、Origin规范化、身份输入UTF-8/NUL、健康检查容量、媒体请求期限、配置显式零值；增加分类并发回归 | 最终结构全量182项通过，0失败、0跳过；`go vet`通过 |
| 第三轮 | 发现并修复Redis普通池填满后PubSub误拒绝、WS丢失服务器取消信号及测试fixture拷贝生命周期锁；增加填满连接池、真实WS取消释放租约和关闭准入回归 | 最终Go全量185项通过，0失败、0跳过；重新执行隔离功能/并发/安全与Linux竞态，结果见下节 |

两端React代码未改变，用户端82、管理端37项测试通过。迁移文件0001—0004及schema.sql仅搬迁至`internal/maintenance`，逐文件散列一致，没有新增业务库迁移。

## 3. 测试边界与证据

本地全量集成测试通过SSH隧道使用专用检查库和Redis逻辑库；各测试使用独立schema。HTTP写入验收只连接`qingye_acceptance`、Redis6382及隔离媒体目录，服务监听5184/5185。性能对照在相同数据、相同请求组合且重新灌入验收数据之前完成。

| 检查 | 结果与证据 |
| --- | --- |
| 全量Go | 私有`gin-final-tests.jsonl`，185通过，0跳过；`gin-round2-tests.jsonl`保留此前182项记录 |
| Linux race | Linux Go1.27.4 + gcc，`go test -race -tags=nomsgpack -count=1 -json ./...`：185项通过，0失败、0测试跳过，无竞态报告；私有`gin-final-race-tests.jsonl` |
| 源码依赖扫描 | `govulncheck -tags=nomsgpack ./...`：未发现已知漏洞 |
| 实际部署二进制扫描 | 最终`gin-final-binary-vuln.txt`，`govulncheck -mode=binary`：未发现已知漏洞 |
| 隔离HTTP | 最终`acceptance-functional-gin-r14-final.json`、`acceptance-concurrency-gin-r14-final.json`、`acceptance-security-gin-r14-final.json`：107/28/67通过 |
| 短时性能对照 | [性能报告](../性能测试报告/GIN-R14--Gin分层性能对照.md) |
| 安全范围 | [安全报告](../安全测试报告/GIN-R14--Gin迁移安全检查.md) |
| 浏览器与部署 | HTTPS实际Chrome两轮各9项检查通过：主页/三种列表/详情、390及1440宽度、两端登录和实时连接；0 pageerror、0 HTTP 5xx。默认轮双方WT=1/WS=0，兼容轮双方WT=0/WS=1。四项业务服务active，API散列一致 |

服务器原始报告在`/root/qingye-private/integration/`；仅摘要进入Git，真实账户、消息、环境变量和数据库数据不提交。最终HTTP套件首次复跑遇到此前安全用例的15分钟登录限流，返回429；保留失败记录。随后复跑前检查隔离Redis6382的登录计数，已自然到期、匹配0键；功能/并发/安全全部通过，没有降低限流阈值或修改业务Redis。

## 4. 发布与后续

API已发布`20261111-gin-r14`，最终二进制SHA256 `210ec3c117b465016fa3f7698dab6704961b1646fc342a89ff3e9e0fbfbf16c0`，16,404,640字节，Go1.27.4、linux/amd64、CGO关闭、`nomsgpack`。两个前端仍使用R13构建。仅切换API current并重启两个API进程，5180/5182健康检查通过，保留R13回退；没有执行数据库迁移、修改TLS/代理或前端配置。

GitHub包含本报告的提交对应本次源码，服务器私有`20261111-gin-r14-receipt.json`保存确切`git_commit`、源码归档与API散列；接手时交叉核验，不使用本文提交号推测运行版本。

本轮不是正式生产发布，测试通过不代表绝对无BUG或漏洞。后续优先开展持续负载和故障恢复演练、监控与告警，再按业务需要设计缓存；不把这次短时压测解释为容量保证。SeaweedFS接入仍暂停，现有原图不迁移或删除。短信供应商、在线支付及订单不属于本轮。
