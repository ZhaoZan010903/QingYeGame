# 管理端运营数据看板

2026年11月14日。用户要求增加管理端数据看板，并明确选择升级现有工作台。保留管理首页`/`和常用操作入口，增加真实数据总览、近7天/30天趋势、商品分布、期间商品排行及待办。基线`d9775f5c129f1d7140dbd4d093498e3d92515af9`；本轮不扩展订单、收入、支付或自动租期排期。

## 页面与统计口径

顶部提供最近7天/30天、数据更新时间、手动刷新及上架入口。当前商品、会员和待办与期间指标分区，避免切换时间后误认为库存是历史快照。趋势可切换指标并查看日明细；排行支持按浏览、收藏、咨询排序，最多10条，点击进入现有管理详情。保留审核、咨询、首页装修和帮助入口。沿用管理端现有白底蓝色主题，不新增图表依赖，手机采用单列模块及双列数字卡。

| 指标 | 口径 |
| --- | --- |
| 商品总数、状态、租售分布 | 当前未删除商品；回收站单列，排除已自动清理的会员墓碑 |
| 公开可见 | 同公开查询：已发布、当前版本获批、分类启用且未删除；不能只用published状态替代 |
| 会员 | role=member，总数及当前启用/停用；期间新增按创建时间，不含管理员 |
| 待办 | 待审核商品、上架处理中商品、待审昵称、未合并的等待接待会话分别计数 |
| 商品浏览 | 商品＋访客标识＋北京时间自然日去重；跨商品累计，不称全站PV或独立访客数 |
| 首次有效收藏 | 首次建立关系在期间内、当前仍有效；排除停用会员、管理员与商品所有者。不代表收藏动作量或净增长 |
| 首次商品咨询 | 会员对商品的首次咨询在期间内；同样排除停用、管理员及本人，不代表消息条数或重复咨询次数 |
| 趋势与排行范围 | 当前未删除商品，不限当前是否上架；随着商品删除、取消收藏或会员停用，历史范围汇总可能变化 |

按数据库时间固定本次统计截止时刻，时区`Asia/Shanghai`，最近N天包括今天，首日从零点起，今天统计截至查询时刻。按创建时间排除未来事件；每日序列补零。排行只含选中指标大于零的商品，按指标降序、商品编号和ID稳定排序。零数据明确显示为空，不生成演示数字，不从旧热门加权批次伪造30天结果。

## 接口与分层

只在管理入口注册`GET /api/v1/admin/dashboard?days=7&rank_by=views`。days仅7/30，rank_by仅views/favorites/consultations，缺省7/views；空值、重复参数及未知参数返回400。匿名、会员和public入口不可读取管理汇总。返回结构不包含手机号、访客标识、聊天正文、原始载荷或私密账号资料。

```text
AdminDashboard → adminApi.dashboard → 管理端HTTP权限适配
              → management.Service → PostgreSQL只读一致快照
```

```ts
type Dashboard = {
  generated_at: string; timezone: "Asia/Shanghai";
  range: { days: 7 | 30; start_date: string; end_date: string; start_at: string; end_at: string };
  products: { total: number; visible: number; sale: number; rent: number; trash: number; statuses: Record<string, number> };
  members: { total: number; enabled: number; disabled: number; new_in_period: number };
  todos: { pending_products: number; publishing_products: number; pending_nicknames: number; waiting_conversations: number };
  engagement: { views: number; favorites: number; consultations: number };
  trend: Array<{ date: string; views: number; favorites: number; consultations: number; new_members: number }>;
  ranking: { by: "views" | "favorites" | "consultations"; limit: 10; items: Array<{ id: string; product_code: string; title: string; trade_type: string; rental_category: string; status: string; views: number; favorites: number; consultations: number }> };
};
```

HTTP层仅解析与响应，不写SQL。服务校验管理员和参数，仓储在有界只读一致快照中汇总，避免跨表相乘或把总数从前端商品分页拼凑。复用现有浏览日期、有效收藏与首次咨询索引；不新增迁移或Redis统计缓存。前端取消过时请求并防乱序回填，切换范围时不会将旧数字标作新范围；失败提供重试，加载与空数据分开。

## 验证与发布边界

至少三轮审查分别覆盖业务口径、交叉实现和运行证据。真实数据库测试仅在隔离资源：权限、北京时间边界、7/30天、未来数据、缺日补零、取消/停用/本人过滤、状态/可见性/回收站和排行稳定性；检查并发读不会修改业务数据及查询性能。隔离HTTP按功能→并发→安全，浏览器检查切换/刷新、图表明细、排行、待办导航、空/错/慢响应和手机桌面布局。

最终只部署本轮变动的管理静态和共用API，用户静态保持R21-2。先核对GitHub、产物散列与测试证据，保留原版本及配置，发布后核对实际进程和只读页面。5178保持masked，8.140生产站不变。无数据库变更，应用回退仍须使用包含R21-1/R21-2既有规则的兼容基线。
