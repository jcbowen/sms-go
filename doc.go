// Package sms 提供多运营商短信发送的统一门面。
//
// 本包只负责「通道能力」：定义 Sender 接口、统一消息与结果类型、错误分类、
// 上下文超时与日志脱敏。验证码生成、Redis 频控、一次性校验、配置读取等
// 业务语义留给使用方实现，因此本模块可以被任意业务项目复用。
//
// 接口归属遵循 Go 官方约定（go.dev/wiki/CodeReviewComments#interfaces）：
// Sender 由使用方（本包）定义，各运营商子包返回具体类型，不重复定义接口。
// 构造采用显式注入，新增运营商无需改动本包：
//
//	sender, err := aliyun.New(aliyun.Config{
//	    AccessKeyID:     "...",
//	    AccessKeySecret: "...",
//	    SignName:        "示例签名",
//	    TemplateCode:    "SMS_000000001",
//	})
//	if err != nil {
//	    return err
//	}
//	client, err := sms.New(sender, sms.WithTimeout(5*time.Second))
//	if err != nil {
//	    return err
//	}
//	result, err := client.Send(ctx, sms.Message{
//	    Phone:    "13800138000",
//	    Template: "SMS_000000001",
//	    Params:   map[string]string{"code": "123456"},
//	})
//
// 子包 aliyun / tencent / mock 各自实现 Sender；internal/httpx 为不可被外部
// 导入的共享 HTTP 辅助实现。
package sms
