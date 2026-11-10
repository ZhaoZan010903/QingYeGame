# 清叶 React 前后端联调前端

包名 `qingye-storefront`，版本 `0.7.0-test.1`。此目录是用户站，连接 Go 后端及会员会话，属于测试环境联调版本，不表示正式生产切换。152 服务器的 `5179` 使用此版；管理前端是独立项目 `../管理端`，使用 `5181` 入口及管理 API `5182`。原型另行归档并保留原 `5178` 服务。

React 19、TypeScript、Vite，沿用 `../../../文档/原型代码/HTML/` 的黑曜石与香槟金视觉。图片、字体和样式在本地构建，不依赖 Tailwind CDN。

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

业务应用默认 `VITE_DATA_MODE=api`、`VITE_API_BASE_URL=/api/v1`。必须显式设置 `VITE_DATA_MODE=demo` 才使用旧样例兼容分支；演示不代表真实库存、登录身份、发布审核或客服服务。

连接实际服务时设置以下配置并重新构建：

```dotenv
VITE_DATA_MODE=api
VITE_API_BASE_URL=/api/v1
VITE_SMS_ENABLED=false
VITE_ENVIRONMENT_LABEL=测试环境 · 旧站商品快照
# 本地开发或构建预览时使用，静态部署通过Nginx转发API
QINGYE_API_PROXY=http://127.0.0.1:5180
QINGYE_WEB_PORT=5179
```

API 模式的请求失败会显示错误，不自动回退至样例。演示账户入口只在 demo 模式提供；演示不发送短信、不创建服务器会员、不将投稿送入真实审核后台。

`VITE_ENVIRONMENT_LABEL` 在首页及其他页面原提示位置显示环境说明；API 模式默认留空，不显示演示或样例提示。`VITE_SMS_ENABLED=false` 明确说明短信尚未启用，密码登录使用服务端开通的测试账户，凭据不写入前端代码。

经营方已确认出售按元、时日出租按元/日、周月出租按元/月；对应服务端`price_unit=account/day/month`，按正式`price_amount`展示并支持同单位预算及排序，原始价格和分类另行保留。不能安全映射的异常条目仍为`unknown`，使用`legacy_price`原样显示待核对说明，不添加币种或参与价格筛选；不得把异常后备状态写为全站固定提示。

演示资料保存在当前站点的 `localStorage` 键 `qingye.demo.v1`，图片保存在 IndexedDB 数据库 `qingye-demo-media`。退出演示账户不删除草稿，清理浏览器站点数据会删除这些本地资料。不要在演示中使用真实账号密码或私密凭据。

上传参数可以通过 `VITE_UPLOAD_MAX_BYTES`、`VITE_UPLOAD_BATCH_SIZE`、`VITE_UPLOAD_CONCURRENCY` 分别调整，初始值为 10 MB、每批20张、并发3张。这些值是前端初始参数，须与后端配置一致，不代表已确认的经营规则，也不限制商品图片总数。

开发与构建预览默认将 `/api` 转发至 `http://127.0.0.1:8080`，可通过 `QINGYE_API_PROXY` 修改；测试后端为 `5180`。后端接口契约见 [接口清单](../../../文档/前期设计文档/07-api-outline--接口清单.md)。客户端使用同源 Cookie，会话与权限由后端校验；旧 SaaS 接口不作为新系统的运行时依赖。

## 静态部署要求

用户站不包含 `/admin` 路由、管理入口或管理代码块。管理功能迁移到独立的 `../管理端` 项目；两个项目分别安装依赖、构建、部署及验证会话。`../共享` 只共享纯类型和首页配置解析，不共享应用入口与登录状态。

首页通过 `/home` 的 `presentation` 读取配置，显式错误的配置会报错，不静默回退。分类筛选和发布字段读取分类配置；卡片使用缩略图，详情保留原图。详情图集近底自动串行翻页，显示已加载数与总数，不设置总张数上限；失败保留当前页重试，版本变化须明确重新加载。离屏缩略图使用浏览器延迟加载和 `content-visibility`，原图仅按当前选中项加载。

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
