# 生产历史图片：原路径接管与受控读取已验证

用户明确OSS容量仅40G，已撤回将整套历史图片从`qingye-test/media/`复制到`qingye-production/media/`的方案。**禁止重新执行整套复制，禁止恢复`copy_production_media.py --apply`。** 本文替代先前的复制、登记和启动操作说明；旧复制材料仅供审计，不能作为当前生产启用依据。

## 已完成的撤回与清理

`copy_production_media.py`在执行任何复制前拒绝`--apply`，报`whole_copy_disabled_by_40gb_capacity_constraint`。OSS同时保留针对`qingye-production/media/*`的精确PutObject Deny，真实写入已验证403。默认预览不代表复制仍获授权，不得通过其他命令、调用内部函数或删除保护绕过禁用。

核对来源、大小、SHA256元数据、CRC、媒体标识与ETag后，本轮新增目标副本已按精确清单清理：

| 项目 | 已核实结果 |
| --- | --- |
| 清理新增副本 | 38,573个 |
| 回收字节 | 21,424,318,469，约21.42GB |
| 剩余新增副本 / 错误 | 0 / 0 |
| 清理后复核 | 整个`qingye-production/`前缀为空；PutObject Deny仍存在 |
| 私有回执 | 8.140的`/root/qingye-private/production-20261114/oss-reclaim-receipt.json` |

源`qingye-test/media/`仍为48,340个对象、26,844,996,534字节，其中历史原图48,332个、26,841,243,424字节，`images/`图片8个、3,753,110字节。清理前后完整inventory SHA256均为：

`59bf568d3abe80d360b968ea08c7ad66ef08452fa22c6a78869a75e0e07e6c04`

清单一致不等于本轮重新下载全部图片核算SHA256。该前缀仅含图片，不是完整PostgreSQL业务备份；商品资料、图片关联与顺序等元数据需要独立备份和核对。

## 当前生产方向与启用条件

历史上架商品图片由生产接管现有`qingye-test/media/legacy/`，保留原对象路径，不复制、不重命名；测试只保留本地图片用于测试，并禁止其删除接管的legacy OSS对象。新上传使用独立生产对象路径`r20/production/objects/`，草稿及上传过程进入服务器私有暂存。`APP_ENV`与`MEDIA_R20_ENVIRONMENT`仍保持`production`，不得为读取历史图片把整个媒体环境改成`test`。

生产读取必须在现有授权和下载预算之后，依据经过核实的精确对象映射读取历史图，并保持内容校验、同源私有响应及有界临时文件。它不能扩大为任意前缀读取，不返回裸OSS地址；头像、聊天与客服素材仍遵守各自既有权限。

启用前必须完成并验证：

1. 历史商品图接管映射与新上传路径分离；接管配置不能改变R20生产写入和生命周期路径。
2. 测试应用删除器及云权限禁止legacy OSS物理删除，测试读取继续使用本地文件；生产完成接管后按本库有效引用及精确键清理独占OSS图片。现有shared guard仅为迁入过渡，不能作为生产永久不删图片的最终设计。
3. 接管证据、商品图片关联与顺序、数据库登记及原对象的完整核对。已撤回的生产复制回执或目标映射不能冒充接管证据；只读HEAD/ACL复核不得声称执行新复制或上传。
4. 真实SQL预览、导入幂等及回滚验证，以及实际私有读图、授权拒绝、下载预算与删除保护验收。

接管代码、真实HEAD/ACL、生产登记及SQL验证已完成；云端测试删除隔离已实测通过，正式历史图读取、页面浏览和管理员入口验证已通过。152双业务API已部署`MEDIA_LEGACY_OSS_DELETE_ENABLED=false`，本地读图保持；正式双API和双网站已于北京时间2026-11-15 00:07:35启动，`MEDIA_WORKERS_ENABLED=true`。最终配置和发布状态见[生产部署说明](../部署/qingye-production/README--生产环境部署说明.md)及最终回执，不按旧独立副本指南启动服务。

## 只读接管证据工具

新增[prepare_production_media.py](prepare_production_media.py)只调用官方SDK的`HeadObject`和`GetObjectAcl`，不执行Copy、Put或Delete，不修改数据库和云权限。使用北京OSS内网HTTPS地址，从8.140执行；配置文件只读取明确的OSS桶和凭据，不输出其值。

输入必须是原完整、已完成的`snapshot_copy_complete`证据包（48,345条、26,845,180,398字节），以及所选商品图片关系清单。原完整证据包含后来未纳入生产的图片，因此不等同当前源前缀inventory数量。工具只选择关系清单内全部48,332张legacy原图、26,841,243,424字节，保留原对象键；不对其他图片执行请求。商品关联与顺序仍须由数据库导入及登记步骤再次比对，不能用离线清单替代业务库核验。

在安装官方`alibabacloud-oss-v2`的独立私有环境内运行，例如：

```sh
python3.11 prepare_production_media.py \
  --source-bundle /root/qingye-private/original-verified-bundle \
  --product-order /root/qingye-private/production-data/product-image-order.jsonl \
  --output /root/qingye-private/production-takeover-evidence \
  --env-file /etc/qingye-production/app.env \
  --expected-count 48332 --expected-bytes 26841243424 \
  --workers 1
```

路径为示例，执行前核对实际私有输入。输出必须在Git仓库外，不能与输入重叠；目录0700、证据文件0600，禁止符号链接。默认单并发、最大4并发，在途future不超过并发数；遇错误停止追加请求，并保留已成功记录。恢复使用相同输入和输出目录，SQLite绑定完整源清单、关系清单及输入摘要；同一24小时会话内只复用已成功的HEAD/ACL记录，失效或输入变化会拒绝，不把旧ledger冒充新一轮检查。已完成包再次运行仅校验原包并报告原完成时间，不声称重新检查对象。

完成包包含原始`source-media-object-map-verified.jsonl`、原字节`product-image-order.jsonl`、新核实的`media-object-map-verified.jsonl`、`completion-receipt.json`及绑定四文件SHA256的`final-record-hashes.json`；最后写入与回执完成时间一致的`SUCCESS`。新回执状态为`production_takeover_verified`、`reference_environment=production`、`storage_mutations=0`。记录为`existing_verified`，包含实际HEAD时间和源ETag，不含复制响应或shared字段。复核只验证元数据与ACL，不声称重新下载全部图片计算内容SHA256；实际读图仍须校验完整内容。

该工具本地19项测试通过，Go登记契约已独立核对。发布负责人已在8.140完成48,332张、26,841,243,424字节真实原对象HEAD/ACL核验，`storage_mutations=0`，随后在正式库登记全部48,332条原路径映射。完成manifest SHA256为`6058465469cf95f52c41f20edc6f489a28f5c82bd8523035db8a0d8e995ffd2c`，绑定顺序清单SHA256为`ba777e849765b91f6fa9cfd117cc4b6a8434992aed69bdf06e8903664f013e50`。这不是重新下载并计算全部文件内容SHA；云权限已实测通过，12张真实原图HTTPS内容和清理及浏览器样本图已通过。

## 历史材料的用途

原复制工具、ledger、源清单、条件复制校验和中间回执仅用于解释此前操作及此次清理范围，不能恢复已撤回的授权。`media-replicas`的既有独立副本登记规则仍供其他已验证场景使用；生产接管须使用明确的`production_takeover_verified`只读证据契约并独立验收，不能把原对象伪装成新复制的生产副本。

凭据、完整对象键、媒体身份、业务快照和SDK原始错误继续留在服务器私有材料中，不进入Git、公开日志或交付正文。禁止按前缀或整桶删除，也不删除既有备份；生产接管验收后仅允许按业务引用保护和精确键清理。

云策略于北京时间2026-11-15 00:00:04实测通过：legacy Delete仅允许生产精确VPC；8.140内网不存在对象删除探针204，152公网相同探针403，两端Put403，bucket versioning disabled。没有删除真实图片、复制或新建对象；策略SHA256为`4879a396c3649129dcfae418ff16fb464d9f5158e7ec94afce2decf1bce530c7`。
