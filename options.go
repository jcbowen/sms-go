package sms

import (
	"net/http"
	"time"
)

// DefaultTimeout 门面默认发送超时，防止外部请求无限期挂起。
const DefaultTimeout = 5 * time.Second

// Logger 最小日志接口，便于接入业务项目既有日志组件。
//
// 实现方只需满足 Printf 即可，标准库 *log.Logger 天然满足。
type Logger interface {
	Printf(format string, args ...any)
}

// options 门面内部配置，仅由 Option 修改。
type options struct {
	timeout    time.Duration
	httpClient *http.Client
	logger     Logger
}

// Option 门面构造选项，采用 Go 社区通行的函数式选项模式。
type Option func(*options)

// WithTimeout 设置单次发送的上下文超时，缺省 DefaultTimeout。
//
// 参数：
//   - d: 超时时长，<=0 表示不追加超时（由调用方 ctx 全权控制）
//
// 返回值：
//   - Option: 可传给 New 的选项
//
// 异常：无
//
// 使用示例：
//
//	client, _ := sms.New(sender, sms.WithTimeout(3*time.Second))
func WithTimeout(d time.Duration) Option {
	return func(o *options) { o.timeout = d }
}

// WithHTTPClient 注入共享 HTTP 客户端。
//
// 仅对实现 HTTPClientSetter 的 Sender 生效；未实现时该选项被忽略，
// Sender 使用自身默认客户端。方便统一代理、连接池与测试桩。
//
// 参数：
//   - client: HTTP 客户端，nil 表示不注入
//
// 返回值：
//   - Option: 可传给 New 的选项
//
// 异常：无
//
// 使用示例：
//
//	client, _ := sms.New(sender, sms.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}))
func WithHTTPClient(client *http.Client) Option {
	return func(o *options) { o.httpClient = client }
}

// WithLogger 注入日志器，用于脱敏记录发送结果。
//
// 参数：
//   - logger: 日志器，nil 表示不记录
//
// 返回值：
//   - Option: 可传给 New 的选项
//
// 异常：无
//
// 使用示例：
//
//	client, _ := sms.New(sender, sms.WithLogger(log.Default()))
func WithLogger(logger Logger) Option {
	return func(o *options) { o.logger = logger }
}

// HTTPClientSetter 可选能力接口，由需要共享 HTTP 客户端的 Sender 实现。
//
// 接口定义在使用方（本包），实现方返回具体类型，符合 Go 官方接口归属约定。
type HTTPClientSetter interface {
	SetHTTPClient(client *http.Client)
}

// defaultOptions 返回缺省配置。
//
// 参数：无
//
// 返回值：
//   - options: 含默认超时的配置值
//
// 异常：无
//
// 使用示例：
//
//	o := defaultOptions()
func defaultOptions() options {
	return options{timeout: DefaultTimeout}
}
