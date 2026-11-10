# 首页 HTML 预览

本目录保留黑曜石与香槟金HTML首页的历史视觉参考。同期[React原型](../React/)已归档，当前业务前端位于[代码/前端/用户端](../../../代码/前端/用户端/)。

直接打开 `index.html`，或在本目录运行 `python -m http.server 5178 --bind 127.0.0.1`。

编辑 `src/` 后执行 `python build.py`，生成单文件首页；可执行 `node --check src/compiled.js` 检查脚本。基础样式、图片、字体已内嵌，Tailwind 通过 CDN 加载。正式 React 工程应独立管理资源与构建样式。

`src/compiled.js` 是临时检查产物，不需要提交。生成概念图及原始提示词见 HTML 注释，不代表真实商品。后续开发阅读 ../../文档/前期设计文档/11-frontend-handoff--原始视觉设计交接.md。
