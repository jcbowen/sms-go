package sms

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Client 短信发送门面，组合一个 Sender 与门面级选项。
//
// Client 并发安全：构造后不再修改内部状态，可被多个 goroutine 共享。
type Client struct {
	sender Sender
	opts   options
}

// New 创建短信门面。
//
// 参数：
//   - sender: 运营商发送器，不能为 nil；由调用方显式构造（如 aliyun.New）
//   - opts: 门面选项，见 WithTimeout / WithHTTPClient / WithLogger
//
// 返回值：
//   - *Client: 门面实例
//   - error: sender 为 nil 时返回 ErrInvalidSender
//
// 异常：
//   - 不发起任何网络请求；Sender 的凭证校验应由 Sender 自身在构造期完成
//
// 使用示例：
//
//	sender, err := tencent.New(tencent.Config{SecretID: "id", SecretKey: "key",
//	    SdkAppID: "1400000000", SignName: "示例签名", TemplateID: "123456"})
//	if err != nil {
//	    return err
//	}
//	client, err := sms.New(sender, sms.WithTimeout(5*time.Second))
func New(sender Sender, opts ...Option) (*Client, error) {
	if sender == nil {
		return nil, ErrInvalidSender
	}
	cfg := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.httpClient != nil {
		if setter, ok := sender.(HTTPClientSetter); ok {
			setter.SetHTTPClient(cfg.httpClient)
		}
	}
	return &Client{sender: sender, opts: cfg}, nil
}

// Provider 返回当前门面使用的运营商标识。
//
// 参数：无
//
// 返回值：
//   - string: Sender.Name() 的结果
//
// 异常：无
//
// 使用示例：
//
//	log.Printf("当前短信通道: %s", client.Provider())
func (c *Client) Provider() string {
	return c.sender.Name()
}

// Send 发送一条模板短信。
//
// 依次完成消息校验、超时包装、Sender 调用、错误分类与脱敏日志；
// 手机号在日志中仅保留前 3 位与后 4 位。
//
// 参数：
//   - ctx: 上下文，取消或超时会透传给 Sender
//   - msg: 模板短信内容，Phone 与 Template 必填
//
// 返回值：
//   - Result: 运营商受理结果
//   - error: ErrInvalidMessage / *Error（可用 IsRetryable 判定）
//
// 异常：
//   - 未分类错误一律按不可重试处理（fail-closed）：短信无幂等，超时不代表
//     未送达，避免重复发短信产生费用；建议失败后用 Result.BizId 查询回执再决定重发
//
// 使用示例：
//
//	res, err := client.Send(ctx, sms.Message{
//	    Phone: "13800138000", Template: "SMS_000000001",
//	    Params: map[string]string{"code": "123456"},
//	})
//	if err != nil {
//	    return err
//	}
//	log.Printf("request_id=%s", res.RequestID)
func (c *Client) Send(ctx context.Context, msg Message) (Result, error) {
	if err := validateMessage(msg); err != nil {
		return Result{}, err
	}
	if c.opts.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.opts.timeout)
		defer cancel()
	}
	res, err := c.sender.Send(ctx, msg)
	if err != nil {
		c.logf("sms: provider=%s phone=%s 发送失败: %v", c.sender.Name(), maskPhone(msg.Phone), err)
		return Result{}, wrapError(c.sender.Name(), err)
	}
	if res.Provider == "" {
		res.Provider = c.sender.Name()
	}
	c.logf("sms: provider=%s phone=%s 已受理 request_id=%s", res.Provider, maskPhone(msg.Phone), res.RequestID)
	return res, nil
}

// validateMessage 校验模板短信必填字段。
//
// 参数：
//   - msg: 待校验消息
//
// 返回值：
//   - error: 缺少 Phone 或 Template 时返回包装了 ErrInvalidMessage 的错误
//
// 异常：无
//
// 使用示例：
//
//	if err := validateMessage(msg); err != nil { return err }
func validateMessage(msg Message) error {
	if strings.TrimSpace(msg.Phone) == "" || strings.TrimSpace(msg.Template) == "" {
		return fmt.Errorf("%w: phone 与 template 必填", ErrInvalidMessage)
	}
	return nil
}

// logf 通过可选日志器输出脱敏日志。
//
// 参数：
//   - format: 格式化串
//   - args: 格式化参数
//
// 返回值：无
//
// 异常：无
//
// 使用示例：
//
//	c.logf("sms: provider=%s 已受理", provider)
func (c *Client) logf(format string, args ...any) {
	if c.opts.logger == nil {
		return
	}
	c.opts.logger.Printf(format, args...)
}

// maskPhone 手机号脱敏，仅保留前 3 位与后 4 位。
//
// 参数：
//   - phone: 原始手机号，可能含 +86 等前缀
//
// 返回值：
//   - string: 脱敏后的号码；长度不足 7 位时返回固定掩码
//
// 异常：无
//
// 使用示例：
//
//	maskPhone("13800138000") // "138****8000"
func maskPhone(phone string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	if len(digits) > 11 && strings.HasPrefix(digits, "86") {
		digits = digits[2:]
	}
	if len(digits) < 7 {
		return "****"
	}
	return digits[:3] + "****" + digits[len(digits)-4:]
}

// IsNotConfigured 判定错误是否为「凭证未配置」，供业务层 fail-closed 分流。
//
// 参数：
//   - err: 待判定错误
//
// 返回值：
//   - bool: 错误链中存在 ErrNotConfigured 时为 true
//
// 异常：无
//
// 使用示例：
//
//	if sms.IsNotConfigured(err) { return ErrSmsNotConfigured }
func IsNotConfigured(err error) bool {
	return errors.Is(err, ErrNotConfigured)
}
