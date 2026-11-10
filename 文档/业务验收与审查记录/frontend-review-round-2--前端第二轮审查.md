# 第 2 轮：前后端契约与独立入口交叉检查

日期：2026-11-11。检查对象：`qingye-storefront`、`qingye-admin` 与 Go 后端当前工作树。此轮由前端实施者只读检查后端路由、身份校验及字段契约，后端实施者交叉检查前端；各自只修改负责范围内的确认缺陷。

本记录区分静态对照、浏览器接口模拟和真实服务检查。模拟通过不代表已完成独立管理端远程部署验收；新管理入口的真实 Cookie、Origin、上传和审核须在部署后复验。

## 检查范围与逐端点结果

接口前缀均为 `/api/v1`。依据实际 `Routes` 注册与处理函数核对，没有仅凭接口文档判断。

| 前端操作 | 实际方法和路径 | 后端处理及约束 | 对照结果 |
| --- | --- | --- | --- |
| 管理员登录、读取身份、退出 | POST `/auth/login`、GET `/me`、POST `/auth/logout` | `auth.go`：只允许当前 surface 的角色；独立 Cookie、session audience、enabled 与有效期检查 | 管理前端仅密码登录，要求明确 `role=admin`，不发送会员注册/SMS 请求 |
| 管理列表、搜索、状态筛选、分页 | GET `/admin/products` | `admin_products.go`：q/status/limit/cursor，列表返回 data/total/next_cursor | 游标不解析，原样透传；旧搜索响应不会覆盖最新查询 |
| 打开编辑、新建草稿、保存 | GET、POST、PATCH `/admin/products[/{id}]` | `ProductInput` 严格字段；新建 request_id；编辑 expected_version，保存转 draft | 前端不发送 source/owner/status；保留 attributes，未知报价传 null；409 保留未保存输入 |
| 通过、退回、提交、下架、平台直接发布 | POST `/admin/products/{id}/review`、`/status`、`/publish` | 当前版本锁与状态校验；退回/下架需原因；member 来源禁止直接 publish | decision/action/reason/expected_version 和前端按钮条件一致 |
| 人工精选、取消精选 | PUT `/admin/products/{id}/featured` | 仅当前公开商品；manual_rank 整数或 null | 前端留空发送 null，服务校验失败显示错误 |
| 管理图集读取、追加、移动、移除 | GET/POST `/admin/products/{id}/images`，PATCH/DELETE `/images/{media_id}` | `member.go` 的 imageActor 对 admin 路径要求管理员；每批 1–20；版本、所有权、去重检查 | 管理组件传入专用 transport；不调用 `/me/products`；同商品写入串行 |
| 上传、进度、处理完成 | POST `/uploads`、PUT 签名 content URL、POST `/uploads/{id}/complete` | `media.go`：filename/mime_type/byte_size，返回 media_id/upload_url/method/headers；ready 返回 id/status/url | raw PUT 使用服务给出的 Content-Type，complete 的 ready 字段与前端映射一致；失败不生成样例图 |
| 媒体预览与缩略图 | GET `/media/{id}/file[?variant=thumb]`、`/preview` | surface gate 与公开/所属条件先于本地文件及派生缓存读取；upload_audience 隔离票据 | 管理端相对 URL 保持管理源；用户卡片/缩略图优先 thumbnail_url，原图按选中项请求 |
| 分类新增、编辑及动态字段 | GET/POST `/admin/categories`、PUT `/admin/categories/{id}`；公开 GET `/categories` | enabled、trade_type、price_units、filter_config；引用项不能直接删除；保存时校验选项编码 | 用户发布按分类读取选项，标准字段及额外属性保留编码；文本等级修复见下文 |
| 客服新增、编辑、二维码 | GET/POST `/admin/contacts`、PUT `/admin/contacts/{id}` | purpose 包含 listing；二维码就绪/归属校验 | 用户侧 publish→listing 映射保留；发现并修复二维码回读和其他管理员保留原图的问题 |
| 说明页、首页配置 | GET `/admin/pages`、PUT `/admin/pages/{slug}`；GET `/admin/settings`、PUT `/admin/settings/{key}` | 页面纯文本；配置 key 白名单；首页结构及站内链接校验 | 新页面使用 PUT，未调用不存在的 POST；错误配置保留原始 JSON 并提示，不静默替换 |
| 会员查询及启停 | GET `/admin/users`、PATCH `/admin/users/{id}` | 手机号脱敏，enabled 布尔，停用撤销会话；禁止操作自身，保留可用管理员 | UI 不显示自身停用按钮，操作结果和错误均由真实响应决定 |
| 推荐参数、刷新、更新时间 | GET `/admin/recommendations`、PUT `/admin/settings/recommendation`、POST `/admin/recommendations/refresh` | config/rule_version/computed_at，刷新在事务锁下串行计算 | 参数保存与生效榜单区分，未保存修改时禁用刷新；错误不会假报成功 |
| 会员草稿、投稿、收藏与个人资料 | `/me/products`、`/submit`、`/withdraw`、`/me/favorites`、PATCH `/me` | public surface 注册；产品所有权与版本检查；管理员 surface 不注册这些写路由 | 只由用户项目调用；会员发布图片接口使用 public session |
| 详情完整图集 | GET `/products/{id}`、GET `/products/{id}/images` | 详情首 20 张；后续 limit/cursor/expected_version；RepeatableRead 内同时检查公开状态、版本和图集 | 公共游标虽为 base64、会员游标为十进制，前端均只透传；不设图集总量截断；409 重新取完整商品 |

## 入口与数据隔离

- `app.go` 按 APP_SURFACE 注册路由：public 不注册 admin，admin 不注册会员发布/收藏/个人资料写入及注册短信接口。管理只读预览也在 adminSurfaceGate 之后。
- `auth.go` 同时检查 Cookie 名、数据库 audience、用户角色、enabled 和过期时间；登录角色不符不签发会话。两个端口使用同一主机时 Cookie 仍按名称和 audience 隔离，不能仅依赖端口隔离 Cookie。
- 管理写入经 `memberMutation` 检查精确 Origin/Referer；上传票据与完成处理检查 upload_audience。前端请求保留 credentials，所有浏览器可见配置不含凭据。
- `productJSON` 明确投影，公开字段移除审核备注、approved_version 等；管理编辑 SQL 不改 legacy_price、legacy_category、legacy_payload。`price_unit=unknown` 仍以原价文本显示并排除价格过滤/排序；管理不会把原价自动写入核对报价。
- 构建产物扫描：用户站无 AdminPage、管理 API 路径或管理台文案；管理源文件/产物无 web/src、会员商品/收藏端点、演示存储或 fixtures 依赖。shared 只含纯模型、等级和首页配置函数。

## 发现、修复与回归

| 编号 | 具体触发与影响 | 修复与证据 |
| --- | --- | --- |
| R2-01 | 后台上传二维码保存后重进联系人，列表只有 qr_media_id 而 qr_url 为空，预览消失 | core 在 adminContacts 派生同源媒体 URL 并保留 ID；复核源码。真实数据库 `TestAdminContactPreservationAndCategoryContracts` 由 core 执行通过 |
| R2-02 | 管理员 B 仅修改管理员 A 已配客服的服务时间，保留原二维码仍被 owner 校验拒绝 | core 允许保留该联系人原绑定且 ready 的媒体；新增/替换仍限本人素材。上述真库用例覆盖跨管理员保留和禁止替换他人素材 |
| R2-03 | 分类单选属性经直接 API 传多个合法选项，原校验只检查选项存在 | core 新增单选数量与重复值检查；分类 PUT 变更 trade_type 也明确返回 IMMUTABLE_TRADE_TYPE，避免静默不生效 |
| R2-04 | 商品 v1 的后续图片请求返回409，原“重载图集”只换成 v2 图片，标题/介绍/价格仍是 v1 | 前端改为重新请求完整公开商品并按 id/version 重建详情。浏览器确认商品 GET 共2次，标题、介绍、价格和202张图片均为 v2；重取返回404/410时旧正文和图集移除 |
| R2-05 | 真实迁移等级“彩钻”等被 Number(...) 转换，详情出现 VIP NaN | shared/readLevel 保留文字及有限数字；两个 API mapper、详情和会员发布输入同步兼容。用户5项及管理1项新增回归覆盖中文等级、数字、缺省/无效数字和写回属性；浏览器验证“彩钻”可见、无 NaN |
| R2-06 | 放大弹窗第20张点击下一张触发慢分页，用户先退到19张，旧异步响应会强制推进回20张 | 返回时仅在当前选择仍等于发起索引才推进。650ms 延迟浏览器回归：用户退到19张后返回仍为19/40 |

## 本轮执行的验证

- 用户项目 TypeScript/build 通过，46 项单测通过；管理项目 TypeScript/build 通过，11 项单测通过。
- 管理浏览器接口模拟在1440和390宽度检查匿名/会员拦截、管理员登录与退出、所有管理页布局、未知报价、完整长标题、409编辑保留、503重试、搜索乱序与错误配置保留。无页面异常、文档横溢或会员端点调用。
- 图集浏览器接口模拟覆盖202张、567张、503当前页重试、409完整商品重取，以及无 IntersectionObserver 时手动加载202张。顺序和末图可达；分页最大并发1；未选择其他原图前仅请求首张，选择末图后共2张原图 URL。1001张完整性另有状态机测试。
- 这些图集耗时使用小型 SVG 模拟响应，只用于交互/请求数量回归，不能当作真实服务器大图性能测量。真实缩略图处理、原图体积及冷缓存性能由部署后第3轮测量。
- 私有浏览器脚本、截图和 JSON 报告位于仓库外 `deploy-private/qa`，包括 round1-admin-report、round1-gallery-report、round1-isolation-report、round2-detail-visibility、round2-gallery-selection。文件中不写入登录凭据。
- 根据最新轮播反馈，使用最新用户站构建配合152测试站真实只读 `/home` 和本机媒体接口检查267字旧商品标题及真实拼图，没有 mock 商品或修改既有商品。桌面改为44%文案/56%图片独立网格，移动端图上文下；图片 contain 保留拼图边界，标题最多3行，旧站详细介绍只在详情完整呈现。1440/1280/390三个宽度的实际图片均加载完成、无图文交叠或横溢，截图经人工查看；记录为 `round2-live-visual-report.json` 和同名前缀截图。

## 部署后必须继续的检查

使用用户入口5179和独立管理入口5181分别登录；验证实际 Cookie/audience、伪造与跨端口 Origin、真实上传及媒体公开权限、会员提交→管理员审核→公开→下架、客服二维码回读、真实商品大图集全量到达与冷/热缓存耗时。只能对明确的联调测试商品做写入，已有576个迁移商品本轮不改报价或业务内容。完成前不把本记录表述为三轮全部通过。
