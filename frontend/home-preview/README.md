# 首页 HTML 预览

本目录为已授权发布的黑曜石与香槟金首页，包含增强背景流光及金属标题效果。完整 React 应用尚未开发。

直接打开 `index.html`，或在本目录运行 `python -m http.server 5178 --bind 127.0.0.1`。

编辑 `src/` 后执行 `python build.py`，生成单文件首页；可执行 `node --check src/compiled.js` 检查脚本。基础样式、图片、字体已内嵌，Tailwind 通过 CDN 加载。正式 React 工程应独立管理资源与构建样式。

`src/compiled.js` 是临时检查产物，不需要提交。生成概念图及原始提示词见 HTML 注释，不代表真实商品。后续开发阅读 ../../docs/11-frontend-handoff.md。
