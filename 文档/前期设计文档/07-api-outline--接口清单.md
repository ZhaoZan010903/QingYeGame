# 接口清单

## 1 约定

本稿保留接口职责与经营规则设计，当前React + Go测试联调版已实现主要流程，实际范围和限制见[测试联调说明](../开发产出文档/16-test-integration--测试联调说明.md)与[后端README](../../代码/后端/README--后端开发说明.md)。短信相关接口暂返回503；完整OpenAPI文件仍待整理，不能把本文所有建议参数视为已确认经营规则。

- 路径前缀统一为 `/api/v1`，以下表格省略前缀。
- 请求和响应以JSON为主。当前图片通过受限签名票据上传至本机媒体服务，后续可更换对象存储适配。
- 使用服务端 Cookie 会话；管理接口检查管理员角色，本人接口检查数据归属。
- 列表采用 `cursor` 和 `limit`；建议默认20条、上限50条。排序需带稳定的最终次序。
- 时间按 ISO 8601 传输，报价采用十进制字符串或空值，避免浮点误差。
- 成功返回 `data`；分页附 `next_cursor`。错误返回 `code`、`message`、必要的字段错误及 `request_id`。

## 2 注册登录

| 方法 | 路径 | 权限 | 主要参数 | 结果与限制 |
| --- | --- | --- | --- | --- |
| POST | `/auth/sms` | 游客 | phone、purpose | 发送验证码；频控和用途隔离 |
| POST | `/auth/register` | 游客 | phone、challenge_id、code、password、agreement | 注册成功建立会话 |
| POST | `/auth/login` | 游客 | method=sms/password 与对应凭据 | 建立 HttpOnly Cookie 会话 |
| POST | `/auth/password-reset` | 游客 | phone、challenge_id、code、new_password | 修改密码并撤销旧会话 |
| POST | `/auth/logout` | 会员 | — | 退出 |

## 3 公开展示

| 方法 | 路径 | 权限 | 主要参数 | 结果与限制 |
| --- | --- | --- | --- | --- |
| GET | `/categories` | 公开 | — | 分类、筛选配置 |
| GET | `/products` | 公开 | category、trade_type、price_unit、q、filters、min_price、max_price、sort、cursor、limit | 仅返回当前版本已审核、已展示且分类启用的商品 |
| GET | `/products/{id}` | 公开 | — | 公开详情与首批图集；下架只返回状态，不返回原资料 |
| GET | `/home` | 公开 | — | 横幅、精选、热门、新品；无重复商品；原型支持可选展示配置presentation，见下文 |
| POST | `/products/{id}/view` | 访客 | 服务端签发的访客标识随 Cookie 携带 | 记录有效浏览，同商品/访客/日期去重 |
| GET | `/contacts` | 公开 | purpose | 客服配置 |
| GET | `/pages/{slug}` | 公开 | — | 已发布说明页 |
| GET | `/products/{id}/images` | 公开 | cursor、limit、expected_version | 按顺序读取后续展示图片；每次检查公开条件和图集版本 |

### 3.1 首页展示配置的原型扩展

`data.presentation` 为可选对象，包含 `editorial`（标题、说明、轮播间隔）、`sections`（hot/newest来源、标题、说明、显示数量及数组顺序）、`shortcuts`（限定站内入口）与 `publishing`（发布区文案）。字段与校验见[原型工程说明](../原型代码/15-prototype-architecture--原型工程说明.md)。缺省使用前端默认展示配置；显式空数组隐藏对应分区，库存为空不使用演示商品补足。接口商品次序由服务端决定。

此字段已接入测试版管理后台与数据库保存；非法配置明确报错，不替换成样例。首页数据仍由服务端排序，推荐参数保存后在下次计算批次生效。

## 4 个人中心与发布

| 方法 | 路径 | 权限 | 主要参数 | 结果与限制 |
| --- | --- | --- | --- | --- |
| GET | `/me` | 会员 | — | 本人资料 |
| PATCH | `/me` | 会员 | nickname | 只能改允许字段 |
| GET | `/me/favorites` | 会员 | cursor、limit | 本人收藏 |
| PUT | `/me/favorites/{product_id}` | 会员 | — | 重复收藏仍只有一条 |
| DELETE | `/me/favorites/{product_id}` | 会员 | — | 取消收藏，保留首次收藏时间 |
| GET | `/me/products` | 会员 | status、cursor、limit | 本人发布及最近审核原因 |
| GET | `/me/products/{id}` | 所属会员 | — | 本人资料、图集总数和首批图片；后续分批获取 |
| POST | `/me/products` | 会员 | ProductWrite | 保存新草稿，source/owner_id 由后端写入 |
| PATCH | `/me/products/{id}` | 所属会员 | ProductWrite、expected_version | 更新内容；待审编辑撤回为草稿；已展示编辑停止公开并需重新提交 |
| POST | `/me/products/{id}/submit` | 所属会员 | expected_version | 校验资料后进入待审核 |
| POST | `/me/products/{id}/withdraw` | 所属会员 | expected_version | 待审核转草稿，已展示转下架；保留审核记录 |
| GET | `/me/products/{id}/images` | 所属会员 | cursor、limit、expected_version | 读取当前图集，返回上传处理状态及有权访问的预览 |
| POST | `/me/products/{id}/images` | 所属会员 | media_ids、expected_version、request_id | 分批追加已校验的本人图片；重复请求不重复关联 |
| PATCH | `/me/products/{id}/images/{media_id}` | 所属会员 | before_media_id、expected_version | 移至指定图片之前；before_media_id 为空时移至末尾 |
| DELETE | `/me/products/{id}/images/{media_id}` | 所属会员 | expected_version | 移除本商品关联，不删除仍被审核记录引用的文件 |

## 5 图片上传与预览

| 方法 | 路径 | 权限 | 主要参数 | 结果与限制 |
| --- | --- | --- | --- | --- |
| POST | `/uploads` | 会员/管理员 | filename、mime_type、byte_size | 返回受限签名上传参数和 media_id |
| PUT | `/uploads/{id}/content` | 持短期票据 | token、原始文件字节 | 本机受控接收，重复票据不覆盖已接收文件 |
| POST | `/uploads/{id}/complete` | 上传者 | — | 服务端核验实际文件、处理图片 |
| GET | `/media/{id}/preview` | 上传者/管理员 | — | 私有素材短期预览；先检查归属 |
| GET | `/media/{id}/file` | 按当前公开条件或所属权限 | 可选variant=thumb | 本机原图或派生预览，下架/撤回后重新检查权限 |

## 6 管理后台

| 方法 | 路径 | 权限 | 主要参数 | 结果与限制 |
| --- | --- | --- | --- | --- |
| GET | `/admin/products` | 管理员 | status、source、q、cursor、limit | 全部商品和待审列表 |
| GET | `/admin/products/{id}` | 管理员 | — | 商品资料、当前版本、审核摘要、图集数量及首批图片；私有图集分批读取 |
| POST | `/admin/products` | 管理员 | ProductWrite | 平台商品草稿 |
| PATCH | `/admin/products/{id}` | 管理员 | ProductWrite、expected_version | 维护商品并保存为草稿；展示中编辑需停止公开，保留操作记录 |
| POST | `/admin/products/{id}/review` | 管理员 | decision=approve/reject、reason、expected_version | 只审核当前待审版本，通过则公开 |
| POST | `/admin/products/{id}/status` | 管理员 | action=submit/offline、expected_version、reason | 进入待审或管理员下架；下架必须有原因 |
| PUT | `/admin/products/{id}/featured` | 管理员 | manual_rank | 只对已展示商品生效 |
| GET | `/admin/categories` | 管理员 | — | 全部分类及筛选配置，包含已停用项目 |
| PUT | `/admin/categories/{id}` | 管理员 | name、price_units、filter_config、sort_order、enabled | 编辑筛选配置；引用中的选项停用而非直接删除 |
| POST | `/admin/categories` | 管理员 | code、name、trade_type、price_units、filter_config | 新增分类 |
| GET | `/admin/contacts` | 管理员 | — | 查看含停用客服的列表 |
| POST | `/admin/contacts` | 管理员 | name、purpose、channel、contact_value、qr_media_id | 新增客服 |
| PUT | `/admin/contacts/{id}` | 管理员 | 客服字段、enabled | 维护二维码和联系方式 |
| GET | `/admin/pages` | 管理员 | — | 说明页含草稿 |
| PUT | `/admin/pages/{slug}` | 管理员 | title、body、published | 保存页面 |
| GET | `/admin/settings` | 管理员 | — | 品牌和推荐等配置 |
| PUT | `/admin/settings/{key}` | 管理员 | value | 按 key 校验结构并保存 |
| GET | `/admin/users` | 管理员 | q、cursor、limit | 会员查询、手机号最少展示 |
| PATCH | `/admin/users/{id}` | 管理员 | enabled | 停用立即撤销会话；已发布商品需管理员明确处理 |
| GET | `/admin/recommendations` | 管理员 | — | 排名、分数构成和更新时间 |
| POST | `/admin/recommendations/refresh` | 管理员 | — | 触发可重复执行的榜单计算 |
| POST | `/admin/products/{id}/publish` | 管理员 | expected_version | 平台录入或旧数据核对后直接校验发布；写入审核记录，不用于绕过会员投稿审核 |
| GET | `/admin/products/{id}/images` | 管理员 | cursor、limit、expected_version 或 review_id | 读取当前图集或指定审核版本的图片清单，逐批授权预览 |
| POST | `/admin/products/{id}/images` | 管理员 | media_ids、expected_version、request_id | 分批追加可用于该商品的媒体，记录操作人 |
| PATCH | `/admin/products/{id}/images/{media_id}` | 管理员 | before_media_id、expected_version | 增量移动图片，保持商品内排序唯一 |
| DELETE | `/admin/products/{id}/images/{media_id}` | 管理员 | expected_version | 移除关联并保留必要的历史素材 |

## 7 主要数据结构

### 7.1 ProductWrite

| 字段 | 含义 | 校验要求 |
| --- | --- | --- |
| category_id | 分类编号 | 分类已启用，与交易类型一致 |
| title | 标题 | 提交审核时必填，最多100字 |
| trade_type | 出售或出租 | sale / rent |
| price_amount | 展示报价 | 非负十进制字符串，或空值表示联系客服报价 |
| price_unit | 报价单位 | 出售为 account；出租为 hour/day/week/month |
| attributes | 账号属性 | 按分类配置校验编码、类型、单选或多选 |
| description | 商品介绍 | 不包含密码、验证码或身份认证资料 |

保存草稿允许业务资料未填全；分类、交易类型及字段类型等基础结构仍需有效。完整必填检查在提交审核或管理员发布时执行。

旧站迁移另有 `price_unit=unknown`、`legacy_price`、`legacy_category`，原价仅作待核对展示，不自动补币种，也不参与预算与价格排序。原始载荷仅保存在数据库，不通过公开接口返回。旧标题超过100字可原样保留，修改为新标题时按新内容限制校验。

图集不作为 ProductWrite 中的完整数组传递。先创建草稿，再通过图集接口分批追加、移除或排序；提交审核时至少有一张有效图片。图集接口成功后返回最新商品版本，前端串行提交同一商品的图集变更，避免上传并发导致版本冲突。

`owner_id`、`source`、`status`、`approved_version`、`manual_rank`、`published_at` 不属于会员可写字段。请求出现不允许字段时返回明确错误，不能照单写入数据库。

### 7.2 商品列表筛选

filters 按分类配置的分组编码与选项编码传递。同组多选为 OR，不同组为 AND。后端拒绝未知分组和选项，不把参数拼接成 SQL。

公开商品列表还应支持 `trade_type` 与 `price_unit`。价格范围和价格排序必须在同一报价单位下使用；缺少单位且结果包含多种租赁单位时返回参数错误，并由前端提示选择单位。

sort 可选择最新、热度、价格升序和价格降序。客服报价商品不参与价格范围筛选，价格排序时排在有数值报价之后。

### 7.3 登录凭据与协议

短信登录提交手机号、challenge_id 和 code；密码登录提交手机号和密码。注册需提交当前协议版本及已同意状态，服务端核验，不仅依赖前端勾选。短信登录未注册的手机号应引导注册，不在用户未确认协议时自动建号。

注册及登录失败的提示避免暴露验证码、内部异常或不必要的用户资料。密码重置完成后要求重新登录。

### 7.4 大图集

不设置商品图片总张数上限。建议追加请求每批20张、图片页默认20张且单页最多50张；这些是单次传输参数，客户端可持续请求下一批。

返回 `total`、`next_cursor` 和商品版本，编辑端显示已保存数量。分页时携带初次返回的版本，图集发生变化则返回409，前端重新加载并保留尚未上传的队列，不把不同版本拼成同一图集。

会员和管理员追加、移除、排序图片均视为内容编辑：检查归属和当前版本、增加版本号，并按复审规则转为草稿；修改已展示商品时停止公开。管理员可在完成修改后发布平台商品，会员投稿仍需提交审核。

`request_id` 用于追加请求去重；已使用的同一请求编号若携带不同内容应拒绝。移除未提交引用前检查商品归属，已关联他人商品的媒体不能复用。处理中或失败的图片不能在提交时被当作可展示图片。

### 7.5 推荐响应与缓存

`/home` 附带推荐规则版本、计算批次和计算时间；补位商品标明来源为新发布，不伪造热度。后台推荐接口返回每项分数构成、`batch_id`、实际生效的 `rule_version` 和最近计算状态。

`/admin/recommendations/refresh` 触发同一计算任务，已有任务运行时返回其状态，不并行生成重复批次。Redis 超时后使用数据库快照或新发布补位，公开接口仍检查商品当前版本和状态，不直接返回整页旧缓存。

`/admin/settings` 仅返回可管理配置；内部 recommendation_state 不接受管理配置接口写入。权重修改产生规则版本，计算完成后才更新实际生效批次。

## 8 状态操作

| 操作 | 允许来源 | 结果 |
| --- | --- | --- |
| 会员保存编辑 | 本人草稿、退回、下架、待审、已展示 | 内容版本增加，保存为草稿；待审撤回，已展示停止公开 |
| 会员提交 | 本人草稿、退回、下架 | 完整校验后转待审核 |
| 会员撤回 | 本人待审核 | 转草稿 |
| 会员下架 | 本人已展示 | 转已下架 |
| 管理员审核 | 当前待审核版本 | 通过转已展示；退回记录原因并转 rejected |
| 管理员直接发布 | 平台录入或旧站导入的草稿、退回、下架 | 完整校验，写入审核通过记录并公开 |
| 管理员下架 | 已展示 | 记录原因并停止公开 |

会员投稿必须经过待审核及管理员审核，不能调用平台直接发布接口。状态不满足时返回409；已下架再次下架等重复操作可返回当前状态，不能重复产生不一致记录。

状态处理按[待确认事项 R02](09-review-items--需求确认与审阅记录.md)的最终结论调整。涉及审核的写操作在同一事务中检查 `expected_version` 和状态；版本变化返回409。

## 9 公开数据与失效状态

- 未审核商品、用户私人联系方式、审核快照和私有对象键不得进入公开响应。
- 下架商品详情返回410及简要状态，页面据此展示“已下架”；未公开或不存在的商品统一返回404。
- 本人收藏可返回已下架的占位记录与取消收藏操作，不返回已撤回的图片和介绍。
- 下架、撤回或停用分类后，列表、详情、后续图片页和推荐均检查最新可见条件。
- 管理员查看完整资料也应通过授权的图片预览地址，不把私有对象直接改为永久公开。

## 10 错误状态

| HTTP 状态 | 场景 |
| --- | --- |
| 400 / 422 | 参数或业务校验失败，标明具体字段 |
| 401 | 未登录或会话失效 |
| 403 | 无权使用管理功能；本人资源越权查询可统一按404处理 |
| 404 | 资源不存在或无权查看 |
| 409 | 内容版本、状态或唯一性冲突 |
| 410 | 原公开商品已下架，返回简要状态 |
| 413 | 上传超出大小限制 |
| 429 | 短信、登录、浏览或上传请求超过限制 |
| 500 / 503 | 服务异常或暂不可用；前端保留已填内容并允许重试 |

上传完成通知、收藏和推荐刷新应可安全重试。产生新商品的请求需使用请求去重标识或等效方式，避免网络重试产生重复草稿。
