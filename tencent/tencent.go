// Package tencent 提供腾讯云短信（SMS v20210111）的发送实现。
//
// 仅依赖 Go 标准库实现 TC3-HMAC-SHA256 签名，不引入腾讯云官方 SDK。
// Sender 为具体类型，满足 github.com/jcbowen/sms-go 的 Sender 接口。
package tencent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jcbowen/sms-go"
	"github.com/jcbowen/sms-go/internal/httpx"
)

const (
	// Name 运营商标识。
	Name = "tencent"
	// DefaultEndpoint 腾讯云短信服务地址。
	DefaultEndpoint = "https://sms.tencentcloudapi.com/"
	// DefaultRegion 缺省地域。
	DefaultRegion = "ap-guangzhou"
	// DefaultHost 缺省请求主机名。
	DefaultHost = "sms.tencentcloudapi.com"
	// apiVersion 短信服务 API 版本。
	apiVersion = "2021-01-11"
	// apiAction 发送短信接口名。
	apiAction = "SendSms"
)

// Config 腾讯云短信配置。
type Config struct {
	// SecretID 云 API 密钥 ID，必填。
	SecretID string
	// SecretKey 云 API 密钥 Key，必填，禁止写入日志。
	SecretKey string
	// SdkAppID 短信应用 SDK AppID，必填。
	SdkAppID string
	// SignName 审核通过的默认短信签名，可被 Message.SignName 覆盖。
	SignName string
	// Region 地域，空则使用 DefaultRegion。
	Region string
	// Endpoint 接口地址，空则使用 DefaultEndpoint；可用于自建代理测试。
	Endpoint string
	// Client 自定义 HTTP 客户端，空则创建缺省客户端。
	Client *http.Client
}

// Sender 腾讯云短信发送器（具体类型，非接口）。
type Sender struct {
	cfg    Config
	host   string
	client *http.Client
}

// New 创建腾讯云发送器。
//
// 参数：
//   - cfg: 凭证与应用配置，SecretID / SecretKey / SdkAppID / SignName 必填
//
// 返回值：
//   - *Sender: 发送器实例
//   - error: 凭证缺失或 Endpoint 非法时返回错误
//
// 异常：
//   - 构造期不发起网络请求
//
// 使用示例：
//
//	sender, err := tencent.New(tencent.Config{
//	    SecretID: "AKID...", SecretKey: "...", SdkAppID: "1400000000",
//	    SignName: "示例签名",
//	})
func New(cfg Config) (*Sender, error) {
	if strings.TrimSpace(cfg.SecretID) == "" || strings.TrimSpace(cfg.SecretKey) == "" ||
		strings.TrimSpace(cfg.SdkAppID) == "" || strings.TrimSpace(cfg.SignName) == "" {
		return nil, fmt.Errorf("%w: tencent 需要 secret_id / secret_key / sdk_app_id / sign_name", sms.ErrNotConfigured)
	}
	if cfg.Region == "" {
		cfg.Region = DefaultRegion
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	parsed, err := url.Parse(cfg.Endpoint)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("%w: tencent endpoint 非法 %q", sms.ErrUnsupportedProvider, cfg.Endpoint)
	}
	client := cfg.Client
	if client == nil {
		client = httpx.NewClient(0)
	}
	return &Sender{cfg: cfg, host: parsed.Host, client: client}, nil
}

// Name 返回运营商标识。
//
// 参数：无
//
// 返回值：
//   - string: 固定为 "tencent"
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

// Send 发送一条腾讯云模板短信。
//
// 参数：
//   - ctx: 上下文，取消或超时会中断请求
//   - msg: 统一消息；Template 为 TemplateId，Params 按 ParamOrder 转为 TemplateParamSet
//
// 返回值：
//   - sms.Result: 受理结果，含 RequestId
//   - error: 本地构造失败或运营商返回错误，均为 *sms.Error
//
// 异常：
//   - 不自动重试：重发可能产生重复费用，业务层应让用户主动重发
//
// 使用示例：
//
//	res, err := sender.Send(ctx, sms.Message{
//	    Phone: "13800138000", Template: "123456",
//	    ParamOrder: []string{"code"}, Params: map[string]string{"code": "123456"},
//	})
func (s *Sender) Send(ctx context.Context, msg sms.Message) (sms.Result, error) {
	payload, err := s.buildPayload(msg)
	if err != nil {
		return sms.Result{}, sms.NewError(Name, "", err.Error(), false, err)
	}
	timestamp := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return sms.Result{}, sms.NewError(Name, "", "构造请求失败", false, err)
	}
	req.Host = s.host
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", buildAuthorization(s.cfg.SecretID, s.cfg.SecretKey, s.host, payload, timestamp))
	req.Header.Set("X-TC-Action", apiAction)
	req.Header.Set("X-TC-Version", apiVersion)
	req.Header.Set("X-TC-Timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("X-TC-Region", s.cfg.Region)

	body, err := httpx.Do(ctx, s.client, req)
	if err != nil {
		return sms.Result{}, sms.NewError(Name, "", "请求运营商失败", false, err)
	}
	return parseResponse(body)
}

// sendSmsPayload SendSms 接口的请求体结构，字段名遵循腾讯云 PascalCase 约定。
type sendSmsPayload struct {
	PhoneNumberSet   []string `json:"PhoneNumberSet"`
	SmsSdkAppId      string   `json:"SmsSdkAppId"`
	SignName         string   `json:"SignName"`
	TemplateId       string   `json:"TemplateId"`
	TemplateParamSet []string `json:"TemplateParamSet,omitempty"`
}

// buildPayload 组装 SendSms 请求体。
//
// 参数：
//   - msg: 统一消息
//
// 返回值：
//   - []byte: JSON 请求体
//   - error: JSON 序列化失败时返回错误
//
// 异常：无
//
// 使用示例：
//
//	payload, err := s.buildPayload(msg)
func (s *Sender) buildPayload(msg sms.Message) ([]byte, error) {
	signName := msg.SignName
	if strings.TrimSpace(signName) == "" {
		signName = s.cfg.SignName
	}
	phone, err := normalizePhone(msg.Phone)
	if err != nil {
		return nil, err
	}
	payload := sendSmsPayload{
		PhoneNumberSet:   []string{"+86" + phone},
		SmsSdkAppId:      s.cfg.SdkAppID,
		SignName:         signName,
		TemplateId:       msg.Template,
		TemplateParamSet: templateParamSet(msg),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("tencent: 请求体序列化失败: %w", err)
	}
	return encoded, nil
}

// apiResponse 腾讯云短信接口响应。
type apiResponse struct {
	Response struct {
		RequestId string `json:"RequestId"`
		Error     *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error"`
		SendStatusSet []struct {
			// SerialNo 发送流水号，用于 QuerySendStatus 查询发送状态。
			SerialNo string `json:"SerialNo"`
			Code     string `json:"Code"`
			Message  string `json:"Message"`
		} `json:"SendStatusSet"`
	} `json:"Response"`
}

// parseResponse 解析运营商响应并映射为统一结果与错误。
//
// 参数：
//   - body: HTTP 响应体
//
// 返回值：
//   - sms.Result: 受理成功时的结果，BizID 承载 SendStatusSet[].SerialNo
//     （发送流水号，可用于 QuerySendStatus 查询发送状态）
//   - error: Response.Error 非空、单条状态非 Ok 或 JSON 非法时返回 *sms.Error
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
	if errInfo := resp.Response.Error; errInfo != nil {
		return sms.Result{}, sms.NewError(Name, errInfo.Code, errInfo.Message, false, nil)
	}
	var bizID string
	if len(resp.Response.SendStatusSet) > 0 {
		bizID = resp.Response.SendStatusSet[0].SerialNo
	}
	for _, status := range resp.Response.SendStatusSet {
		if status.Code != "Ok" {
			return sms.Result{}, sms.NewError(Name, status.Code, status.Message, false, nil)
		}
	}
	return sms.Result{Provider: Name, RequestID: resp.Response.RequestId, BizID: bizID}, nil
}

// templateParamSet 把模板变量映射转为腾讯云要求的有序数组。
//
// 优先使用 Message.ParamOrder；为空时按键字典序，键名不参与传输。
//
// 参数：
//   - msg: 统一消息
//
// 返回值：
//   - []string: 有序模板参数值，无参数时返回空切片
//
// 异常：无
//
// 使用示例：
//
//	values := templateParamSet(msg)
func templateParamSet(msg sms.Message) []string {
	keys := msg.ParamOrder
	if len(keys) == 0 {
		for key := range msg.Params {
			keys = append(keys, key)
		}
		sort.Strings(keys)
	}
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, msg.Params[key])
	}
	return values
}

// normalizePhone 去掉国内号码可能携带的 +86 / 0086 / 86 前缀与分隔符。
//
// 官方支持 0086、86 或无前缀的 11 位国内号码；本库固定补 "+86" 前缀发送。
// SendSms 虽支持单次最多 200 个号码，但本库定位单条发送：
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
		return "", fmt.Errorf("tencent: 不支持逗号分隔的多号码，请逐条发送")
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	digits = strings.TrimPrefix(digits, "0086")
	return strings.TrimPrefix(digits, "86"), nil
}
