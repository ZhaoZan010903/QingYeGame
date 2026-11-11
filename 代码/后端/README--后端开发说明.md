# 清叶 Go 联调后端

这是与 React 前端联调的业务后端，采用 Gin 1.12.0、PostgreSQL、pgx 和 go-redis。PostgreSQL 保存商品、会话、审核及推荐事实；Redis 承担限流、短期聊天票据与租约、通知和可重建的推荐批次标识。用户与管理 API 独立运行，共用业务数据，分别校验会话身份范围。

152 测试服务器的用户入口为 `https://152.136.252.204:5179`，管理入口为 `https://152.136.252.204:5181`；API 分别只监听回环地址5180、5182。HTTPS 已启用，Cookie 使用 Secure。原型5178继续保留，未切换8.140生产站。分层决策、权限及资源边界见 [Gin分层与安全性能设计](../../文档/开发产出文档/ADR-GIN-LAYERS--Gin分层与安全性能设计.md)。

当前不包含订单、支付、余额、自动租期或短信供应商。短信相关接口明确返回不可用，使用受控创建的测试账号验证密码登录、收藏、发布及审核。用户2026年11月11日确认：账号出售按元、时日租号按元/日、周月租号按元/月；业务单位对应 `account/day/month`。原始报价、分类和载荷继续保留，R5按确认规则回填有效报价；无法建立有效报价的异常条目不参与预算和价格排序，不擅自造价。

## 构建与启动

模块最低 Go 1.26，本轮构建和验证使用 Go 1.27.4；Gin及其他依赖由 `go.mod` / `go.sum` 固定。构建、测试和静态检查统一指定 `-tags=nomsgpack`，禁用未使用的 MessagePack 支持。先用 `go version` 确认工具链。

```sh
go mod download
go test -tags=nomsgpack ./...
go vet -tags=nomsgpack ./...
go build -tags=nomsgpack -trimpath -ldflags="-s -w" -o qingye ./cmd/qingye
```

程序从进程环境读取配置，不自动加载项目中的 `.env`。用服务管理器的 `EnvironmentFile` 或外部环境文件注入真实配置，不能把真实凭据提交到 Git。示例字段见 [.env.example](.env.example)。

```sh
./qingye migrate
./qingye refresh-itemcf
./qingye seed-user --file /private/test-admin.json
./qingye serve
```

`refresh-itemcf` 手动重算商品协同过滤快照。正常运行时仅 public 进程在启动时及每小时自动执行；PostgreSQL 事务锁避免重叠运行。浏览明细、ItemCF 快照均保存在 PostgreSQL，不增加 Redis 缓存或新的中间件。运行前先应用增量迁移 `0004_itemcf.sql`；算法、资源边界和回退规则见[推荐说明](../../文档/开发产出文档/10-recommendation-design--热门推荐算法.md)。

`seed-user` 文件格式为 `{"phone":"测试登录标识","nickname":"测试管理员","password":"专用强密码","role":"admin"}`。省略 `--file` 时从标准输入读取；密码不出现在命令参数和日志。密码至少12字节，使用 bcrypt 散列；同一个手机号重复执行不会修改原账号、权限或密码。会员使用 `role=member`。管理界面不提供自助提权。

## 环境配置

| 字段 | 作用与默认值 |
| --- | --- |
| `APP_SURFACE` | `public`（默认）或 `admin`；决定路由、会话身份范围及后台任务归属。 |
| `QINGYE_ADDR` | 用户API默认 `127.0.0.1:5180`，管理API默认 `127.0.0.1:5182`，由同机反向代理提供HTTPS入口。 |
| `DATABASE_URL` | 专用 PostgreSQL 连接地址，必填。 |
| `DB_MAX_CONNS` | 每进程数据库连接池上限，默认5，范围1—50。两个API进程的连接预算分别计算。 |
| `REDIS_URL` | 专用 Redis 地址，支持密码；测试部署使用6381。 |
| `REDIS_POOL_SIZE` | 每进程Redis普通命令连接池大小，默认5，范围1—100；等待连接超时1秒。聊天PubSub另用长连接，受会话租约限制，不与普通池共用总连接上限。 |
| `MAX_CONCURRENT_REQUESTS` | 每进程普通HTTP请求容量，默认128，范围1—2048；满载返回503及重试提示。 |
| `REQUEST_TIMEOUT` | 普通API上下文期限，默认 `15s`，范围 `1s`—`60s`；数据库等待也受取消信号约束。 |
| `MEDIA_DIR` | 受控图片存储根目录，由服务账号读写，不直接映射为公开静态目录。 |
| `PUBLIC_BASE_URL` | 对外前端地址；用户端使用HTTPS 5179，管理端使用HTTPS 5181。媒体仍使用相对API路径。 |
| `ALLOWED_ORIGINS` | 逗号分隔的完整HTTP(S)来源，不能使用通配符、路径、查询串或用户信息；管理端仅配置管理来源。 |
| `SESSION_SECRET` | 至少32字符的随机密钥，用于访客签名。登录会话使用独立随机令牌，数据库只存令牌散列。 |
| `SECURE_COOKIES` | 当前HTTPS测试部署为 `true`；仅明确的本地HTTP隔离验收才设为 `false`。 |
| `MAX_UPLOAD_BYTES` | 单个上传文件默认10 MiB，允许1—67,108,864字节；商品图集总张数不设固定上限。 |
| `CHAT_WT_ADDR` / `CHAT_WT_URL` | WebTransport的UDP监听与外部HTTPS地址；未配置监听地址时不启动该服务。 |
| `CHAT_WT_CERT` / `CHAT_WT_KEY` | WebTransport证书及私钥文件路径，仅服务账号可读取。 |
| `CHAT_WT_PIN_CERTIFICATE` | 当前可信IP证书部署为 `false`；自签证书的隔离验证另按部署说明配置。 |

配置在建立监听前验证，非法范围直接阻止启动。`SMS_MODE` 仅为部署示例中的禁用说明，本版没有可通过此变量启用短信的实现。

普通请求、健康检查和聊天连接采用不同容量约束：健康检查固定2个并发槽，满载返回 `HEALTH_BUSY`；密码比较固定2个并发槽，满载返回 `LOGIN_BUSY`；聊天长连接由票据和租约限制。媒体及上传请求仍占普通HTTP容量，使用独立60秒上下文期限。HTTP服务器另设请求头5秒、读取30秒、写入60秒、空闲60秒超时和16 KiB请求头限制，文件上下文期限不会取消这些网络层限制。

`GET /healthz` 同时检查 PostgreSQL 与 Redis；任一不可用返回503。写接口的浏览器请求检查精确来源，带会话 Cookie 但缺少 Origin / Referer 的请求被拒绝。会话 Cookie 为 HttpOnly、SameSite=Lax。反向代理应保留正确的 Origin，不应给不受信客户端伪造来源。

## 数据导入与图片

只使用已授权取得的私密商品快照。导入命令不访问生产站，不上传会员数据，也不会覆盖管理员已修改的商品。

```sh
./qingye import-legacy --products /private/products.raw.json
./qingye reconcile-media
./qingye reconcile-media --limit 1000
```

导入以原商品 `_id` 去重，保留完整原始标题、分类、报价、时间及原始载荷。旧公开快照在隔离测试环境中导入为已展示，标记 `source=legacy`、认证资料未提供。正式上线前仍需经营方核验内容。标题超过100字的旧记录允许管理员原样保留；改写的新标题遵守100字限制。原始载荷及远程图片地址只在数据库内部保存，不通过公开商品接口返回。

R5增加有序迁移 [0001_confirm_legacy_price_units.sql](internal/maintenance/migrations/0001_confirm_legacy_price_units.sql)，在 `migrate` 原有事务及数据库互斥锁内顺序执行，通过 `schema_migrations` 标记完成。仅回填 `source=legacy`、单位仍为unknown且金额为空、原分类/当前导入分类ID/交易类型全部匹配的商品：QQ炫舞买号→account、时日租号→day、周月租号→month。原价必须为非负普通十进制，整数部分1—10位、小数最多2位；负值、科学计数、超精度、溢出和空值均不猜测转换，继续保留来源供人工处理。

迁移不覆盖已人工填写的金额/单位或重分类商品，原始字段和历史审核快照不变。回填时商品版本加1，原已批准当前版本的记录同步批准版本以保持展示，`published_at`不改；记录本次更新数量。分类单位只有仍为原导入默认配置且没有其他人工单位商品时才收窄，避免覆盖运营配置。后续导入采用相同报价规则，不覆盖既有源ID；未知来源分类明确报错。R4确认前的unknown/null状态仅为历史阶段，实际R5应用结果见[发布记录](../../文档/计划/RELEASE--测试环境发布记录.md)。

原图按 `legacy/<sha256(原始绝对URL)>` 保存，协议相对地址只补 `https:`；逗号形式的图片处理参数保持原样。导入只建立本地媒体引用。缺少本机文件的媒体为 pending；`reconcile-media` 检查文件格式，计算散列、大小和 MIME 后标记 ready。异常文件保持不可公开，不进行任意远程代理。媒体接口每次验证商品可见性或访问者权限。

会员上传检查真实格式、字节和像素上限，并重新编码移除元数据。旧原图保持原样；列表缩略图按需生成到独立派生目录，不覆盖原图。详情保留原图链接。`?variant=thumb` 仍执行相同的访问权限检查。

## 管理与扩展

公开接口提供分类和动态筛选、商品列表 / 详情 / 图集、首页、客服和说明页。商品列表价格比较必须指定相同且已确认的报价单位。公开图集的可见性、版本和分页内容使用同一个数据库快照，防止并发编辑混入草稿图集。

会员只能修改本人资料和商品；图集追加及新建草稿使用请求编号去重。商品和图集修改带 `expected_version`，并将已展示或待审内容撤回草稿。发布和审核再次检查当前分类配置、资料及全部图片状态，公开条件要求当前版本已审核、分类已启用。

管理员可以维护商品、审核、上下架、精选顺序、分类与筛选项、客服、说明页、首页展示配置、推荐参数及账号启停。引用中的筛选选项保留编码，以 `enabled:false` 停用。客服用途为 `general / sale / rent / listing / support`。审核记录保留商品内容及完整有序媒体清单；历史图集查询只支持本版已保存图集清单的审核记录，早期缺少清单的记录明确返回不可用。

可编辑设置只有两个键：

- `home_presentation`：首页轮播文案与间隔、热门 / 最新 / 为你推荐分区的顺序与数量、站内快捷入口及发布区文案。
- `recommendation`：版本、时间窗口、浏览权重及收藏权重。当前默认 `engagement-v2`、7个北京时间自然日内有效浏览×1、首次有效收藏×5。热门仅按浏览和收藏加权，上架时间不加热度分。历史字段 `new_bonus` / `new_hours` 为兼容旧配置保留；解析时 `new_bonus` 固定归零。

推荐结果由数据库按同一快照计算，公开API进程每15分钟刷新，也可由管理员手动触发。批次与实际采用的规则、截止时刻同时保存；更新配置在下次刷新时生效。所有来源商品均不计新品加分；旧站商品可凭本站有效浏览和收藏进入热门。首页按设置顺序去重，禁用或下架商品立即被可见性条件排除，超过60分钟的陈旧快照回退最新次序。会员按用户ID、游客按签名Cookie与商品、北京时间日期去重；本人及后台浏览不计，并实施 Redis 频控。

公开目录及热门、最新列表省略重复的完整介绍以减少传输；详情与首页精选保留完整介绍，所有位置仍保留完整标题。推荐的日期边界、冷启动、参数与实测口径见[推荐算法说明](../../文档/开发产出文档/10-recommendation-design--热门推荐算法.md)。

核心模块及依赖方向：

| 位置 | 职责 |
| --- | --- |
| `cmd/qingye` | 解析命令，选择服务启动或显式维护任务。 |
| `internal/config` | 部署配置、默认值和范围验证。运营配置继续保存在数据库。 |
| `internal/bootstrap` | 连接池、服务依赖装配、后台任务归属与关闭资源。 |
| `internal/transport/httpapi` | Gin路由、Cookie、Origin、请求限制、参数解码、响应及聊天协议入口。 |
| `internal/application` | 商品、会员、管理、身份、推荐、聊天及媒体用例，定义仓储和外部能力接口。 |
| `internal/domain` | 普通数据类型、纯业务规则、状态转换和可公开错误。 |
| `internal/infrastructure/postgres` | 参数化SQL、事务、锁、查询投影及版本冲突检查。 |
| `internal/infrastructure/cache` | Redis限流及聊天通知等适配。 |
| `internal/infrastructure/security` | 随机令牌、散列和密码校验容量。 |
| `internal/infrastructure/mediafiles` | 受控文件路径、图片校验与派生缩略图。 |
| `internal/maintenance` | 显式迁移、开户、历史导入和媒体核验；[初始结构](internal/maintenance/schema.sql)及[增量迁移](internal/maintenance/migrations/)均在此目录。 |

HTTP入口调用应用服务；应用服务依赖纯领域规则与接口，基础设施实现接口。服务层不接收 `gin.Context`，也不直接拼接SQL或访问磁盘。写用例的锁、版本检查、状态转换、持久化和审计保持完整事务。会员商品写入与分类规则修改通过分类共享/排他锁协调；图片重排在商品行锁下批量更新。HTTP、WebSocket和WebTransport直接复用聊天服务，不伪造HTTP请求调用业务处理器。

## 自测

R15新增 `PRODUCT_CREDENTIAL_KEY`：32字节随机值的Base64，用于独立QQ账号资料的AES-256-GCM加密。同一业务数据库的public/admin进程必须使用同一把密钥，隔离验收另配；不填时普通业务可运行，私密资料操作明确不可用。密钥不能放入前端变量、Git、日志或公共备份。恢复数据库必须恢复相应私有密钥。迁移0005/0006后，旧API的每商品多会话假设已不成立，回退前需核对数据库兼容性。

普通 `go test -tags=nomsgpack ./...` 运行单元检查；需要真实 PostgreSQL / Redis 的集成测试在未配置环境时跳过。完整集成验证设置 `TEST_DATABASE_URL` 和 `TEST_REDIS_URL` 后执行：

```sh
go test -tags=nomsgpack ./... -count=1 -v
go vet -tags=nomsgpack ./...
```

数据库测试每次创建唯一 schema，完成后只删除自身 schema，不清空共享数据库；Redis 只使用专属测试标识及测试库。测试数据库账号需有创建 schema 权限。覆盖旧数据幂等及原价保留、图片校验、权限隔离、草稿公开边界、版本冲突、上传重试、审核与下架、动态筛选、后台配置、推荐参数及停用会话等流程。实际部署还需进行浏览器及反向代理链路验收。

当前测试IP已启用HTTPS；正式上线仍需完成正式域名、真实短信、经营方审核规则及异地备份恢复方案。三类旧商品的报价单位已经确认。本项目仍是测试联调环境，不表述为正式生产交付；测试结果与发布版本以对应验收记录为准。
