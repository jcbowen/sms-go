// Package aliyun 提供阿里云短信（Dysmsapi 2017-05-25）的发送实现。
//
// 按阿里云官方《V3版本请求体&签名机制》实现自签名调用：
// 业务参数经 RFC3986 编码后放入 query string，请求体为空，
// 使用 ACS3-HMAC-SHA256 算法生成 Authorization 请求头。
// 仅依赖 Go 标准库，不引入阿里云官方 SDK，避免依赖传染。
// Sender 为具体类型，满足 github.com/jcbowen/sms-go 的 Sender 接口。
package aliyun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jcbowen/sms-go"
	"github.com/jcbowen/sms-go/internal/httpx"
)

const (
	// Name 运营商标识。
	Name = "aliyun"
	// DefaultEndpoint 阿里云短信服务地址。
	DefaultEndpoint = "https://dysmsapi.aliyuncs.com/"
	// DefaultHost 缺省服务地址主机名，Endpoint 解析失败时兜底。
	DefaultHost = "dysmsapi.aliyuncs.com"
	// apiVersion 短信服务 API 版本，进入 x-acs-version 请求头。
	apiVersion = "2017-05-25"
	// apiAction 发送短信接口名，进入 x-acs-action 请求头。
	apiAction = "SendSms"
	// canonicalURI RPC 风格 API 的规范化 URI 固定为 "/"。
	canonicalURI = "/"
	// timeLayout V3 x-acs-date 的 ISO8601 UTC 时间格式。
	timeLayout = "2006-01-02T15:04:05Z"
)

// Config 阿里云短信配置。
type Config struct {
	// AccessKeyID RAM 访问密钥 ID，必填。
	AccessKeyID string
	// AccessKeySecret RAM 访问密钥 Secret，必填，禁止写入日志。
	AccessKeySecret string
	// SignName 审核通过的默认短信签名，可被 Message.SignName 覆盖。
	SignName string
	// Endpoint 接口地址，空则使用 DefaultEndpoint；可用于沙箱或自建代理测试。
	Endpoint string
	// Client 自定义 HTTP 客户端，空则创建缺省客户端。
	Client *http.Client
}

// Sender 阿里云短信发送器（具体类型，非接口）。
type Sender struct {
	cfg    Config
	client *http.Client
}

// New 创建阿里云发送器。
//
// 参数：
//   - cfg: 凭证与签名配置，AccessKeyID / AccessKeySecret / SignName 必填
//
// 返回值：
//   - *Sender: 发送器实例
//   - error: 凭证缺失时返回包装了 sms.ErrNotConfigured 的错误
//
// 异常：
//   - 构造期不发起网络请求
//
// 使用示例：
//
//	sender, err := aliyun.New(aliyun.Config{
//	    AccessKeyID: "LTAI...", AccessKeySecret: "...", SignName: "示例签名",
//	})
func New(cfg Config) (*Sender, error) {
	if strings.TrimSpace(cfg.AccessKeyID) == "" || strings.TrimSpace(cfg.AccessKeySecret) == "" ||
		strings.TrimSpace(cfg.SignName) == "" {
		return nil, fmt.Errorf("%w: aliyun 需要 access_key_id / access_key_secret / sign_name", sms.ErrNotConfigured)
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	// 归一化为带尾部斜杠的根路径，保证 CanonicalURI 与实际请求路径一致（均为 "/"）。
	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/") + "/"
	client := cfg.Client
	if client == nil {
		client = httpx.NewClient(0)
	}
	return &Sender{cfg: cfg, client: client}, nil
}

// Name 返回运营商标识。
//
// 参数：无
//
// 返回值：
//   - string: 固定为 "aliyun"
//
// 异常：无
//
// 使用示例：
//
//	_ = sender.Name()
func (s *Sender) Name() string {
	return Name
}

// SetHTTPClient 注入共享 HTTP 客户端，实现 sms.HTTPClientSetter 可选能力。
//
// 参数：
//   - client: HTTP 客户端，nil 时忽略
//
// 返回值：无
//
// 异常：无
//
// 使用示例：
//
//	_ = sms.New(sender, sms.WithHTTPClient(client))
func (s *Sender) SetHTTPClient(client *http.Client) {
	if client != nil {
		s.client = client
	}
}

// Send 发送一条阿里云模板短信。
//
// 参数：
//   - ctx: 上下文，取消或超时会中断请求
//   - msg: 统一消息；Template 为 TemplateCode，Params 序列化为 TemplateParam JSON
//
// 返回值：
//   - sms.Result: 受理结果，含 RequestId 与 BizId
//   - error: 本地构造失败或运营商返回错误，均为 *sms.Error
//
// 异常：
//   - 不自动重试：短信无幂等，超时不代表未送达，重发可能产生重复费用；
//     官方建议超时后用 BizId 调 QuerySendDetails 查回执再决定是否重发
//
// 使用示例：
//
//	res, err := sender.Send(ctx, sms.Message{
//	    Phone: "13800138000", Template: "SMS_000000001",
//	    Params: map[string]string{"code": "123456"},
//	})
func (s *Sender) Send(ctx context.Context, msg sms.Message) (sms.Result, error) {
	nonce, err := randomNonce()
	if err != nil {
		return sms.Result{}, sms.NewError(Name, "", err.Error(), false, err)
	}
	return s.send(ctx, msg, time.Now().UTC(), nonce)
}

// send 内部发送实现，时间与随机数可注入，供测试做固定参数签名回归。
//
// 参数：
//   - ctx: 上下文，取消或超时会中断请求
//   - msg: 统一消息
//   - now: 签名用的 UTC 时间，进入 x-acs-date
//   - nonce: 签名唯一随机数，进入 x-acs-signature-nonce
//
// 返回值：
//   - sms.Result: 受理结果
//   - error: 本地构造失败或运营商返回错误，均为 *sms.Error
//
// 异常：无
//
// 使用示例：
//
//	return s.send(ctx, msg, time.Now().UTC(), nonce)
func (s *Sender) send(ctx context.Context, msg sms.Message, now time.Time, nonce string) (sms.Result, error) {
	signName := msg.SignName
	if strings.TrimSpace(signName) == "" {
		signName = s.cfg.SignName
	}
	query, err := s.buildParams(msg, signName)
	if err != nil {
		return sms.Result{}, sms.NewError(Name, "", err.Error(), false, err)
	}

	// V3：业务参数全部放 query string，请求体为空，HashedRequestPayload 为空串摘要。
	contentSHA := hashHex(nil)
	headers := []signedHeader{
		{Name: "host", Value: s.host()},
		{Name: "x-acs-action", Value: apiAction},
		{Name: "x-acs-content-sha256", Value: contentSHA},
		{Name: "x-acs-date", Value: now.Format(timeLayout)},
		{Name: "x-acs-signature-nonce", Value: nonce},
		{Name: "x-acs-version", Value: apiVersion},
	}
	canonicalHeaders, signedHeaders := buildCanonicalHeaders(headers)
	canonicalRequest := strings.Join([]string{
		http.MethodPost,
		canonicalURI,
		canonicalQuery(query),
		canonicalHeaders,
		signedHeaders,
		contentSHA,
	}, "\n")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.Endpoint+"?"+canonicalQuery(query), nil)
	if err != nil {
		return sms.Result{}, sms.NewError(Name, "", "构造请求失败", false, err)
	}
	for _, h := range headers {
		req.Header.Set(h.Name, h.Value)
	}
	req.Header.Set("Authorization", buildAuthorization(s.cfg.AccessKeyID, s.cfg.AccessKeySecret, canonicalRequest, signedHeaders))

	body, err := httpx.Do(ctx, s.client, req)
	if err != nil {
		return sms.Result{}, sms.NewError(Name, "", "请求运营商失败", false, err)
	}
	return parseResponse(body)
}

// host 从 Endpoint 解析服务地址主机名，用于 host 请求头与签名。
//
// 参数：无
//
// 返回值：
//   - string: Endpoint 的主机名，解析失败时兜底为 DefaultHost
//
// 异常：无
//
// 使用示例：
//
//	headers[0].Value = s.host()
func (s *Sender) host() string {
	u, err := url.Parse(s.cfg.Endpoint)
	if err != nil || u.Host == "" {
		return DefaultHost
	}
	return u.Host
}

// buildParams 组装 SendSms 的业务请求参数（V3 下全部经 query string 传递）。
//
// 参数：
//   - msg: 统一消息
//   - signName: 生效的短信签名
//
// 返回值：
//   - map[string]string: PhoneNumbers / SignName / TemplateCode / TemplateParam
//   - error: 号码非法或模板参数 JSON 序列化失败时返回错误
//
// 异常：无
//
// 使用示例：
//
//	params, err := s.buildParams(msg, "示例签名")
func (s *Sender) buildParams(msg sms.Message, signName string) (map[string]string, error) {
	phone, err := normalizePhone(msg.Phone)
	if err != nil {
		return nil, err
	}
	templateParam := "{}"
	if len(msg.Params) > 0 {
		encoded, err := json.Marshal(msg.Params)
		if err != nil {
			return nil, fmt.Errorf("aliyun: 模板参数序列化失败: %w", err)
		}
		templateParam = string(encoded)
	}
	return map[string]string{
		"PhoneNumbers":  phone,
		"SignName":      signName,
		"TemplateCode":  msg.Template,
		"TemplateParam": templateParam,
	}, nil
}

// apiResponse 阿里云短信接口响应。
type apiResponse struct {
	Code      string `json:"Code"`
	Message   string `json:"Message"`
	RequestID string `json:"RequestId"`
	BizID     string `json:"BizId"`
}

// parseResponse 解析运营商响应并映射为统一结果与错误。
//
// 参数：
//   - body: HTTP 响应体
//
// 返回值：
//   - sms.Result: Code=OK 时的受理结果
//   - error: Code 非 OK 或 JSON 非法时返回 *sms.Error
//
// 异常：无
//
// 使用示例：
//
//	return parseResponse(body)
func parseResponse(body []byte) (sms.Result, error) {
	var resp apiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return sms.Result{}, sms.NewError(Name, "", "响应解析失败", false, err)
	}
	if resp.Code != "OK" {
		return sms.Result{}, sms.NewError(Name, resp.Code, resp.Message, false, nil)
	}
	return sms.Result{Provider: Name, RequestID: resp.RequestID, BizID: resp.BizID}, nil
}

// normalizePhone 去掉国内号码可能携带的 +86 / 86 前缀与分隔符。
//
// SendSms 虽支持逗号分隔多号码（上限 1000），但本库定位单条发送：
// 短信无幂等，多号码静默拼接会错发并产生费用，故检测到逗号即报错。
//
// 参数：
//   - phone: 原始手机号
//
// 返回值：
//   - string: 11 位国内号码格式
//   - error: 含逗号分隔多号码时返回错误
//
// 异常：无
//
// 使用示例：
//
//	phone, err := normalizePhone("+86 138-0013-8000") // "13800138000", nil
func normalizePhone(phone string) (string, error) {
	if strings.Contains(phone, ",") {
		return "", fmt.Errorf("aliyun: 不支持逗号分隔的多号码，请逐条发送")
	}
	trimmed := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	trimmed = strings.TrimPrefix(trimmed, "86")
	return trimmed, nil
}

// randomNonce 生成防重放随机数。
//
// 参数：无
//
// 返回值：
//   - string: 32 位十六进制随机串
//   - error: 系统随机源不可用时返回错误
//
// 异常：无
//
// 使用示例：
//
//	nonce, err := randomNonce()
func randomNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("aliyun: 生成随机数失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
