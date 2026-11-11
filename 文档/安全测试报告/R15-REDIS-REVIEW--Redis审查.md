# R15 Redis 审查

审查时间：2026-11-12。范围包括 Redis 连接池、原子限流、身份失效、聊天票据、WebSocket/WebTransport 连接租约、Pub/Sub 回收、TTL 与故障行为。本记录对应当前工作树的定向审查，不代表已部署，也不替代整站安全或容量验收。

## 1. 发现与修复

### 已修复：基础设施故障被误报为身份失效

位置：`代码/后端/internal/transport/httpapi/chat_transport.go` 的 `acceptChatTicket`。

此前 Redis `GETDEL` 返回的所有错误都会得到 `401 INVALID_TICKET`；后续 PostgreSQL 会话查询的所有错误都会得到 `401 SESSION_EXPIRED`。使用已关闭的 Redis 客户端、已关闭的 PostgreSQL 池，可在 WebSocket 和 WebTransport 两条握手路径分别复现：合法格式的连接请求被误报为票据过期或需要重新登录。

修复仅区分错误类型：

| 情况 | 返回 |
| --- | --- |
| 票据不存在、已过期、内容或绑定不符 | 保持 `401 INVALID_TICKET` |
| Redis 连接、命令或连接池不可用 | `503 CHAT_UNAVAILABLE` |
| 登录会话不存在或已失效 | 保持 `401 SESSION_EXPIRED` |
| PostgreSQL 会话查询不可用 | `503 SESSION_UNAVAILABLE` |

Origin、入口 audience、传输方式、WebSocket Cookie 绑定、账号归属和连接数量校验均未放宽。两个传输共用同一握手方法。

共享客户端对申请票据 API 的 401/403 停止自动重试，对暂时失败进行兼容通道尝试或退避重连；浏览器对握手失败不一定暴露 HTTP 状态，但后端仍需准确报告暂时不可用与凭据无效。故障期间不复用已消费票据，重新连接申请新票据。

新增 `chat_redis_failure_test.go` 先复现修复前的错误 401，修复后 Redis/数据库不可用分支返回 503，缺失票据分支仍返回 401。测试不依赖关闭真实服务，也不修改正在运行的 Redis 配置。

## 2. Redis 实际职责及 TTL

| 数据 | Key / channel | 原子性与期限 | 真正数据来源 |
| --- | --- | --- | --- |
| 登录 IP/账号限流 | `qingye:limit:login-ip:*`、`login-phone:*` | Lua 一次执行 INCR 与首次 EXPIRE；15分钟窗口 | Redis 临时计数 |
| 上传限流 | `qingye:limit:upload:*` | 同一脚本；10分钟窗口 | Redis 临时计数 |
| 浏览、偏好、聊天操作频控 | `qingye:limit:view:*`、`interest:*`、`chat-*` | 同一脚本；当前调用为1分钟窗口 | Redis 临时计数 |
| 一次性聊天票据 | `qingye:chat:ticket:{surface}:{token_hash}` | SET 携带30秒期限；GETDEL 原子消费 | 登录资格仍查询 PostgreSQL |
| 活跃连接租约 | `qingye:chat:streams:{surface}:{user_id}` | ZSET；Lua清理超过50秒未续约项、检查4个额度、写租约并设置70秒TTL | 临时连接协调 |
| 聊天更新通知 | `qingye:chat:notify:{user_id}` | Pub/Sub，不落消息正文，无离线缓存或Key TTL | PostgreSQL 消息表 |
| 热门批次提示 | `qingye:recommendations:batch` | SET，1小时TTL；发布失败不回滚数据库批次 | PostgreSQL 热门快照与批次状态 |

当前代码没有把商品列表、详情、登录会话或个性化推荐响应放入 Redis 缓存。登录会话、权限、商品公开状态、聊天正文和行为事实均由 PostgreSQL 查询决定。因此本轮没有为不存在的商品缓存添加“缓存击穿锁”，也不能把 Redis 批次提示描述为推荐的权威数据源。

## 3. 连接池与资源回收

- `internal/bootstrap/runtime.go` 为普通命令设置可配置的 `REDIS_POOL_SIZE`，默认5；拨号、读、写和普通池等待超时均为1秒。
- `MaxActiveConns=0` 是为了允许 go-redis 的独立 Pub/Sub 连接，不应改回普通池容量，否则普通池填满后聊天订阅会失败。检查所用 go-redis v9.12.1 的本地源码可见，普通命令 `Get` 经池信号量，Pub/Sub `NewConn` 单独建立连接。
- 每个实时聊天连接持有一个订阅，`defer sub.Close()` 回收；初始化订阅等待受3秒 context 限制。退出路径同时停止 ticker、取消上下文、关闭流并释放该连接的租约。
- WebSocket 与 WebTransport 共用每用户、每入口最多4条连接的额度。租约每20秒续期，续租脚本必须找到原租约成员才更新，不能把已被清理的旧连接重新加回集合。
- 正常关闭使用1秒独立清理上下文执行 ZREM；清理失败时由 TTL 及下次准入的过期成员清理兜底。单连接最多存活10分钟，客户端随后申请新票据重连。
- 服务关闭先停止聊天准入再等待现有会话退出，资源池保持到租约清理结束。现有取消与回收测试仍通过。

## 4. 限流、身份与故障行为

### 限流

Lua 原子执行首次计数与过期设置，避免两个普通命令之间进程退出留下无期限计数。后续请求不延长窗口；到期后重新计数。Redis 为空、已关闭或命令失败时返回 `RATE_LIMIT_UNAVAILABLE`，不把故障当作允许请求。

当前所有调用周期至少为1分钟，不存在小于1秒周期被整秒转换截断的实际调用。限流为固定窗口，窗口边界可能出现两段额度邻接；它不是平滑令牌桶，也不宣称能抵御所有突发流量。

### 登录与失效

身份不跨请求缓存到 Redis。`auth.go` 中的身份缓存只用于单次 HTTP 请求，避免同一请求重复查库；下一次请求重新读取 PostgreSQL 的会话有效期、角色、用户启停状态与 audience。

实时连接在每条命令、收到通知时以及定时更新时重新认证。注销删除数据库会话后，下一条命令不再以连接建立时的身份执行。既有 WebSocket 注销回归实际验证了立即拒绝后续命令并关闭连接。

### 消息与通知

消息先通过数据库事务持久化再通知双方。Pub/Sub 失败不会撤销已提交消息，也不会让客户端自动重发未经确认的内容；重试相同内容使用原 `client_id` 进行数据库去重。通知只含 `changed`，客户端补取数据时重新执行归属检查。

Pub/Sub 本身没有离线投递保证。服务每20秒仍发送一次更新提示；租约续期或身份复核失败则关闭连接，客户端重新连接后从数据库补取记录。因此消息正确性不依赖通知全部到达。

商品读取和会话认证不会因为 Redis 中旧值而继续返回已撤销权限。限流相关写入口、连接票据及租约在 Redis 不可用时明确失败；这会影响可用性，但不会用绕过限流或鉴权换取继续写入。

## 5. 只读实例核对

通过现有测试隧道对当前 Redis 实例执行 `CONFIG GET` 白名单项与 `INFO` 指标读取，时间为 `2026-11-11T18:21:16Z`（北京时间2026-11-12 02:21:16）。没有读取密码、列举用户 Key、修改配置、重启服务或执行 Flush。

| 项目 | 观察值 |
| --- | --- |
| 监听 | `127.0.0.1`，`protected-mode=yes` |
| 持久化 | `appendonly=yes`，`appendfsync=everysec`，最近 AOF 写状态 `ok` |
| 内存限制 | 64MiB，`noeviction` |
| 客户端限制 | `maxclients=100` |
| 当前客户端 | 6，阻塞客户端0 |
| 当时内存 | 约1013KiB，峰值约1.34MiB |
| 淘汰与连接拒绝累计值 | `evicted_keys=0`，`rejected_connections=0` |

这些是一次采样，不是最大容量结果。Pub/Sub 每条聊天连接占用独立 Redis 连接，100 个客户端上限还要容纳两个 API 普通池及运维连接，不能据此承诺支持100个同时在线聊天窗口。`noeviction` 达到内存上限时可能拒绝写命令，应用会返回服务暂不可用；不能靠删限流Key或关掉鉴权解决。

## 6. 实际执行的定向检查

工具链为 Go 1.27.4，使用 `-tags=nomsgpack`。真实数据库/Redis项通过私有测试环境加载器执行；数据库使用测试 fixture 的隔离 schema，Redis只创建随机测试Key并定向清理，不执行 Flush。以下各项已执行通过，没有跳过这些选定项：

| 检查 | 证据与覆盖 |
| --- | --- |
| `TestLimiterConcurrentQuotaAndWindowExpiry` | 32个并发请求仅7个允许、25个限流；首次TTL存在，后续请求不续窗，到期恢复 |
| `TestLimiterUnavailableFailsClosed` | nil及已关闭客户端均拒绝，返回服务不可用 |
| `TestChatNotifierDeduplicatesRecipients` | 同一收件人只收到一次通知；载荷不含消息正文 |
| `TestChatSubscriptionAfterCommandPoolFills` | 普通命令池填满后仍能建立订阅，且普通命令继续可用 |
| `TestChatTicketInfrastructureFailureIsRetryable` | WT/WS的Redis与数据库故障返回503，缺失票据保持401；包含修复前失败复现 |
| `TestChatStaleLeaseReclaimedAndTicketExpires` | 票据TTL不超过30秒；第5条租约被拒；过期成员被清理后可补入且总数仍为4；集合TTL不超过70秒 |
| `TestChatAdmissionStopsBeforeWaiting` | 服务开始关闭后拒绝新聊天会话 |
| `TestWebSocketServerCancellationReleasesLease` | 服务器取消后处理器退出，Redis租约数归零 |
| `TestChatWebSocketLimitsAndRevocation` | WT/WS共享额度；帧限制；注销后连接关闭 |

其中新增测试文件为 `internal/infrastructure/cache/limiter_test.go` 与 `internal/transport/httpapi/chat_redis_failure_test.go`。原有池、传输与关闭测试用于回归，本记录不将其计为本轮新写的测试。

可使用已配置 `TEST_DATABASE_URL/TEST_REDIS_URL` 的私有环境复跑：

```sh
go test -tags=nomsgpack ./internal/infrastructure/cache ./internal/bootstrap ./internal/transport/httpapi -run 'Test(LimiterConcurrentQuotaAndWindowExpiry|LimiterUnavailableFailsClosed|ChatNotifierDeduplicatesRecipients|ChatSubscriptionAfterCommandPoolFills|ChatTicketInfrastructureFailureIsRetryable|ChatStaleLeaseReclaimedAndTicketExpires|ChatAdmissionStopsBeforeWaiting|WebSocketServerCancellationReleasesLease|ChatWebSocketLimitsAndRevocation)$' -count=1 -v
```

## 7. 明确的审查边界

- 本轮未执行 Redis 宕机重启、磁盘写满、内存耗尽、网络分区或主从切换演练；关闭客户端和连接池只用于验证应用错误分类，不能替代上述基础设施验证。
- AOF `everysec` 不承诺故障前最后一秒的临时计数或票据消费记录零丢失。Redis不是登录身份与聊天消息的权威存储，但运维仍需保持认证、回环监听与持久化监控。
- 连接租约分数使用 API 机器时钟，当前部署同机；未来多机扩容需要统一时钟或改为 Redis 时间，并重新验证故障重连和容量。
- 没有发现需要新增会话缓存或缓存失效广播的证据；引入这类缓存时应另行设计注销、用户停用及跨入口失效语义。
