# QingYeGame

qingyeyouxi.com 网站自主建设项目。

项目采用 React 前端和 Go 后端，加入 Redis 缓存及独立图片存储，逐步替换现有 SaaS 建站，独立管理商品、会员、图片和运营配置。首期围绕商品展示、客服咨询和发布审核建设，新增首页热门推荐，并重新设计电脑端与移动端界面。

React 用户前端位于 [frontend/web](frontend/web/)，沿用首页光效增强版，包含首页、商品列表与详情、登录注册、收藏、发布、我的发布、个人中心和说明页。启动、构建及配置见 [前端说明](frontend/web/README.md)，实现边界与检查记录见 [React 前端交接](docs/13-react-frontend.md)。

前端提供明确标识的本地演示模式与独立 API 模式。Go 接口、短信、对象存储、管理员审核和生产数据迁移尚未交付；API 模式失败不会回退为样例数据。页面完成不代表这些后端服务已经运行。

原 [frontend/home-preview](frontend/home-preview/) 保留为 V3 视觉参考，相关要求见 [前端设计交接](docs/11-frontend-handoff.md)，预览部署方式见 [部署说明](deploy/qingye-preview/README.md)。

本次对话的需求与决定已整理为[会话接续记录](docs/12-session-notes-2026-11-10.md)，不是逐字聊天导出，已排除密码等凭据。

React 预览：[清叶用户前端](http://152.136.252.204:5178/)。该独立服务监听 TCP 5178，页面使用明确标识的演示数据，不连接业务后台。原 HTML 版本已保留供回退。发布目录、验证和回退见 [React 发布记录](deploy/qingye-preview/REACT-RELEASE.md)。

## 阅读顺序

先审阅以下四份文档，确认业务范围、用户操作和页面要求。

| 顺序 | 文档 | 主要内容 |
| --- | --- | --- |
| 1 | [项目概述](docs/01-project-overview.md) | 建设背景、目标、范围和交付要求 |
| 2 | [功能需求](docs/02-functional-requirements.md) | 按模块列出具体功能，采用 1、1.1、1.2 编号 |
| 3 | [业务流程](docs/03-business-flows.md) | 浏览咨询、发布、审核、修改和下架 |
| 4 | [界面设计要求](docs/04-interface-design.md) | 电脑端、移动端的页面布局与交互要求 |

技术部分供后续开发评审使用，不作为已确定的业务规则。

| 文档 | 主要内容 |
| --- | --- |
| [技术方案](docs/05-technical-architecture.md) | React、Go、数据库、Redis、独立图片存储及部署方式 |
| [数据库说明](docs/06-data-model.md) | 业务实体、字段、关联和约束 |
| [接口清单](docs/07-api-outline.md) | 接口职责、权限、主要参数和错误处理 |
| [实施与验收](docs/08-delivery-and-migration.md) | 开发顺序、SaaS 数据迁移、验收及上线交接 |
| [待确认事项](docs/09-review-items.md) | 已确认需求、建议参数和审阅记录 |
| [推荐算法说明](docs/10-recommendation-design.md) | 选型理由、热度公式、算例、Redis 榜单及运营调整 |
| [React 前端交接](docs/13-react-frontend.md) | 实现范围、接口约定、页面验收与接入边界 |

## 首期业务

- 用户浏览、搜索和筛选 QQ 炫舞账号，查看商品详情后联系平台客服。
- 保留联系客服上架；新增会员自主提交出售或出租商品，审核通过后展示。
- 商品图片不设固定总张数上限，采用分批上传、编辑和加载。
- 首页提供人工精选、近期热门和新发布商品。
- 管理员维护商品、审核、分类、筛选项、客服、内容和推荐规则。

在线订单、充值、积分支付、提现、佣金结算和租期自动化不列入本轮建设范围。

## 文档管理

React 前端与文档版本：`0.4`。更新日期：2026年11月10日。

已确认需求与建议方案在[待确认事项](docs/09-review-items.md)中分别记录。审阅时可直接指出文档名称、章节编号和希望调整的操作；确认后同步修改相关流程、页面、数据与验收要求。

仓库维护方式见 [CONTRIBUTING.md](CONTRIBUTING.md)，版本记录见 [CHANGELOG.md](CHANGELOG.md)。
