# 历史原型服务：已停用

2026年11月13日17:01（北京时间），用户明确要求原型不再运行。152服务器的`qingye-preview.service`已停止、取消开机启动并持久屏蔽（masked）。TCP/UDP 5178均无监听，服务器本机与公网地址连接失败；从开发机器连接公网5178也失败。

**禁止按旧发布记录恢复5178服务。** 后续开发使用用户测试站5179和管理测试站5181；如要恢复原型服务，必须取得用户新的明确指令。本目录的Nginx、systemd配置和[React发布记录](REACT-RELEASE--历史React发布记录.md)仅供历史查阅。

## 停用结果

| 检查项 | 结果 |
| --- | --- |
| 服务状态 | `inactive / dead`，`MainPID=0` |
| 启动状态 | `masked`，开机和常规手动启动均被阻止 |
| 5178监听 | 无 |
| 用户端5179、管理端5181 | HTTP 200 |
| API 5180、5182 | 健康，进程未重启 |

原服务单元与Nginx配置保存在服务器私有目录`/root/qingye-private/prototype-retirement-20261113/170113`。`/etc/systemd/system/qingye-preview.service`现在指向`/dev/null`。回执位于`/root/qingye-private/integration/20261113-prototype-retirement-receipt.json`。

旧构建产物、日志与历史私有交接资料仍在服务器磁盘中，本轮没有删除；它们不再通过5178提供访问。未修改云安全组；当前停用通过停止监听并屏蔽服务实现。

## 只读检查

```sh
systemctl show qingye-preview.service -p ActiveState -p SubState -p UnitFileState -p MainPID
ss -H -lntup 'sport = :5178'
```

第二条命令应无输出。不要执行旧文档中的启动、重载或原型发布操作。HTML和React原型代码已从当前仓库清理，查阅旧源码见[历史原型说明](../../../文档/原型代码/README--历史原型说明.md)。
