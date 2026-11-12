# MEDIA-R20-ACCEPTANCE -- 商品媒体隔离验收记录

更新：2026年11月13日。本文记录实际执行范围。**最终后端、真实存储、限定卷回收及核心浏览器检查通过，三端已部署152测试环境20261113-media-r20；文档随本次提交更新，远端main与私有回执github_commit核对。** 8.140生产站和5178原型未切换。

关联：[实现架构与E-R](../开发产出文档/MEDIA-R20--媒体生命周期架构与故障一致性.md)、[Go安全范围](../开发产出文档/SECURITY-R20--Go安全模块设计.md)、[存储维护](../开发产出文档/STORAGE-R20--ECS存储选型与测试环境方案.md)、[审查清单](MEDIA-R20--业务数据库与Redis审查清单.md)、[实施清单](../计划/MEDIA-R20--实施与验收清单.md)。

## 最后审查修复

最后审查发现两项实质边界问题，已修复并在同版最终流水线复测：

1. **退回修改被旧草稿截止提前清理**：第4天退回使图片留存延至第9天，随后编辑回草稿可能仍沿用旧第5天截止。已使有效图片留存与草稿清理条件一致，普通文字保存不延长窗口，主动删除不等待留存期。
2. **停权清暂存后迟到候选仍保留五天**：处理中管理员停权清暂存，迟到StageCandidate可能重新登记五天窗口。已使用所有者→媒体锁序，候选登记重新核对撤销、清理意图、停权版本及当前处理租约；旧claim不能借用或释放新租约。

最终针对性回归21项、全量338项、race338项及R20专项45项×3均通过，无失败/跳过。正式测试入口已部署，下方记录独立的部署后核验，避免以修复完成代替发布证据。

## 1 版本与隔离

本轮从R19基线 `b5e97dd` 实施。最终后端验证与构建对应 `r20-backend.tar.gz` 源码包SHA-256，不是Git提交号：

```text
3a457c9cfcaddfda29c0ef91c5b0e79716284644d141c5cb4327e530a8a768eb
```

最终Linux二进制SHA-256，构建及该二进制govulncheck均通过：

```text
110f29741258a3e3566b5f0a9f6cc3e325531b67476ec573d0e833d6cc4d7f3a
```

该最终二进制已部署到隔离API5184/5185并通过健康检查；开发期已试跑过0012的私有验收schema也已同步最终函数定义。业务测试库已首次执行最终0012，不通过改写已发布迁移升级业务库。代码提交为 `db887ae0b7d2c7619ace79c366dc52c5e2e3d10d`，部署回执见下文。

前端最终包已重新通过自动化及构建；管理端构建配置 `VITE_PUBLIC_SITE_URL=https://152.136.252.204:5179`。最终tar已规范为Linux文件0644、目录0755、root归属，前端内容未改变；产物包SHA-256：

| 前端 | SHA-256 |
| --- | --- |
| 用户端 | `aab5cdbb75d6e469ade954996e96212404435dc2322107f350d25834fbdec65d` |
| 管理端 | `b68bb339a16863f76fbe4838e43a36e94f4b9a73af058a35600ab8c081b5e23e` |

后续若重新格式化、重打包或修改代码，最终源码包与提交散列应另填，不能把上述包散列自动套用到新包。后台私密证据位于152的 `/root/qingye-private/r20-20261113/`，凭据、Cookie、签名地址及真实图片不提交仓库。

隔离测试使用独立PostgreSQL/schema、Redis与媒体目录，S3固定为 `qingye-acceptance`，OSS限定本轮验收对象。真实图片全部为小型合成图；没有使用历史商品原图库作压力输入，也没有对旧数据启用新生命周期。API仅监听本机5184/5185，部署后健康检查通过。

## 2 已执行检查

| 检查 | 实际结果 | 范围与限制 |
| --- | --- | --- |
| 152 Linux后端全量 | 338通过，0失败，0跳过 | 真实隔离PG/Redis；普通对象故障测试使用受控适配，不能等同全量真实OSS压力 |
| 152 Linux后端race | 338通过，0失败，0跳过 | 同版竞态检查，不代表所有可能交错穷尽 |
| R20专项重复三轮 | 每轮45项，`-count=3`汇总135通过，0失败，0跳过 | 重复执行关键生命周期/并发/安全场景；不是新增135种功能 |
| 最后缺陷针对性回归 | 21通过，0失败，0跳过 | 覆盖最后留存与候选停权问题等具体回归 |
| 后端vet及构建 | 通过 | Go 1.27.4，沿用 `-tags=nomsgpack` |
| 源码与Linux二进制govulncheck | 均无已知漏洞报告 | 仅当前依赖与扫描规则可发现的问题，不保证绝对无漏洞 |
| 用户端自动化与构建 | 156项及构建通过 | 不代替实际浏览器文件选择、排序、手机交互验证 |
| 管理端自动化与构建 | 79项及构建通过 | 浏览器核心流程及390×844视口另见下节 |
| 存储维护脚本本地安全测试 | 12项通过 | 精确键、归属、删除传播等待、超时等脚本边界 |

以上338等数字是测试项计数，包含子测试，不是338个独立顶层函数。

本地Windows容量专项曾为9通过、1项符号链接测试因权限跳过；上表Linux全量/race为后续同版338项均无跳过，不能用Windows结果冒充Linux覆盖。

## 3 单原图真实300图闭环

`TestR20RealStorageUploadPublishReturnAndDelete` 使用真实SeaweedFS和OSS，300张合成图，每张输入3174字节，输入合计952,200字节。每媒体只产生一个 `original` 业务对象，**不生成独立preview**。已完成：

1. 受控上传、完整性检查及图集关联，保存显式顺序。
2. 审核后同步OSS，校验目标，再公开商品并清理旧源。
3. 下架后拒绝匿名新访问。
4. 在隔离fixture推进第一个五天窗口，迁回SeaweedFS并校验。
5. 推进第二个五天窗口，精确删除对象及验证最终状态。

最终同版整个测试用时 **42.04秒**。业务期限没有改成几分钟；测试使用隔离时钟/到期数据推动，不等待真实十天，也不更改共享环境五天规则。

该测试证明小文件在真实提供方之间的业务、顺序与清理闭环，**不证明300张真实大截图的传输时间、解码内存或生产并发容量**。浏览器99＋99＋99＋3的本地选择、拖动、进度体验需要独立浏览器证据，不能由Go测试替代。

### 两端实际浏览器验证

实际浏览器脚本在第三轮完成7个核心流程后，又在最终二进制 `110f2974…` 上完整重跑通过，报告finished时间为19:51:20 UTC；`pageErrors=0`，证据在仓库外 `deploy-private/r20-browser-results/report.json`。使用3172字节合成PNG，具体覆盖：

- 用户端先3张再追加99张，累计102张真实上传与关联；两端选择100张均整批拒绝，没有上传请求。
- 注入分块断网，界面显示真实进度和失败，重试后完成；不是只调用后端接口模拟页面成功。
- 上传后排序、删除封面及后续展示一致。
- 会员提交审核，管理批准后SeaweedFS→OSS自动推进；实际签名OSS图片加载成功。
- 管理员新建商品直接检查后上架，无需再审核自己。

最终二进制上的两端390×844视口复测也通过：操作控件可用、未被遮挡、在视口内，无横向溢出，`pageErrors=0`；主验收视觉复核通过。总进度移到队列“开始上传”按钮旁，仅显示一份。两端最终自动化及构建再次通过。

本次并非全站全面UI验收，也不是浏览器一次上传300张或所有手机尺寸的证据。刷新后恢复未完成本地File队列没有实现，不能由断网重试成功推导为刷新恢复。

## 4 故障与安全实现核对

以下测试入口包含在上述同版后端执行结果中；它们的具体输入、模拟故障和断言以源码为准。完整方案中的CDN、多实例字节租约及自动风控未实现，不能因此勾选整份设计清单。

| 测试入口 | 验证重点 |
| --- | --- |
| `TestR20SecurityEverydayBatchesRetriesAndSharedIP` | 正常300图、追加、重试与共享IP不误触持久停权 |
| `TestR20SecurityRedisFailureDoesNotBanOrCommit` | Redis失败拒绝受保护动作，不生成错误提交或误封 |
| `TestR20SecurityConfirmedBanRevokesOnlyProductStaging` | 管理员确认后仅撤销目标会员商品上传及所选暂存范围 |
| `TestR20SecurityChunkLimitCoversConfiguredSizesWithoutOverflow` | 分块窗口随配置计算且避免整数溢出 |
| `TestR20SecurityTwoThreeHundredFileWindowsAllowChunkRetries` | 正常批量分片重试与窗口边界 |
| `TestR20SecurityTwoThreeHundredCreateWindowsRemainBounded` | 两个请求窗口保持有界 |
| `TestR20SecurityCapacityRefusalPreventsTicketReservation` | 容量预检失败不预占上传票据 |
| `TestR20SecurityStorageFailurePreservesDraftAndNeverPromotesPartialMedia` | 接收、暂存、校验及持久化失败保留文字草稿，不公开半成品 |
| `TestR20SecurityInvalidImageReleasesReservationAndPreservesText` | 非法图片释放占用，不抹除有效文字 |
| `TestR20SecurityReadyCompleteBypassesBusyDecoderWithoutBypassingOwner` | 已完成重试不被繁忙解码队列阻断，仍检查归属 |
| `TestR20SecurityReadyCancellationAndAppendRollbackAreReferenceSafe` | 取消与关联回滚不误删有效引用 |
| `TestR20SecurityReadyCancelRacingAppendCannotDeleteTheWinner` | 取消/追加并发只有明确生效结果 |
| `TestR20SecurityProductCapacityDoesNotBlockOtherMediaPurposes` | 商品存储故障不错误扩展到聊天/头像等用途 |
| `TestR20StageCandidateRecoveryHonorsProcessingLease` | 候选回收尊重处理中租约 |
| `TestR20WorkerLeasesAndLateCompletionDoNotHoldConnections` | 慢I/O/迟到返回不长时间占用数据库连接 |
| `TestR20LatePutAfterRetiredDeleteRequeuesExactTarget` | 删除后迟到成功的外部写入仍纳入精确补偿 |
| `TestR20ExpiredLeaseWithoutCallbackIsReconciled` | 丢失进程回调后持久对账仍可收敛 |
| `TestR20RelistDuringReturnKeepsOSSAndCleansStaleTarget` | 回迁中重新上架保护有效OSS来源并清理失效目标 |
| `TestR20RetiredDeleteRetriesAndKeyCannotResurrect` | 旧键删除重试，不复用已退役代次 |
| `TestR20CrossProductTransitionsRestrictionsAndAppendLockOrder` | 跨商品状态、停权与追加的锁顺序 |
| `TestR20RejectedDraftKeepsImageGraceUntilItsActualDeadline` | 退回修改后回草稿，不被旧截止提前清理 |
| `TestR20DraftTextEditsDoNotRenewRetention` | 普通文字保存不无限续期 |
| `TestR20ExplicitDraftDeletionDoesNotWaitForImageGrace` | 主动删除不因留存窗口被延后 |
| `TestR20SecurityCandidateGateRechecksRevocationAndLease` | 候选登记复核撤销、停权和当前租约 |
| `TestR20SecurityCandidateOldClaimCannotBorrowOrReleaseNewLease` | 旧claim不能借用或释放新worker的租约 |

此外磁盘专项覆盖已有父目录探测、不创建目录、符号链接拒绝、空间/整数边界、whole/chunk接收与提交前检查、失败尾部恢复，以及低水位下读取/删除/已提交重试仍允许。逐项入口见后端 `mediafiles/disk_test.go`。计数和容量保护分别使用Redis窗口、PG既有额度和磁盘快照；没有宣称实现跨进程硬空间预留。

## 5 性能抽样

分层抽样每项80次，记录各自p95，不相加、不当作端到端开销：

| 测量 | p95 | 边界 |
| --- | --- | --- |
| PG上传限制读取 | 0.197617 ms | 单层真实数据库查询 |
| Redis Lua限频 | 0.096841 ms | 单层真实Redis调用 |
| HTTP上传状态，含身份/Redis/PG | 2.566764 ms | Go `httptest` 进程内，不含真实网络 |
| PNG解码、编码和本地I/O | 2.658479 ms | 小型合成PNG，不是大图容量 |
| 324次完整小图上传 | 22.029888 ms | 300四批加24日常/共享IP；内存对象适配，不含OSS网络 |

纯规则基准运行三次为16.98—18.18 ns/op、0 B/op、0 allocs/op，只反映纯规则函数，不是接口性能，更不代表安全校验没有开销。

另在最后期限/候选修复前已运行的隔离API上，对同一账号、同一上传票据状态热点进行真实HTTP并发请求：

| 并发 | 总请求 | 结果 | p95 |
| --- | --- | --- | --- |
| 20 | 100 | 全部200 | 105.958 ms |
| 50 | 200 | 全部200 | 107.770 ms |

请求包含真实PG/Redis和网络，不含图片上传、解码与存储传输。结束后正常列表仍可用，取消请求返回200。证据在私密 `live-security.json`。最后修复未修改该状态/纯规则路径；这里保留为独立抽样证据，没有冒充最终源码重新压测。这是本次状态接口负载的成功观察，不能推导全站50人同时上传300张大图的容量。

## 6 SeaweedFS逻辑删除与物理回收

14张约3 KiB专属PNG的S3写入、完整GET散列、精确删除及存活样例检查通过。单对象元数据探针使用3172字节PNG，确认 `inline_bytes=0`，chunk位于卷2 / collection `qingye-acceptance`；对象已清理。Master早先8字节拓扑观测有心跳滞后，不能据此误判小图全部inline。

首次立即压缩未观察到回收，检查正确失败。加入最多45秒、每3秒读取目标卷删除计数的受控等待后，真实重跑通过：

| 结果 | 值 |
| --- | --- |
| 实际压缩范围 | 仅卷2，`qingye-acceptance` |
| `.dat`逻辑大小减少 | 84,656字节 |
| 文件系统分配块减少 | 122,880字节 |
| 压缩耗时 | 0.416秒 |
| 观察到物理回收 | true |
| 原有对象清单保持一致 | true |
| 其他collection文件保持一致 | true |
| 剩余本次fixture键 | 空 |

最终两张存活样例清理后会留下少量新垃圾，没有再次自动压缩。脚本没有全局vacuum、删除桶、清空目录或删除元数据；详细准入及命令见存储运行说明。此为小样本回收验证，**不是512 MiB满卷或并发压缩容量实验**。

`qingye-test`服务用户已能对0700存储路径执行容量探测，未扩大目录权限。应用2 GiB保留量与512 MiB卷不构成专属保留分区；维护时仍检查额外工作空间。

发布前另使用应用实际SeaweedFS凭据及 `qingye-media` 范围、OSS最终 `r20/test/objects` 前缀，各写入一张3174字节随机PNG，读取完整内容核对校验值并精确删除，均通过。私密 `application-storage-preflight.json` 记录 `passed=true`、`verified_and_deleted_both=true`，未写业务数据库。此项验证实际应用配置可写且可清理，避免只验证acceptance权限；不等同正式入口已经部署。

## 7 中间版本记录

早期152隔离全量为267、272、315项；取消独立预览前，双variant真实300图往返为70.684秒。初版单原图曾通过全量/race各328项、专项35项×3，真实300图为38.3451674秒；第一处期限修复中间快照又通过333项全量。它们均为迭代证据，**最终以本报告同版338项、45项×3和42.04秒闭环为准**，不能用前一次通过替代最后缺陷复测，也不将不同代码/环境的耗时当严格前后性能对比。

## 8 实际发布回执与后续边界

三端统一发布 `20261113-media-r20`，回执时间 `2026-11-13T03:52:21+08:00`，代码提交 `db887ae0b7d2c7619ace79c366dc52c5e2e3d10d`。产物为第1节最终源码、Linux二进制、用户端和管理端包；部署不触碰8.140生产站或5178原型。

发布前备份目录 `/root/qingye-private/r20-20261113/test-backup-20261113-035217` 含 `database.dump`、私密环境、Nginx配置及previous links。私有回执为 `/root/qingye-private/r20-20261113/test-release-receipt.json`，并复制到 `/root/qingye-private/integration/20261113-media-r20-receipt.json`。部署后 `test-postdeploy.json` 为 `passed=true`。

| 部署后核验 | 结果 |
| --- | --- |
| 双API5180/5182健康及0012迁移 | 通过 |
| Nginx语法、reload及 `/api/` streaming | 通过，`proxy_request_buffering off`已生效 |
| API systemd停止等待 | 实际 `TimeoutStopSec=45s` |
| 两端首页HTML及引用JS/CSS | bytes与散列匹配最终构建 |
| 工作机公网HTTPS两端访问 | HTTP200、`ssl_verify_result=0`，未使用`-k`或关闭TLS校验 |
| 首页、categories、dictionaries、contacts、products | HTTP200 |
| 旧商品详情及图片 | HTTP200 |
| 公开入口访问管理路径 | 404 |
| 独立管理接口未登录 | 401 |
| 历史来源专属商品及图片 | 576条商品、48,332条关联及映射摘要前后一致 |

这里576/48,332是历史来源子集，**不是全库总数**。额外通过 `pg_restore --data-only --file=-` 只读提取部署前备份的原字段，与当前数据库COPY结果排序后比较SHA-256；没有将备份还原到业务库，也没有执行写入。`test-backup-comparison.json` 的 `all_unchanged=true`，8表原字段全部相同：

| 表 | 行数 | 比较的原字段数 |
| --- | --- | --- |
| products | 580 | 29 |
| product_images | 48,337 | 3 |
| media_files | 48,345 | 18 |
| reviews | 4 | 8 |
| categories | 4 | 7 |
| contacts | 7 | 9 |
| content_pages | 7 | 5 |
| favorites | 3 | 5 |

因此可以确认已有580条商品与48,337条图片关联保留；这项比较针对原字段，不将0012新增字段排除在迁移功能验证之外。

Nginx关闭请求缓冲让Go在流式接收前检查票据/容量，仍保留11m入口上限和Go 1 MiB分片校验。停止等待45秒覆盖HTTP排空、后台最终事务与聊天退出；没有为了发布关闭鉴权或容量保护。

已完成的浏览器7个核心流程和390×844视口不等于全站所有浏览器/尺寸验收；公网只读浏览器又检查8页：首页、集市、QYCH557旧详情、管理登录各1440/390两种宽度，均200、pageErrors=0、GET失败=0、无横向溢出；首页/集市下滑及旧详情主图、轮播原图等待decode后视觉确认。2次浏览计数POST由只读脚本主动拦截，不是产品错误；未登录、建账号或写业务库。私密 `public-browser-report.json` 为passed，本机12张截图在 `deploy-private/r20-public-smoke`。未完成File队列刷新后恢复仍是后续能力。CDN/边缘防刷、生产阿里云内网、真实大图/满卷容量未验收；本轮取消独立小图，不实施“小图优先”降级，短期OSS签名可重复下载，Go限频不能替代实际图片入口防刷。

产生R20数据后不能只切回R19二进制，回退须停写并核对0012及对象任务。0012正式发布后不可原地修改，后续修复使用新迁移。文档随本次提交更新；实际部署代码为 `db887ae0b7d2c7619ace79c366dc52c5e2e3d10d`，远端main与私有回执的 `github_commit` 核对，不要求文档自引用自身提交号。
