# 生产历史图片与后端候选审查记录

## 接管版最新验证与迁入状态

以下新增事实优先于下方已撤回复制候选的历史记录。接管版289个后端源文件（266个Go文件）已按LF规范化核对，清单SHA256为`ad41b15e8bf4eedb0b73c96961884c01350364faab84ba2f16459fe0b8d19fea`；专项206、全量692、race692均0失败0跳过，vet及CGO_ENABLED=0构建通过。源码与实际API二进制govuln均exit0、`No vulnerabilities found`；隔离HTTP功能418→并发55→安全168通过。

API SHA256为`de0c9b83ad5c31c928d08e7ea6c65cc31dd886c67f1e0b75a7419ff4a920afb4`，登记器为`f9580bfbd5ff5f91abffa0b2558b6d3b993d6e2fd235cc2649a9040ba206a563`。本地独立只读审查确认生产显式legacy前缀、R20新写路径隔离、测试无删除器时拒绝物理清理、接管证据和真实图片关联顺序核对、不可覆盖/复活已登记对象，无阻断发现。删除器为HEAD后精确DeleteObject，云端须持续禁止覆盖legacy原对象。

只读媒体工具19项、数据工具20项及真实SQL7组通过。48,332张legacy原图已完成HEAD/ACL并登记生产，存储写入0，完成manifest SHA256为`6058465469cf95f52c41f20edc6f489a28f5c82bd8523035db8a0d8e995ffd2c`。正式库已导入576商品、48,332图片关联、48,349必要媒体、10头像及1独立管理员，17份支持素材已落私有目录。

152双业务API于北京时间2026-11-14 23:44:24发布`20261114-test-legacy-delete-disabled-de0c9b83ad5c`，仅增加legacy删除禁用配置；11组业务/运营摘要、20迁移、静态及5178 masked保持，隔离API已恢复inactive。8.140双API和双网站已于北京时间2026-11-15 00:07:35启动、媒体工作器开启，精确VPC云删除策略已实测通过，18组正式浏览器及管理员登录/退出验收通过。Cloudflare重新授权后连接仍报scope错误；生产IP及真实验证码/短信未验证。当前不能写为正式上线。

## 下方为已撤回独立副本方案的历史记录

## 当前边界：下方为已撤回独立副本方案的历史记录

用户明确OSS仅40G，已撤回整套历史图二次复制。`copy_production_media.py --apply`已禁用，OSS精准PutObject Deny保留；38,573个本轮新增副本已精确清理，回收21,424,318,469字节，剩余0、错误0，源前缀完整inventory保持。不得根据下方历史“恢复复制”“独立对象前缀”等描述重启复制或启用生产。

下方665项全量、665项race与179项专项对应旧独立副本候选，不代表生产接管、测试删除隔离和生产引用清理已经通过。用户已确认保留原对象路径由生产接管，测试仅本地读且禁legacy OSS删除，不复制、不重命名；接管方案正在单独实现和验收，shared guard仅为迁入过渡；最终状态见[生产部署说明](../../代码/部署/qingye-production/README--生产环境部署说明.md)与[当前媒体说明](../../代码/工具/README-PRODUCTION-MEDIA--生产历史图片副本说明.md)。正式网站、API、数据及协议是否发布须核对新的实际回执。

2026-11-14，本轮完成了历史 OSS 读取、生产副本证据校验及媒体后台任务启动闸门的代码工作。152 随后出现 SSH banner 和 HTTPS 均无响应；用户表示稍后检查服务器，现已停止新增远端任务。本记录不能作为生产发布完成证明。

## 已实际通过

- 隔离后端专项 179、全量 665、race 665，均 0 失败、0 跳过；vet 通过。
- API 与 `media-replicas` 均以 `CGO_ENABLED=0` 构建成功。候选二进制 SHA256 尚未在失联前取回，不能填写推测值。
- 285 个后端源文件已在 152 私有目录 gofmt，本地源码与其逐文件 SHA256 一致。源码清单文件 SHA256：`e0713364d2930674dfcd4f0131a7080271b06070b5358643d587047246d9638f`。
- OSS 复制工具 6 项单元测试通过；1 个真实生产前缀对象的复制、ledger 恢复，以及再次复制触发的真实禁覆盖冲突均核验通过。源/目标元数据、CRC64、ETag 和私有 ACL 均已检查。
- 官方 Python SDK 已核实：`if_match` 对应 `x-oss-copy-source-if-match`，`forbid_overwrite` 对应禁覆盖头，复制响应 `hash_crc64` 来自服务端 CRC64 头。`OperationError` 的底层 `ServiceError` 必须解包；只有 `409 / FileAlreadyExists` 允许按已有目标重新验证。

## 尚未完成或状态未知

- 全量 OSS 复制计划为 48,332 张、26,841,243,424 字节。主任务最后已知进度为 34,310 完成、1 失败；失联后没有观察到完整回执或 `SUCCESS`，不能推测复制已完成。运行中的旧进程未动态加载后续的异常解包和日志修复，恢复后应使用已更新脚本。
- 源码 govuln 已启动，但没有取得成功结果；对应本机 SSH 会话已精确终止，远端扫描子进程是否退出仍待恢复后核对。二进制 govuln 尚未启动。
- 已核对原隔离双 API 的 `ExecStart`、旧 PID 和旧候选目录，随后尝试执行隔离候选切换脚本；未取得完成回执。切换是否开始、是否完成、是否已回退均需查看服务实际状态，不能宣称候选已发布。
- HTTP 功能→并发→安全串行验收脚本已准备并上传，尚未启动。本轮没有重跑已通过的 full/race，也没有通过该审查任务部署业务 test 站或操作 8.140。

## 恢复后核对位置

远端工作根目录：`/root/qingye-private/production-20261114/`。

- 构建源码与证据：`backend/`、`source-manifest.json`、`focused/full/race/vet/build/build-importer-summary.json`，以及各阶段私有日志；候选产物为 `qingye-api`、`media-replicas`。
- 扫描：`vuln.log` 及可能存在的 `vuln-summary.json`，必须核对实际退出状态。精确停止扫描的辅助脚本目前仅保存在本地 `tmp/production/stop-vuln.py`，失联期间上传失败。
- 隔离切换：`deploy-acceptance.py`、可能存在的 `acceptance-candidate-backup/previous.json`、`acceptance-release-receipt.json` 或回退记录。原隔离链接为 `/opt/qingye-test/acceptance-current`；切换前实际目标为 `/opt/qingye-test/releases/20261114-product-r23-acceptance-71ec0faf1475`，双服务为 `qingye-acceptance@public` / `qingye-acceptance@admin`。
- 复制：`qingye-production-media-copy.service`、`copy.log`、`oss-copy/progress.json`、`oss-copy/last-error.json`、`oss-copy/copy-ledger.sqlite` 及最终证据文件。不要把 ledger 记录数量当作本轮全量重核完成；恢复脚本会重新 HEAD/ACL 核对。
- 本地已观察结果汇总：`tmp/production/backend-observed-checks.json`；该文件明确区分已通过、未启动和状态未知。

恢复后先核对并停止仍占用资源的复制/扫描进程，确认业务 test API 与其他项目可用，再核对隔离 API 实际候选与旧候选。后续数据验证、HTTP 验收、扫描、复制采用串行或受控并发；不重复已过的 full/race，除非后端源码发生变化。

实现边界与证据格式见[生产历史图片副本说明](../../代码/工具/README-PRODUCTION-MEDIA--生产历史图片副本说明.md)。原测试对象不删除、不覆盖；生产使用独立对象前缀。未取得完整目标映射、复制完成证据及实际受控读图验收前，不开启正式媒体清理任务。
