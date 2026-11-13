# sms-go

多运营商短信发送 SDK，纯 Go 标准库实现，零第三方依赖。

- 模块路径：`github.com/jcbowen/sms-go`
- 首期支持：阿里云短信（Dysmsapi 2017-05-25，自签名采用官方 V3 规范 ACS3-HMAC-SHA256）、腾讯云短信（SMS v20210111）
- 辅助：`mock` 用于本地联调，不联网

## 设计边界

本模块只负责**通道能力**：统一消息类型、`Sender` 接口、运营商错误分类、上下文超时、日志脱敏。
验证码生成、Redis 频控、一次性校验、配置读取等**业务语义由使用方实现**，因此可被任意项目复用。

接口由使用方（根包 `sms`）定义，运营商子包返回具体类型 —— 符合
[Go Code Review Comments](https://go.dev/wiki/CodeReviewComments#interfaces) 的接口归属约定。
切换运营商只需替换 `Sender` 构造，业务调用代码不变。

## 安装

```bash
go get github.com/jcbowen/sms-go@latest
```

## 快速开始

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/jcbowen/sms-go"
    "github.com/jcbowen/sms-go/aliyun"
)

func main() {
    sender, err := aliyun.New(aliyun.Config{
        AccessKeyID:     "从后台配置读取",
        AccessKeySecret: "从后台配置读取",
        SignName:        "审核通过的签名",
    })
    if err != nil {
        log.Fatal(err)
    }
    client, err := sms.New(sender, sms.WithTimeout(5*time.Second), sms.WithLogger(log.Default()))
    if err != nil {
        log.Fatal(err)
    }
    res, err := client.Send(context.Background(), sms.Message{
        Phone:    "13800138000",
        Template: "SMS_000000001",
        Params:   map[string]string{"code": "123456"},
    })
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("request_id=%s", res.RequestID)
}
```

切换到腾讯云：

```go
sender, err := tencent.New(tencent.Config{
    SecretID: "…", SecretKey: "…", SdkAppID: "1400000000", SignName: "审核通过的签名",
})
client, err := sms.New(sender)
```

## 核心类型

| 类型 | 说明 |
|---|---|
| `sms.Sender` | 运营商能力接口：`Name()` + `Send(ctx, msg)` |
| `sms.Message` | `Phone` / `Template` / `Params` / `ParamOrder` / `SignName` |
| `sms.Result` | `Provider` / `RequestID` / `BizID`（阿里云 BizId / 腾讯云 SerialNo，用于查询发送状态） |
| `sms.Error` | `Provider` / `Code` / `Message` / `Retryable`，支持 `errors.As` |
| `sms.Option` | `WithTimeout` / `WithHTTPClient` / `WithLogger` |

模板参数差异：阿里云 `Params` 序列化为 `TemplateParam` JSON 对象；腾讯云的
`TemplateParamSet` 是有序数组，优先按 `ParamOrder` 输出，留空时按键字典序。
腾讯云模板建议把变量键名设计成可排序（如 `1`、`2` 或 `code`、`minutes`）。

号码格式：国内号码可携带 `+86` / `86` 前缀，由各 Sender 规范化；
阿里云 `Sender` 检测到逗号分隔多号码会显式报错（短信无幂等，避免静默错发），
群发需求请业务层逐条调用。

## 错误与重试

```go
if sms.IsNotConfigured(err) { /* 凭证未配置，fail-closed 拒绝发码 */ }
if sms.IsRetryable(err)     { /* 由业务层决定是否重试 */ }
```

SDK **不自动重试**：短信无幂等（超时不代表未送达），重发会产生真实费用与用户骚扰，
未分类错误一律为不可重试（`Retryable` 恒为 `false`）。
建议业务层在发送失败时释放频控占位、用 `Result.BizId` 查询运营商回执确认失败后
再让用户主动重发（market 项目即采用此口径）。

## 目录布局

遵循 [Organizing a Go module](https://go.dev/doc/modules/layout) 的「Multiple packages」：

```
doc.go / sms.go / sender.go / errors.go / options.go   根包 package sms
aliyun/ tencent/ mock/                                 各为独立 package
internal/httpx/                                        共享 HTTP 辅助，外部不可导入
example_test.go                                        可编译、可执行的官方示例
```

未设置 `pkg/`、`cmd/`、`util/`、`common/` 等非规范目录；根包无可执行程序，
故不建 `cmd/`。示例按 [Testable Examples](https://go.dev/blog/examples) 写在
`example_test.go`：带 `// Output:` 的示例由 `go test` 执行，不带的只编译（用于演示联网调用）。

## 扩展新运营商

1. 新建子包（如 `yuntongxun/`），返回具体 `*Sender` 类型；
2. 实现 `Name()` 与 `Send(ctx, sms.Message)`，可选实现 `SetHTTPClient`；
3. 用 `sms.NewError` 映射运营商错误码，凭证缺失返回 `sms.ErrNotConfigured`；
4. 补签名黄金用例与响应解析测试。

根包无需改动，也不需要注册表 —— 显式构造即可。

## 测试

```bash
go test ./...
go test -race ./...
go vet ./...
```

CI 覆盖 ubuntu / macos / windows 三平台（分支 / PR / `v*` tag 推送均触发）。
所有测试均不发起真实网络请求（`httptest` 与 `mock`），签名用例使用固定输入的回归基线。

## 发布

单入口发版脚本（对齐 license-cloud 模式；纯 Node ≥18 内置模块，三平台通用，依赖 git 与 go）：

```bash
node scripts/release.mjs --check            # 只跑门禁：gofmt → build → vet → test -race
node scripts/release.mjs 0.1.0              # 门禁通过后创建 v0.1.0 轻量 tag（默认不推送）
node scripts/release.mjs 0.1.0 --push       # 创建并推送 tag（推送即发布）
node scripts/release.mjs 0.1.0 --resume     # tag 已存在时复用（配合 --push 只补推）
node scripts/release.mjs --force 0.1.0      # 最新 tag 与 HEAD 无变更时强制发版
```

发版交互说明：

- **门禁链**：`gofmt -l .` → `go build ./...` → `go vet ./...` → `go test -race ./...`（`--skip-test` 可跳过测试）；
- **必要性探测**：最新 `v*` tag 与 HEAD 之间无变更时直接退出（`--force` 跳过）；工作区不干净拒绝发版；
- **候选版本**：不带版本号时按最新 tag 交互选择 patch/minor/major 递增（↑/↓ + Enter）；
- **默认只建本地 tag 不推送**，确认后用 `--push --resume` 补推——tag 推送即发布，
  CI 会在三平台复验，goproxy 按语义化 tag 自动缓存；
- git 提交与推送均由发版人自行执行，脚本只做门禁、探测与打 tag。

消费方按 tag 锁定版本：

```bash
go get github.com/jcbowen/sms-go@v0.1.0
```

## 安全约定

- 密钥仅存在于 `Config`，不写入日志、不出现在错误信息中；
- 日志中的手机号脱敏为前 3 后 4；
- 响应体读取上限 1 MiB，非 2xx 错误仅携带最多 512 字节片段；
- `mock` 仅供本地/测试栈，生产环境禁止启用。

## License

MIT
