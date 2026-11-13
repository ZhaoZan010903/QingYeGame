# 首批卡通头像

R21-1 首批提供十张卡通漫画头像，供管理员导入头像库。其中四张通过内置 imagegen 生成，包含日漫与仙侠男女角色，打包为 512×512 JPEG；六张为 256×256 PNG 线绘头像。均非真人照片。

| 生成文件 | 库内名称 | 风格 |
| --- | --- | --- |
| anime-spring.jpg | 日漫·春晴 | 短发少女、浅绿背景 |
| anime-starlight.jpg | 日漫·星野 | 蓝发青年、浅蓝背景 |
| xianxia-jade.jpg | 仙侠·青璃 | 青白汉服女侠 |
| xianxia-cloud.jpg | 仙侠·云归 | 蓝白汉服男侠 |

生成方式与提示词见[生成说明](GENERATED--头像生成说明.md)。原图经等比缩放、JPEG编码压缩以适配头像上传额度，没有改变角色内容。

线绘图片来自 DiceBear 的 Lorelei 风格，原作者 Lisa Wischofsky，采用 [CC0 1.0](https://creativecommons.org/publicdomain/zero/1.0/)；[官方风格及许可说明](https://www.dicebear.com/styles/lorelei/)。获取日期：2026年11月13日。

| 文件 | 库内名称 | seed | backgroundColor |
| --- | --- | --- | --- |
| qingye-spring.png | 线绘·春芽 | QingYeSpring | ecf5ee |
| qingye-summer.png | 线绘·夏晴 | QingYeSummer | fff1db |
| qingye-autumn.png | 线绘·秋叶 | QingYeAutumn | fdece9 |
| qingye-winter.png | 线绘·冬雪 | QingYeWinter | e8efff |
| qingye-moon.png | 线绘·月光 | QingYeMoon | eee8fa |
| qingye-star.png | 线绘·星辰 | QingYeStar | e1f2f5 |

来源格式为 `https://api.dicebear.com/10.x/lorelei/png?seed=<seed>&size=256&backgroundColor=<backgroundColor>`。文件原样保存，没有添加真人资料。运行时不调用第三方头像 API，不传递会员信息。

使用 `代码/工具/seed_avatar_library.py` 经管理员上传和头像库接口初始化。图片沿用现有受控媒体转码、权限及访问保护，不作为公开静态资源绕过媒体接口。工具仅补充缺少的名称，不覆盖管理员维护的已有记录。
