# React 预览发布记录

日期：2026年11月10日。

- 预览地址：http://152.136.252.204:5178/
- 发布目录：`/var/www/qingye-preview/releases/20261110-react-v1`
- 当前链接：`/var/www/qingye-preview/current`
- 前一 HTML 版本：`/var/www/qingye-preview/releases/20261110-v3-light`
- 配置备份：`/etc/qingye-preview/nginx.before-react-20261110.conf`
- 构建：`frontend/web` 中执行 `pnpm build`，使用明确标识的 demo 模式。
- index.html SHA-256：`03aeca0b1cd20e2e49da04602a970f96c467c576635c8f7c2d0622f33be74890`
- 私有完整源码包：`/root/qingye-private/qingye-react-source-20261110.zip`

公开目录仅包含 dist 静态资源。源码包、交接资料与凭据不通过网站公开。图片、草稿与收藏的演示操作保存在访问者当前浏览器。

验证结果：Nginx 配置检查通过，服务 active 且 enabled；外网首页、列表与详情路由可用，刷新不再返回静态404；未接入的 `/api/v1` 返回503 JSON，不回退为首页。外网 Chrome 在320、390、768、1440px下检查无横向溢出，主列表列数1/1/2/3，未捕获页面异常。

本次只更新示例服务器的独立5178站点，未切换8.140生产服务，未更改示例服务器的Caddy站点。

## 回退

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

## 源码来源与GitHub

React用户前端以仓库提交`5ff1c9f32dfd4cb8272c35b2132b83853f72d092`为基础。2026年11月10日已通过GitHub连接在线读取并核对最新main及项目文档，确认与原归档一致。正式源码继续维护在私有仓库`ZhaoZan010903/QingYeGame`，本次提交保留远端历史，以非强制方式更新main。具体提交编号以GitHub历史为准。

服务器源码压缩包为首次预览交付快照，不含Git历史；最新开发与GitHub核验记录请以仓库为准。GitHub源码更新不改变当前已验证的5178静态构建，也不代表生产服务发布。
