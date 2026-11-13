package sms

import "context"

// Message 一条模板短信的统一描述，与具体运营商解耦。
//
// Params 为模板变量名到变量值的映射（阿里云直接序列化为 JSON 对象）。
// 腾讯云的 TemplateParamSet 是有序数组，因此提供 ParamOrder 指定变量顺序；
// 留空时按 Params 的键字典序输出，建议腾讯云模板变量使用可排序的键名。
// SignName 为空时使用 Sender 构造时的默认签名。
type Message struct {
	// Phone 接收方手机号；国内号码不带 +86 前缀，由各 Sender 负责规范化。
	Phone string
	// Template 运营商模板标识：阿里云 TemplateCode / 腾讯云 TemplateId。
	Template string
	// Params 模板变量，键为变量名，值为变量内容。
	Params map[string]string
	// ParamOrder 模板变量输出顺序，仅腾讯云使用；为空时按键字典序。
	ParamOrder []string
	// SignName 覆盖默认签名，须与运营商审核通过的签名一致。
	SignName string
}

// Result 运营商受理后的统一结果。
//
// RequestID 用于与运营商对账排障；BizID 为发送流水号——阿里云为 BizId、
// 腾讯云为 SendStatusSet[].SerialNo——可用于查询发送状态。
// 受理成功不代表最终送达，送达状态需另行查询回执。
type Result struct {
	// Provider 实际处理本次发送的运营商标识。
	Provider string
	// RequestID 运营商返回的请求标识。
	RequestID string
	// BizID 运营商返回的业务流水号，无则返回空串。
	BizID string
}

// Sender 运营商发送能力接口，由使用方（本包）定义。
//
// 实现方应返回具体类型（如 *aliyun.Sender），无需在本包注册；
// Send 必须遵守 ctx 的取消与超时信号，且不得记录密钥或完整手机号。
type Sender interface {
	// Name 返回运营商标识，如 "aliyun"、"tencent"、"mock"。
	Name() string
	// Send 发送一条模板短信并返回受理结果。
	Send(ctx context.Context, msg Message) (Result, error)
}
