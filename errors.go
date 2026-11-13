package sms

import "errors"

// 包级哨兵错误，供调用方用 errors.Is 判定。
var (
	// ErrInvalidSender 构造门面时传入了 nil Sender。
	ErrInvalidSender = errors.New("sms: sender 不能为空")
	// ErrInvalidMessage 消息缺少必填字段。
	ErrInvalidMessage = errors.New("sms: 短信消息不完整")
	// ErrNotConfigured 运营商凭证缺失，发送前即可判定，不应重试。
	ErrNotConfigured = errors.New("sms: 运营商凭证未配置")
	// ErrUnsupportedProvider 传入了不支持的运营商配置。
	ErrUnsupportedProvider = errors.New("sms: 不支持的运营商")
)

// Error 运营商级别的统一错误，携带业务码与是否可重试的分类。
//
// 短信无幂等且重复发送会产生真实费用与用户骚扰（官方建议超时后用 BizId
// 查询回执再决定是否重发），因此各 Sender 统一按不可重试处理（fail-closed）；
// Retryable 字段保留为扩展点，供业务层确认运营商侧失败后自行启用重试。
type Error struct {
	// Provider 出错的运营商标识。
	Provider string
	// Code 运营商返回的错误码，本地校验错误为空串。
	Code string
	// Message 运营商返回或本地生成的错误描述，不含密钥。
	Message string
	// Retryable 是否建议重试；当前统一为 false（短信无幂等，超时不代表
	// 未送达），保留字段供业务层确认运营商侧失败后扩展使用。
	Retryable bool
	// Err 被包装的底层错误，可为 nil。
	Err error
}

// NewError 构造一个运营商错误。
//
// 参数：
//   - provider: 运营商标识
//   - code: 运营商错误码，本地错误传空串
//   - message: 错误描述，不得包含密钥或完整手机号
//   - retryable: 是否建议重试
//   - err: 被包装的底层错误，可为 nil
//
// 返回值：
//   - *Error: 可被 errors.As 提取的错误实例
//
// 异常：无
//
// 使用示例：
//
//	return sms.Result{}, sms.NewError("aliyun", "isv.BUSINESS_LIMIT_CONTROL",
//	    "触发运营商频控", false, nil)
func NewError(provider, code, message string, retryable bool, err error) *Error {
	return &Error{Provider: provider, Code: code, Message: message, Retryable: retryable, Err: err}
}

// Error 返回格式化错误描述。
//
// 参数：无
//
// 返回值：
//   - string: 形如 "sms: aliyun 发送失败 code=... message=..." 的描述
//
// 异常：无
//
// 使用示例：
//
//	log.Printf("发送失败: %v", err)
func (e *Error) Error() string {
	text := "sms: " + e.Provider
	if e.Code != "" {
		text += " code=" + e.Code
	}
	if e.Message != "" {
		text += " " + e.Message
	}
	if e.Err != nil {
		text += ": " + e.Err.Error()
	}
	return text
}

// Unwrap 返回被包装的底层错误，支持 errors.Is / errors.As 链式判定。
//
// 参数：无
//
// 返回值：
//   - error: 底层错误，可能为 nil
//
// 异常：无
//
// 使用示例：
//
//	if errors.Is(err, context.DeadlineExceeded) { ... }
func (e *Error) Unwrap() error {
	return e.Err
}

// IsRetryable 判断错误是否建议重试。
//
// 参数：
//   - err: 待判定错误，可为 nil
//
// 返回值：
//   - bool: 仅当错误链中存在 Retryable=true 的 *Error 时为 true
//
// 异常：无
//
// 使用示例：
//
//	if sms.IsRetryable(err) { return retry(ctx, msg) }
func IsRetryable(err error) bool {
	var target *Error
	if errors.As(err, &target) {
		return target.Retryable
	}
	return false
}

// wrapError 把 Sender 返回的裸错误包装成 *Error，已分类的错误原样返回。
//
// 参数：
//   - provider: 运营商标识
//   - err: Sender 返回的原始错误
//
// 返回值：
//   - error: 统一包装后的错误
//
// 异常：无
//
// 使用示例：
//
//	return wrapError(c.sender.Name(), err)
func wrapError(provider string, err error) error {
	var classified *Error
	if errors.As(err, &classified) {
		return err
	}
	return NewError(provider, "", "发送失败", false, err)
}
