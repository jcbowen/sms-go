// Package httpx 提供运营商子包共用的 HTTP 请求辅助实现。
//
// 位于 internal 下，外部模块无法导入，可随实现自由重构而不破坏公开 API。
package httpx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// MaxResponseSize 响应体最大读取字节数，避免异常响应占用过多内存。
const MaxResponseSize = 1 << 20

// DefaultTimeout 运营商请求的缺省客户端超时。
const DefaultTimeout = 10 * time.Second

// NewClient 创建带超时的 HTTP 客户端。
//
// 参数：
//   - timeout: 客户端超时，<=0 时使用 DefaultTimeout
//
// 返回值：
//   - *http.Client: 可被多个 Sender 共享的客户端
//
// 异常：无
//
// 使用示例：
//
//	client := httpx.NewClient(5 * time.Second)
func NewClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &http.Client{Timeout: timeout}
}

// Do 执行请求并读取受限长度的响应体。
//
// ctx 会覆盖请求原有上下文；状态码非 2xx 时返回 HTTPError，错误描述中
// 携带最多 512 字节响应片段，便于排障且不会污染日志。
//
// 参数：
//   - ctx: 上下文，取消或超时会中断请求
//   - client: HTTP 客户端，nil 时创建缺省客户端
//   - req: 已构造好的请求，Body 由调用方提供
//
// 返回值：
//   - []byte: 响应体字节
//   - error: 网络失败、状态码异常或响应体超限时返回错误
//
// 异常：
//   - 不重试；短信重发可能产生重复费用，重试策略由业务层决定
//
// 使用示例：
//
//	body, err := httpx.Do(ctx, s.client, req)
func Do(ctx context.Context, client *http.Client, req *http.Request) ([]byte, error) {
	if client == nil {
		client = NewClient(0)
	}
	resp, err := client.Do(req.Clone(ctx))
	if err != nil {
		return nil, fmt.Errorf("httpx: 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("httpx: 读取响应失败: %w", err)
	}
	if len(body) > MaxResponseSize {
		return nil, fmt.Errorf("httpx: 响应体超过 %d 字节", MaxResponseSize)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: snippet(body)}
	}
	return body, nil
}

// HTTPError 运营商接口返回非 2xx 状态码时的错误。
type HTTPError struct {
	// StatusCode HTTP 状态码。
	StatusCode int
	// Body 截断后的响应片段，不含密钥。
	Body string
}

// Error 返回 HTTP 错误描述。
//
// 参数：无
//
// 返回值：
//   - string: 形如 "httpx: HTTP 500: {...}" 的描述
//
// 异常：无
//
// 使用示例：
//
//	log.Printf("%v", err)
func (e *HTTPError) Error() string {
	return fmt.Sprintf("httpx: HTTP %d: %s", e.StatusCode, e.Body)
}

// snippet 截取响应片段，避免把超大响应写进日志。
//
// 参数：
//   - body: 原始响应体
//
// 返回值：
//   - string: 最多 512 字节的片段
//
// 异常：无
//
// 使用示例：
//
//	s := snippet(body)
func snippet(body []byte) string {
	const limit = 512
	if len(body) > limit {
		body = body[:limit]
	}
	return string(body)
}
