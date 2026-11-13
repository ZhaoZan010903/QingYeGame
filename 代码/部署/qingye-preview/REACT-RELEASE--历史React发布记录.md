# React 前端原型预览发布记录

> 停用说明：用户于2026年11月13日明确要求原型不再运行，5178服务已停止并masked。下文仅为历史发布与回退记录，不可据此启动或发布原型；当前状态见[停用说明](README--历史原型部署说明.md)。

## 当前原型版本：0.6.0-prototype.1

2026年11月11日，经经营方授权，将手机动画、性能和结构优化后版本发布至 [152原型预览站](http://152.136.252.204:5178/)。当前属于演示原型，不是正式业务前端交付。

- 当前目录：`/var/www/qingye-preview/releases/20261111-react-prototype-v3`；`current`原子切换至该目录。
- 上一版：`/var/www/qingye-preview/releases/20261110-react-v2`；更早的React v1与HTML参考版均保留。
- 构建：`代码/前端/用户端`，包名`qingye-web-prototype`，版本`0.6.0-prototype.1`，demo模式；28项测试、TypeScript及Vite构建通过。
- index.html SHA-256：`1a84219f4a213ce1fdf110a4733d0ee997ff9a8248cf91ad0ad175cb0e7e3e68`；共16个公开静态文件，完整清单随私有发布记录保存。
- 私有源码快照：`/root/qingye-private/qingye-react-prototype-source-20261111-v3.zip`。
- 开发进度：`/root/qingye-private/development-progress-0.6.0-prototype.1.md`。更新`session-notes.md`和`frontend-handoff.md`，先前交接资料保留版本备份。
- 精确GitHub提交、源码包及全部公开文件的校验值：`/root/qingye-private/release-20261111-react-prototype-v3.json`。源码与进度同一次非强制提交，保留远端历史。

验证：独立`qingye-preview`服务active，配置检查通过；外网首页、集市、详情与登录路由正常；320/390/768/1440px无横向溢出。单标题过渡、两个6条分区、同一搜索输入归位、排序和演示个人中心通过浏览器检查，页面异常为0。公开资源逐个与本地构建核对SHA-256；未配置的`/api/v1/home`明确返回503 JSON。

性能测量条件、结果及配置扩展边界见[开发进度](../../../文档/计划/14-development-progress--开发进度.md)与[原型工程说明](../../../文档/原型代码/15-prototype-architecture--原型工程说明.md)。本次只发布152服务器5178预览站，未修改8.140生产站、Caddy服务或业务数据库。私有交接资料不存放在公开站点。

### 回退到上一预览版

```sh
test ! -e /var/www/qingye-preview/current.rollback-v2
ln -s /var/www/qingye-preview/releases/20261110-react-v2 /var/www/qingye-preview/current.rollback-v2
mv -Tf /var/www/qingye-preview/current.rollback-v2 /var/www/qingye-preview/current
systemctl reload qingye-preview
curl -I http://127.0.0.1:5178/
```

## 历史预览：0.5.0

2026年11月10日曾将界面改版临时切换至`20261110-react-v2`，之后按新增优化要求发布上述原型版。0.5.0构建和目录保留；其入口SHA-256为`a507313b5b7beedc3b0211eeceec6f95db00e07b5cd1265a726c3aa988838814`，6个静态文件核验通过。GitHub源码及开发进度与本次0.6.0-prototype.1统一同步，不单独声称0.5.0已同步交接源码。

## 历史版本：0.4

日期：2026年11月10日。

- 预览地址：http://152.136.252.204:5178/
- 发布目录：`/var/www/qingye-preview/releases/20261110-react-v1`
- 当前链接：`/var/www/qingye-preview/current`
- 前一 HTML 版本：`/var/www/qingye-preview/releases/20261110-v3-light`
- 配置备份：`/etc/qingye-preview/nginx.before-react-20261110.conf`
- 构建：`代码/前端/用户端` 中执行 `pnpm build`，使用明确标识的 demo 模式。
- index.html SHA-256：`03aeca0b1cd20e2e49da04602a970f96c467c576635c8f7c2d0622f33be74890`
- 私有完整源码包：`/root/qingye-private/qingye-react-source-20261110.zip`

公开目录仅包含 dist 静态资源。源码包、交接资料与凭据不通过网站公开。图片、草稿与收藏的演示操作保存在访问者当前浏览器。

验证结果：Nginx 配置检查通过，服务 active 且 enabled；外网首页、列表与详情路由可用，刷新不再返回静态404；未接入的 `/api/v1` 返回503 JSON，不回退为首页。外网 Chrome 在320、390、768、1440px下检查无横向溢出，主列表列数1/1/2/3，未捕获页面异常。

本次只更新示例服务器的独立5178站点，未切换8.140生产服务，未更改示例服务器的Caddy站点。

### 首次交付回退到HTML

在示例服务器运行：

```sh
cp /etc/qingye-preview/nginx.before-react-20261110.conf /etc/qingye-preview/nginx.conf
nginx -t -c /etc/qingye-preview/nginx.conf
ln -s /var/www/qingye-preview/releases/20261110-v3-light /var/www/qingye-preview/current.rollback
mv -Tf /var/www/qingye-preview/current.rollback /var/www/qingye-preview/current
systemctl reload qingye-preview
curl -I http://127.0.0.1:5178/
```

不删除其他站点、不重启系统默认Nginx。切回React时使用本仓库nginx.conf，并将current指向已保存的React发布目录。

### 首次React来源与GitHub

React用户前端以仓库提交`5ff1c9f32dfd4cb8272c35b2132b83853f72d092`为基础。2026年11月10日已通过GitHub连接在线读取并核对最新main及项目文档，确认与原归档一致。原型源码继续维护在私有仓库`ZhaoZan010903/QingYeGame`，本次提交保留远端历史，以非强制方式更新main。具体提交编号以GitHub历史为准。

服务器源码压缩包为首次预览交付快照，不含Git历史；最新开发与GitHub核验记录请以仓库为准。GitHub源码更新不改变当前已验证的5178静态构建，也不代表生产服务发布。
