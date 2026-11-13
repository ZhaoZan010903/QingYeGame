# R21 注册短信认证接入方案

日期：2026年11月13日。状态：已核对官方资料、现有代码与现有RAM凭据，尚未接通真实短信。用户已开通短信认证，随后表示已添加权限，并指定AccessKey与当前OSS所用一致；签名与模板待补充。

14:21（北京时间）从152服务器现有OSS私有配置取凭据进行非发送检查：STS返回RAMUser，证明该组凭据有效；CheckSmsVerifyCode使用无效号码和未发送的测试码，返回isv.ValidateFail（验证失败），未遇到权限拒绝。此结果只证明凭据身份与该次核验请求响应，不能当作验证码校验成功、完整RAM策略审计或发送权限验收。没有调用SendSmsVerifyCode，没有发送短信；私有证据保存在`/root/qingye-private/r21-sms-20261113/credential-probe.json`，凭据未输出或提交仓库。

## 本阶段范围

R21先处理注册短信获取与校验，形成手机号验证到注册的闭环。个人管理仅调整页面，不自行新增换绑、销户或其他账户业务；页面调整稍后开展。短信登录、找回密码分别安排，不因注册开通而一并启用。先前R20-2内容未定，本次按用户最新指示优先进入R21。

## 选用的阿里云产品

采用号码认证服务PNVS下的短信认证，服务端使用官方Go SDK，接口为SendSmsVerifyCode和CheckSmsVerifyCode，服务地址dypnsapi.aliyuncs.com。该服务托管验证码生成与核验；它与短信服务SMS的API、套餐包不同，额度不能混用。见[官方选型指南](https://help.aliyun.com/zh/sms/product-overview/selection-guide-for-short-message-service-and-sms-verification-service)。

首阶段使用控制台提供的赠送签名及登录/注册模板，签名与模板必须配套。官方当前列出的登录/注册模板编号为100001，接入时核对该账号控制台实际配置。短信显示所选平台签名，不能假定可自定义为“清叶”。快速测试需绑定测试手机号，测试短信也收费。见[短信认证配置说明](https://help.aliyun.com/zh/pnvs/user-guide/sms-authentication-service)。

## 当前项目差距

- AuthPage已有手机号、验证码、密码、协议确认和重发倒计时；只有全局VITE_SMS_ENABLED开关，需要改为按注册/登录/找回密码分别展示能力。
- 后端/auth/sms、/auth/register、/auth/password-reset当前固定返回SMS_NOT_CONFIGURED；短信登录也未实现。不能只修改前端开关便宣布接通。
- 数据库预留sms_challenges，但尚无调用逻辑，code_hash为非空的自建验证码设计。若改为云端核验，需新增有序迁移保存供应商流水、绑定和消费状态，不能用伪造验证码散列填补结构。
- 可复用Turnstile底层核验适配。现有HumanService及Redis完成动作只支持media_read，需要独立sms_send挑战与一次发送许可，不能复用图片通过状态。

代码依据：`代码/前端/用户端/src/pages/AuthPage.tsx`、`代码/后端/internal/transport/httpapi/auth.go`、`代码/后端/internal/application/identity/service.go`、`代码/后端/internal/maintenance/schema.sql`、`代码/后端/internal/application/security/human.go`。

## 拟定流程与初始参数

输入手机号 → 获取验证码 → Turnstile核验与本地限频 → Go调用阿里云发送 → 显示倒计时 → 用户填写短信验证码 → Go调用阿里云核验 → 消费注册凭据并创建会员。

验证码由阿里云动态生成，TemplateParam使用code占位符，明确ReturnVerifyCode=false；验证码不返回前端、不记录明文日志。短信提交成功与手机实际送达分开处理；核验必须检查Model.VerifyResult=PASS，不能把HTTP成功或Code=OK当成验证码正确。发送与核验使用一致的SchemeName，测试/正式环境及注册/登录用途分别隔离。依据：[发送接口](https://help.aliyun.com/zh/pnvs/developer-reference/api-dypnsapi-2017-05-25-sendsmsverifycode)、[核验接口](https://help.aliyun.com/zh/pnvs/developer-reference/api-dypnsapi-2017-05-25-checksmsverifycode)。

以下为初始实施建议，不是用户逐项指定或已上线配置：

| 项目 | 建议 |
| --- | --- |
| 手机号 | 中国大陆手机号，国家码86 |
| 验证码 | 6位纯数字，5分钟有效 |
| 重发 | 60秒间隔；成功发送新验证码后旧验证码失效 |
| 单号码发送 | 每小时5次、每天10次，服务端计数 |
| 错误核验 | 每次挑战最多5次，另有号码及IP累计限制 |
| 费用防护 | 独立IP、浏览器及全站发送预算；测试初期只开放明确指定的测试号码 |

每次发送先通过独立短信场景的人机核验与原子额度预留。手机号改变时清除旧挑战；并发点击、刷新与重试不能重复扣费发送。供应商超时属于结果不确定，不能自动重发或简单退还额度；OutId仅作为业务关联，不假定阿里云承诺发送幂等。供应商短信验证码核验与本地注册凭据消费分别管理，注册重试不得重复建号。保留手机号唯一约束、既有密码散列与public会话边界，不能借验证码注册管理员或恢复停用账号。

Redis保存有时效的频控、人机挑战与执行锁；PostgreSQL保存会员、必要的短信挑战元数据、消费状态和协议版本。不新增数据库种类或独立安全服务。供应商调用不占数据库长事务；脱敏记录RequestId、BizId、结果与耗时，不记录验证码、密钥、完整请求体。全球发送上限数值在真实联调前落实。

## 需要准备的控制台配置

1. 用户最新决定使用现有OSS所属RAM用户的AccessKey，并为其追加短信权限；本轮按此准备，不要求重新创建密钥。短信运行配置将单独声明，不由程序静默回退读取OSS配置。独立RAM用户与临时凭据仍是后续收敛权限的建议，不作为当前接入前置条件。
2. 在号码认证服务的短信认证参数配置中选择赠送签名与登录/注册模板，记录签名名称、模板编号。
3. 绑定用于验收的测试手机号；接口凭据存服务器私有配置或仓库外本机私有文件，再提供文件路径，凭据不进聊天和GitHub。

下列RAM策略仅允许发送与核验；这两个API的官方授权表均使用全部资源星号，不支持资源级细分。[发送权限说明](https://help.aliyun.com/zh/pnvs/developer-reference/api-dypnsapi-2017-05-25-sendsmsverifycode)、[核验权限说明](https://help.aliyun.com/zh/pnvs/developer-reference/api-dypnsapi-2017-05-25-checksmsverifycode)、[AccessKey创建说明](https://help.aliyun.com/zh/ram/user-guide/create-an-accesskey-pair)。

```json
{
  "Version": "1",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["dypns:SendSmsVerifyCode", "dypns:CheckSmsVerifyCode"],
      "Resource": "*"
    }
  ]
}
```

## 开发与验收顺序

先实现供应商适配、注册场景、挑战消费与防刷测试，再接前端获取/重发/校验反馈，最后在隔离环境使用指定号码验证真实收信及注册。覆盖错误/过期/重放验证码、改手机号、重复点击、断网重试、额度耗尽、注册并发和供应商异常；收到短信及核验通过才计真实链路验收完成。之后按项目流程部署测试环境并核对实际版本，不在业务库跑注册批量写测试。

目前完成调研、方案记录与不发送短信的凭据探测；没有创建RAM凭据、发送真实短信、修改业务代码或部署。独立检查脚本使用服务器私有目录内的官方SDK，不改变业务进程依赖。官方SDK入口：[Go SDK](https://github.com/alibabacloud-go/dypnsapi-20170525)。
