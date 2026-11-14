# 生产首批数据包

`prepare_production_data.py` 只处理本轮确认的576件可见商品（45件出售、531件租赁），默认导入操作是只读预览。脚本不连接SSH、不复制OSS对象，也不修改数据库结构或`schema_migrations`。命令在对应服务器本机执行，通过PostgreSQL本机Unix socket访问命名库。

## 当前验证状态

本地20项单元测试通过。8.140独立PostgreSQL实例5433的专用`qingye_production_validation`已完成20个标准迁移、1个独立验证管理员及7组真实SQL验证：只读预览前后不变、脏目标拒绝、末尾强制错误后全部表和两条编号序列回滚、完整导入、同包重复不变、已修改业务行拒绝且不覆盖、最终数量/归属检查。共120.894秒，Python峰值RSS为471436KiB；运行限制768MiB地址空间，SQL按有限批次流式输入。

验证库结果为576件商品、48,349条必要媒体、48,332条商品图片关联、1个独立管理员，商品与媒体归属已替换，导入步骤保持条款/隐私未发布。下一出售/租赁编号分别为558/2836。完整私有回执在8.140的`/root/qingye-private/production-20261114/data-validation/sql-validation-receipt.json`。

发布负责人随后已在正式`qingye_production:5433`完成独立备份、v2包只读预览及真实事务导入，内部payload SHA与下文一致。实际正式库有576件商品、48,332条图片关联、48,349条媒体、10个头像和1个独立管理员；会员、聊天消息及迁入源账号均为0。17份本地支持素材已安装，生产本地历史商品原图数量为0。正式预览与apply的管理员映射摘要均为`e9a8870fe5fa3e27c95b43a034bd1451acc342847ba62c56fa612d9d8835fd91`；导入前备份SHA256为`036267c3603f31f598083295250e74c53d8037273028af50e0ceab129a55b6b4`，私有回执为`production-import-receipt.json`。

48,332份历史OSS映射已单独完成接管预览、登记及幂等预览；登记对应26,841,243,424字节，后续预览新增登记为0，存储变更为0。证据见同一私有目录的`production-replicas-registered-receipt.json`。正式协议在独立发布步骤中成为已发布v3，见`legal-published-receipt.json`；这不改变本工具始终将旧协议未发布导入的规则。**正式数据已导入；服务启动、实际图片读取和浏览器验收由发布流程另行记录，不能仅以导入完成宣称完整验收完成。**

正式服务启动后，已通过可信HTTPS对12张历史原图进行流式完整SHA256、字节数及MIME校验，覆盖出售/租赁各首、中、末3件商品的首尾图片，共6件商品、6,490,732字节，耗时14.505秒；所有响应为200且没有OSS重定向。每次读取后`r20-tmp`、相关API文件句柄及Nginx `proxy_temp`均已清空，生产本地仍只有原17份支持素材，没有保留legacy原图副本，6类媒体暂存/清理任务计数前后均为0。私有回执为同目录`media-sampling-receipt.json`。该抽样不等同48,332张图片全部重新下载，也不包含线上新上传生命周期写测；后者沿用隔离测试证据，不能记作正式环境真实写入验收。

原始失败已确认来自验证快照对序列使用`to_jsonb(s)`引发SQLSTATE 42809，发生在导入预览之前；快照现已改成显式序列字段和逐行摘要。后续发现整段SQL拼接内存较高，已改为流式规范JSON散列及私有临时SQL流。152此前确有系统OOM日志，但不能把整台服务器故障归因于某一个进程；152配置保持，整包apply只在8.140独立实例验证。

验证旧包为v1，格式名称及图片顺序字段现改为v2的`image_order_sha256`，不再称作复制证明。相同导入SQL规则保持；正式v2包已复用该业务快照，并在152按只读分批逐字段比较确认源数据仍一致，绑定接管HEAD核验使用的同字节顺序清单。源复核及支持素材打包耗时45.634秒，Python峰值RSS为472536KiB，没有数据库或OSS写入。验证文件名中的`NOT-FOR-PRODUCTION`/`NOT-OSS-COPY-PROOF`明确排除把测试回执当作OSS接管证据。

正式内部payload SHA256为`e72ba63e59c2ae0fd80869ef53b15f481ba93224ca7374f7630b62d41abfd789`；顺序清单SHA256为`ba777e849765b91f6fa9cfd117cc4b6a8434992aed69bdf06e8903664f013e50`。17份支持素材共2,051,687字节，仅含`images/`精确条目，无`legacy/`；tar SHA256为`8a828f0daf3153c98aa41463a03c7bedcd8749e1d569e5bd4ff3700977bc1735`。私有正式材料位于两机`/root/qingye-private/production-20261114/data-production/`；正式导入须使用payload SHA，而非JSON文件本身SHA。

## 数据范围

保留商品ID、历史ID、编号、报价及多租期条件、属性、原始载荷、版本、批准版本、发布时间、人工排序及推荐加分；保留图片ID和排序、媒体尺寸与SHA256。商品、媒体和头像库创建人的归属统一映射到目标独立创建的正式管理员，清除旧审核备注、上传票据和处理租约。

导入表白名单为`categories`、`product_dictionary_groups`、`product_dictionary_options`、`products`、`media_files`、`product_images`、`product_private_credentials`、`contacts`、`content_pages`、`site_settings`、`avatar_library`。媒体只包含所选商品、客服二维码和头像库的引用；停用头像库条目也保留。`site_settings`只保留`home_presentation`、`recommendation`、`publish_form`。

不迁移测试会员、管理员、密码、会话、聊天、浏览/收藏/咨询统计、推荐结果、短信挑战、昵称请求、原审核记录、上传限制、媒体搬迁/清理任务或旧OSS副本登记。正式管理员必须先在目标独立使用`seed-user`创建；脚本不接受或打印账号密码。

`terms`和`privacy`保留内容与版本，但强制未发布。其他运营页保留源内容，正式上线前需由经营方检查其中的测试环境说明。发布正式协议及开启短信是后续独立步骤。

本轮所选商品均为历史图片，不包含R20对象。发现R20键、生命周期对象、退役键或未核验的旧商品副本时整包拒绝，不能自动替换环境前缀。生产对现有`qingye-test/media/`对象完成只读HEAD/ACL核验后，使用独立的`media-replicas --production-takeover`工具登记生产所有权；不整套复制图片、不伪造复制记录。测试站必须先失去这些对象的云删除权限。头像和客服二维码原文件按包内媒体键、尺寸与SHA256复制到生产私有本地媒体目录。

## 导出与预览

脚本仅用Python标准库、`runuser`及`psql`。152已验证的解释器为`python3`；8.140须显式使用`/usr/bin/python3.11`，不能使用系统默认Python3.6。以root在152运行，先准备权限0700的仓库外私有目录：

```sh
python3 prepare_production_data.py export \
  --output /root/qingye-private/production-data/initial.json \
  --source-media-dir /var/lib/qingye-test/media \
  --image-order-manifest /root/qingye-private/production-data/product-image-order.jsonl

python3 prepare_production_data.py inspect \
  --package /root/qingye-private/production-data/initial.json
```

导出从`qingye_test`使用单个`REPEATABLE READ READ ONLY`事务，包文件权限0600且拒绝覆盖。终端仅输出表数量、字节数及散列，不输出商品行、账号、联系方式、密文或配置秘密。包内包含业务信息，必须按数据库备份等级私有保存，不能提交Git或上传公开工单。

旧R19头像/客服素材可能没有数据库SHA256；导出通过`--source-media-dir`核验每份引用的本地原文件、尺寸与读前读后文件状态，并把计算的SHA256写入目标数据包，不更新源库。商品图片必须保留已存在且与OSS核验登记一致的SHA256，不能借本地文件补造历史副本证据。

`--image-order-manifest`为接管核验使用的`product-image-order.jsonl`，每行包含`media_id`、`product_id`、`sort_order`。导出逐项核对全部商品图片关联与顺序，把原始字节SHA256写入包；导入再次校验同一清单。核验后图片重排、增删或换商品都会拒绝。发布负责人还须核对接管回执的`product_image_order_sha256`与包内`image_order_sha256`一致、`status=production_takeover_verified`、`storage_mutations=0`及完整源manifest绑定。本工具的顺序清单仅证明数据库关联，不独立证明云对象状态。

若复用已生成的正式包，先在152执行`verify-source --package ... --image-order-manifest ... --source-media-dir /var/lib/qingye-test/media`。该命令在单个只读事务按有限批次逐字段对比商品、媒体、关联及配置，重新检查可见范围、schema/20迁移、旧副本和退役状态；只返回结构性结果，避免再次导出52MB全量JSON。17份头像/客服素材另外从真实本地文件核验SHA256，不修改源库。

如果所选商品存在`product_private_credentials`，导出还须传`--source-environment-file`指向源私有环境文件；包内只绑定该32字节密钥的SHA256，不包含密钥。导入时校验目标`PRODUCT_CREDENTIAL_KEY`匹配，原密文保持不变。没有密文时不需要读取源环境文件。

## 目标条件与导入

默认只操作本机`qingye_production`。专项验证可明确传`--database qingye_production_validation`，必须另建专用数据库与角色并明确`--validation-instance test|production`；不接受其他名称。目标已按正常流程完成0001—0020迁移，并且只有一个独立创建、启用、bcrypt成本12的正式管理员。除迁移产生的初始字典及两条迁移审计外，不能存在其他业务数据。源与目标列结构和迁移版本必须完全相同。

数据库实例路由固定：152源库使用`/var/run/postgresql:5432`；验证库按显式`--validation-instance`选择实例；8.140生产库使用独立`/run/qingye-production-postgres:5433`及`/usr/pgsql-16/bin/psql`。目标环境文件的本机端口必须与该路由一致，不能因同名数据库误用系统默认5432实例。

目标私有环境文件必须包含`APP_ENV=production`、`MEDIA_WORKERS_ENABLED=false`，注册与登录短信均关闭。目标数据库不能有其他客户端连接，API应在数据准备完成后启动。该检查排除当前迁移客户端自身。现有媒体触发器需要约4.8万事务锁，工具要求锁容量至少为媒体条数加2048；不得为绕过该条件停用触发器或修改152配置。

```sh
python3.11 prepare_production_data.py import \
  --package /root/qingye-private/production-data/initial.json \
  --administrator-id 正式管理员UUID \
  --environment-file /etc/qingye-production/app.env \
  --image-order-manifest /root/qingye-private/production-data/product-image-order.jsonl \
  --expected-sha256 已独立核对的数据包散列
```

不带`--apply`只运行`SERIALIZABLE READ ONLY`预览，不插入、不删除、不改序列。确认同一数据包与目标后，在相同命令末尾加`--apply`执行导入。执行再次取得迁移互斥锁和业务表锁，在同一事务重新检查新库条件、替换精确匹配的迁移默认字典、导入并按主键逐字段核对全部记录及全表数量，最后写入一条最小导入审计。每批最多500行、约512KiB JSON，单行最多8MiB；各批仍属于同一事务，避免全量媒体JSON同时进入单个PL/pgSQL变量。编号序列使用事务性的`ALTER SEQUENCE ... RESTART`，按导入编号最大值递增，失败回滚不会留下`setval`副作用。

同一包和管理员映射重试时，只有已导入表仍与原包完全一致才返回验证成功，不重写数据。有差异或已有其他业务数据时拒绝。上线后产生实际业务变化的数据库不能再次当作新库导入。

数据包核验通过不代表现有OSS对象接管、图片实际读取、生产TLS或浏览器验收完成；这些由发布流程单独记录。不要仅因表数量一致就开启媒体任务，须先完成生产对象所有权、核验登记与存储路径检查。

## 验证

```sh
python3.11 -m unittest -v test_prepare_production_data.py
```

单元测试覆盖精度保持、白名单、可见性、引用、源身份和票据清理、停用头像保留、私密密钥绑定、SQL文本边界、只读预览、新库闸门、序列事务语义和错误脱敏。实际导出、目标预览、导入后数据核对与图片核验应分别记录，不能以单元测试代替真实数据库验证。
