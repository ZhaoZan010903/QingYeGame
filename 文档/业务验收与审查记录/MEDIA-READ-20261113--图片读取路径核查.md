# 图片读取路径核查

日期：2026年11月13日，北京时间10:52起。范围：152测试环境与GitHub基线`714b9df9cf858127f2bc33e39cf389c158bf2c42`。本轮为只读核查，不是新增功能验收；未切换存储、启动复制、迁移或删除图片，也未修改服务配置。

## 1 当前结论

当前业务库的商品图片仍从152服务器本机返回。R20新媒体的SeaweedFS/OSS适配已经启用，但业务库暂时没有登记R20对象。历史图片的OSS读取预备代码仍关闭，不能将“存在OSS适配”理解为“现有图片都已改走OSS”。

| 证据 | 实查结果 |
| --- | --- |
| public/admin运行版本 | 两个进程均运行`/opt/qingye-test/releases/20261113-media-r20/qingye-api` |
| 运行二进制SHA-256 | `110f29741258a3e3566b5f0a9f6cc3e325531b67476ec573d0e833d6cc4d7f3a`，与[R20发布记录](../计划/RELEASE--测试环境发布记录.md)一致 |
| 非敏感运行配置 | 两端`MEDIA_R20_ENABLED=true`，环境为test；暂存Endpoint在本机，OSS Endpoint为北京公网 |
| 业务库就绪商品媒体 | 48,345条：48,332条`legacy/`路径，13条其他本机路径；无`r20/`路径 |
| R20对象登记 | `media_objects`为0条；没有可据此判定正在走OSS的新业务图片 |
| 公开旧图抽查 | 3张均存在本机文件；向运行中的public API请求128字节Range，均返回206、`image/png`及对应正文，无Location重定向 |

数据库查询使用只读事务，仅输出分类计数；图片样本只读取128字节，未下载全套图片。上述是核查时的快照，不保证后续新上传后的数量仍相同，也不表示已逐文件检验全部本机图片。

## 2 两套OSS代码的区别

| 路径 | 当前状态与作用 |
| --- | --- |
| `internal/infrastructure/mediafilesoss` | 历史图片读取预备，受`oss_read`构建标签隔离，业务装配未启用；即使OSS上已有部分副本，也不自动切换读取 |
| `internal/infrastructure/mediaobject` | R20新增媒体适配，由`bootstrap/runtime.go`按配置装配，当前双API已启用 |
| `mediaobject.Files.Open`遇到非`r20/`键 | 转交`Local.Open`，读取服务器原媒体目录 |
| `mediaobject.Files.Open`遇到R20 staging对象 | 读取本机私有SeaweedFS，经Go授权与校验后向浏览器返回 |
| `mediaobject.Files.Open`遇到R20 oss对象 | 生成60秒签名URL，由HTTP层302跳转，浏览器直接从OSS下载 |

R20分支依据数据库记录的有效对象位置读取，不是“本机没有就自动去OSS找”。对应入口见`代码/后端/internal/bootstrap/runtime.go`、`代码/后端/internal/infrastructure/mediaobject/files.go`与`代码/后端/internal/transport/httpapi/media.go`。

## 3 历史复制当前状态

核查时`qingye-oss-copy-20261112.service`未运行。私有迁移目录中没有`SUCCESS`、`completion-receipt.json`或`media-object-map-verified.jsonl`全量完成证据。

最后保存的`upload-progress.json`更新时间为2026年11月13日02:26:21北京时间，记录已验证8,803/48,345个文件，状态为`failed`；监督状态为`needs_attention`。这是进度文件中的记录，不是本轮重新枚举或校验OSS得出的实时对象总数。本轮未调查完整失败原因、重启复制或启用旧图读取。

复制与网站读取是两条独立流程。即使后台复制后续完成，也要核对全量及增量映射、权限和实际响应后单独启用；不能只取消注释就切换。

## 4 对R20-1防刷的影响

R20-1应先覆盖现有本机图片下载入口，其后分别验收R20 OSS的直接下载保护。只保护OSS签名接口，不能保护当前主要图片流量；只保护Go入口，也不能限制未来已签发OSS地址的重复下载。各路径的请求计数、缓存、Range与异常恢复需按实际实现验证。

聊天/头像沿用原权限与本机适配规则；本轮没有对私有附件进行越权访问，也不把公开商品抽查当作聊天附件验收。
