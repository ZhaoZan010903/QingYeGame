# 前端预览部署

当前5178已更新为React用户前端，原HTML版本保留。最新构建、验证和回退见 [React 发布记录](REACT-RELEASE.md)。以下保留独立服务的运维说明。

本次获准将静态首页部署到经营方指定服务器的 TCP 5178 端口。采用独立 Nginx 配置与 systemd 服务，不修改服务器已有 Caddy 的 80/443 站点，也不启动系统默认 Nginx 站点。

2026年11月10日已部署，外网访问 [http://152.136.252.204:5178/](http://152.136.252.204:5178/) 返回 HTTP 200，服务状态 active，开机启动 enabled。主机现有防火墙允许该端口，本次未修改其他防火墙规则。

## 文件位置

| 路径 | 用途 |
| --- | --- |
| /var/www/qingye-preview/releases/ | 按版本保存公开静态资源 |
| /var/www/qingye-preview/current | 指向当前公开版本的符号链接 |
| /etc/qingye-preview/nginx.conf | 仅监听 5178 的独立配置 |
| /etc/systemd/system/qingye-preview.service | 服务与开机启动 |
| /var/log/qingye-preview/ | 该服务的访问与错误日志 |
| /root/qingye-private/ | 非公开的接续资料、源码交接包及部署记录，目录权限 700 |

网页目录只保存公开构建产物，不存放密码、对话记录、源码压缩包或生产数据。对话接续资料经筛选和去除凭据后保存，不能称作完整聊天导出。

## 验证与管理

```sh
systemctl status qingye-preview --no-pager
curl -I http://127.0.0.1:5178/
nginx -t -c /etc/qingye-preview/nginx.conf
systemctl reload qingye-preview
```

发布时先上传新的 release 中完整 dist 资源，核对 SHA-256，再原子切换 current 链接。独立配置修改后先检查配置再重载本服务。回滚只需将 current 指向已保存的上一版本，再验证首页；不要重启或删除其他站点。

主机防火墙或云安全组应按经营方授权允许 TCP 5178。本次网页是 HTTP 静态预览，不接收账号密码或交易信息。对话与接续资料只通过 SSH/SFTP 取回，不通过 5178 公开。

## 换机器继续开发

从私有 GitHub 仓库取得源码，或通过现有 SSH/SFTP 访问 /root/qingye-private/。优先阅读 session-notes.md、frontend-handoff.md 与 README.md，再将交接包提供给后续 AI。登录凭据由经营方自行保管，本仓库和备份资料不保存密码。
