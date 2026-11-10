# 清叶 React 用户前端

React 19、TypeScript、Vite，沿用 `../home-preview/` 的黑曜石与香槟金视觉。图片、字体和样式在本地构建，不依赖 Tailwind CDN。

## 启动与构建

使用 Node.js 22.12 或更新版本、pnpm 10。在本目录执行：

```sh
pnpm install --frozen-lockfile
pnpm dev
```

本地地址为 `http://127.0.0.1:5173`。Windows PowerShell 无法运行脚本入口时可改用 `pnpm.cmd`。

```sh
pnpm test
pnpm build
pnpm preview
```

`build` 先执行 TypeScript 检查，再生成 `dist/`。`preview` 用于检查构建结果，不作为长期生产服务。

## 配置与服务边界

复制 `.env.example` 为 `.env.local` 后调整配置。`VITE_` 变量会进入浏览器构建产物，只能存公开配置，不得填写服务器密码、短信密钥或数据库凭据。

默认示例配置为 `VITE_DATA_MODE=demo`、`VITE_API_BASE_URL=/api/v1`。演示环境中的商品、图片与操作数据仅用于检查页面，不代表真实库存、登录身份、发布审核或客服服务。

连接实际服务时设置以下配置并重新构建：

```dotenv
VITE_DATA_MODE=api
VITE_API_BASE_URL=/api/v1
```

API 模式的请求失败会显示错误，不自动回退至样例。演示账户入口只在 demo 模式提供；演示不发送短信、不创建服务器会员、不将投稿送入真实审核后台。

演示资料保存在当前站点的 `localStorage` 键 `qingye.demo.v1`，图片保存在 IndexedDB 数据库 `qingye-demo-media`。退出演示账户不删除草稿，清理浏览器站点数据会删除这些本地资料。不要在演示中使用真实账号密码或私密凭据。

上传参数可以通过 `VITE_UPLOAD_MAX_BYTES`、`VITE_UPLOAD_BATCH_SIZE`、`VITE_UPLOAD_CONCURRENCY` 分别调整，初始值为 10 MB、每批20张、并发3张。这些值是前端初始参数，须与后端配置一致，不代表已确认的经营规则，也不限制商品图片总数。

开发服务器将 `/api` 转发至 `http://127.0.0.1:8080`。后端接口契约见 [接口清单](../../docs/07-api-outline.md)，本轮实现及验收边界见 [React 前端交接](../../docs/13-react-frontend.md)。Go 服务、短信与对象存储需要独立提供；不将旧 SaaS 数据接口视为兼容的新后端。

## 静态部署要求

将 `dist/` 作为站点目录，为 React 路由配置刷新回退。`/api/` 必须单独转发，接口不存在或失败时不得返回 `index.html`。

```nginx
location /api/ {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
}

location / {
    try_files $uri $uri/ /index.html;
}
```

生产入口使用 HTTPS 和服务端 Cookie 会话。Nginx 示例只说明路由规则，不替代证书、实际后端地址及现有服务器配置。GitHub 更新与现有生产服务切换分别管理。
