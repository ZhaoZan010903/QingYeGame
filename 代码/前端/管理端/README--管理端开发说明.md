# 清叶独立管理前端

包名 `qingye-admin`，版本 `0.7.0-test.1`。这是独立 React/Vite 项目，拥有独立依赖、登录页、路由、构建目录及部署入口。管理前端测试端口为 `5181`，同源 `/api/v1` 只转发管理 API `5182`。用户站 `5179` 不提供管理页面或管理接口代理。

```sh
pnpm install --frozen-lockfile
pnpm test
pnpm build
pnpm dev
```

复制 `.env.example` 为 `.env.local`。`VITE_API_BASE_URL=/api/v1` 保持同源请求；`QINGYE_ADMIN_API_PROXY` 仅用于开发/预览代理。`VITE_PUBLIC_SITE_URL` 用于在新标签查看用户站及公开商品，并不共享登录状态。所有 `VITE_` 值会进入浏览器，禁止放入密码、数据库地址或供应商密钥。

管理后端只接受管理员账户，使用独立 `qingye_admin_session` Cookie 和管理会话 audience。前端还会检查明确的 `role=admin`，服务错误直接显示，不回退到演示数据。没有会员注册、收藏或会员投稿路由。管理图片使用 `/uploads` 与 `/admin/products/:id/images`，不调用会员图集接口。

管理功能包括商品审核、编辑、图集追加及排序、上下架、人工精选、会员查询和停用、客服与二维码、分类筛选、说明页面、首页和推荐配置。写入保留服务端版本冲突错误，未保存内容不会自动覆盖新版本。经营方已确认出售元、时日出租元/日、周月出租元/月，分别显示服务端account/day/month正式报价；原价和原分类保留。只有不能安全映射、仍为unknown的异常条目显示待核对说明。

`../共享` 只包含两个应用共用的纯类型和首页配置解析函数，不依赖 React 组件、用户应用、样例商品或浏览器存储。管理项目不导入 `../用户端/src`；用户站不产出管理模块。

部署 `dist/` 到独立站点，所有页面路由回退至本项目 `index.html`，API错误不得回退到HTML。真实管理登录、Cookie、Origin、上传和审核联调需要管理入口 `http://152.136.252.204:5181`（其 API 代理 `5182`）；不能借用户站 origin 验证管理写入。
