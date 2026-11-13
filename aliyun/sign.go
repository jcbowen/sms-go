package aliyun

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
)

// signatureAlgorithm 阿里云 V3 签名算法标识，当前仅支持 ACS3-HMAC-SHA256。
const signatureAlgorithm = "ACS3-HMAC-SHA256"

// percentEncode 按 V3 签名规范做 RFC3986 百分号编码。
//
// 与 url.QueryEscape 的差异：空格编码为 %20 而非 +，星号编码为 %2A，
// 波浪线保留不转义。V2 与 V3 的编码规则一致，本函数同时用于
// CanonicalQueryString 的构造与实际请求 URL，保证两处逐字节相同。
//
// 参数：
//   - s: 待编码原文
//
// 返回值：
//   - string: 规范化编码结果
//
// 异常：无
//
// 使用示例：
//
//	percentEncode("a b~*") // "a%20b~%2A"
func percentEncode(s string) string {
	encoded := url.QueryEscape(s)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

// canonicalQuery 生成按键字典序排列的规范化查询串（V3 CanonicalQueryString）。
//
// 参数：
//   - params: 请求参数集合，不含凭证与签名信息
//
// 返回值：
//   - string: 形如 "PhoneNumbers=xxx&SignName=yyy" 的规范化查询串，
//     可直接作为请求 URL 的 query 部分
//
// 异常：无
//
// 使用示例：
//
//	query := canonicalQuery(params)
func canonicalQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, percentEncode(key)+"="+percentEncode(params[key]))
	}
	return strings.Join(parts, "&")
}

// hashHex 计算 SHA256 摘要并以小写十六进制（Base16）输出。
//
// 参数：
//   - data: 原始字节，可为 nil（等价于空串，用于空请求体）
//
// 返回值：
//   - string: 64 位小写十六进制摘要
//
// 异常：无（SHA256 固定输出，不会失败）
//
// 使用示例：
//
//	hashHex(nil) // "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// hmacSHA256Hex 以 AccessKeySecret 为密钥计算 HMAC-SHA256，小写十六进制输出。
//
// 参数：
//   - secret: AccessKey Secret，仅参与运算，不出现在返回值或日志中
//   - data: 待签名字节
//
// 返回值：
//   - string: 64 位小写十六进制签名
//
// 异常：无
//
// 使用示例：
//
//	hmacSHA256Hex(secret, []byte(stringToSign))
func hmacSHA256Hex(secret string, data []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// signedHeader 参与签名的单个请求头，Name 不区分大小写。
type signedHeader struct {
	// Name 请求头名称，如 "host"、"x-acs-action"。
	Name string
	// Value 请求头值，构造签名时会去除首尾空格。
	Value string
}

// buildCanonicalHeaders 构造 V3 规范化消息头与已签名消息头列表。
//
// 参与签名的头须为 x-acs- 前缀、host 或 content-type（由调用方保证）；
// 本函数统一将名称转小写并按字典序升序排列。
//
// 参数：
//   - headers: 参与签名的请求头集合
//
// 返回值：
//   - canonical: 规范化消息头，形如 "host:xxx\nx-acs-action:yyy\n..."（每项以 \n 结尾）
//   - signedList: 已签名头列表，形如 "host;x-acs-action;..."（分号分隔）
//
// 异常：无
//
// 使用示例：
//
//	canonical, signedList := buildCanonicalHeaders(headers)
func buildCanonicalHeaders(headers []signedHeader) (canonical, signedList string) {
	sorted := make([]signedHeader, len(headers))
	copy(sorted, headers)
	sort.Slice(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
	})
	names := make([]string, 0, len(sorted))
	entries := make([]string, 0, len(sorted))
	for _, h := range sorted {
		name := strings.ToLower(h.Name)
		names = append(names, name)
		entries = append(entries, name+":"+strings.TrimSpace(h.Value)+"\n")
	}
	return strings.Join(entries, ""), strings.Join(names, ";")
}

// buildAuthorization 构造 V3 签名的 Authorization 请求头。
//
// 签名流程（对应官方《V3版本请求体&签名机制》）：
//
//	StringToSign = signatureAlgorithm + "\n" + hashHex(canonicalRequest)
//	Signature    = hmacSHA256Hex(secret, stringToSign)
//
// 参数：
//   - accessKeyID: RAM 访问密钥 ID，进入 Credential 字段
//   - secret: AccessKey Secret，仅参与运算，不出现在返回值或日志中
//   - canonicalRequest: 规范化请求串（五段式，\n 分隔）
//   - signedHeaders: buildCanonicalHeaders 返回的已签名头列表
//
// 返回值：
//   - string: 形如 "ACS3-HMAC-SHA256 Credential=...,SignedHeaders=...,Signature=..." 的头值
//
// 异常：无
//
// 使用示例：
//
//	req.Header.Set("Authorization", buildAuthorization(akID, secret, canonicalRequest, signedList))
func buildAuthorization(accessKeyID, secret, canonicalRequest, signedHeaders string) string {
	stringToSign := signatureAlgorithm + "\n" + hashHex([]byte(canonicalRequest))
	signature := hmacSHA256Hex(secret, []byte(stringToSign))
	return signatureAlgorithm + " Credential=" + accessKeyID +
		",SignedHeaders=" + signedHeaders + ",Signature=" + signature
}
