package tencent

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

const (
	// algorithm TC3 签名算法标识。
	algorithm = "TC3-HMAC-SHA256"
	// service 签名派生用服务名，腾讯云短信固定 sms。
	service = "sms"
	// requestType 签名派生末段，固定 tc3_request。
	requestType = "tc3_request"
	// signedHeaders 参与签名的请求头，顺序固定。
	signedHeaders = "content-type;host"
	// contentType 参与签名与请求的 Content-Type，必须与签名串逐字一致。
	contentType = "application/json; charset=utf-8"
)

// hmacSHA256 计算 HMAC-SHA256，返回原始字节供后续派生。
//
// 参数：
//   - key: 密钥字节
//   - data: 待签名数据
//
// 返回值：
//   - []byte: 32 字节摘要
//
// 异常：无
//
// 使用示例：
//
//	secretDate := hmacSHA256([]byte("TC3"+secretKey), date)
func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(data))
	return mac.Sum(nil)
}

// sha256Hex 计算 SHA-256 并返回小写十六进制串。
//
// 参数：
//   - data: 原始字节
//
// 返回值：
//   - string: 64 位十六进制摘要
//
// 异常：无
//
// 使用示例：
//
//	hashed := sha256Hex(payload)
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// canonicalRequest 构造 TC3 规范请求串。
//
// 参数：
//   - payload: 请求体原始字节
//   - host: 请求主机名，必须与 Authorization 及实际请求 Host 一致
//
// 返回值：
//   - string: 规范请求串
//
// 异常：无
//
// 使用示例：
//
//	canonical := canonicalRequest(payload, "sms.tencentcloudapi.com")
func canonicalRequest(payload []byte, host string) string {
	return "POST\n/\n\n" +
		"content-type:" + contentType + "\n" +
		"host:" + host + "\n\n" +
		signedHeaders + "\n" +
		sha256Hex(payload)
}

// buildAuthorization 生成腾讯云 TC3-HMAC-SHA256 Authorization 头。
//
// 派生链：TC3+SecretKey → date → service → tc3_request → stringToSign。
//
// 参数：
//   - secretID: 云 API 密钥 ID
//   - secretKey: 云 API 密钥 Key，禁止写入日志
//   - host: 请求主机名
//   - payload: 请求体原始字节
//   - timestamp: Unix 秒级时间戳
//
// 返回值：
//   - string: 可直接放入 Authorization 请求头的值
//
// 异常：无
//
// 使用示例：
//
//	req.Header.Set("Authorization", buildAuthorization(id, key, host, payload, ts))
func buildAuthorization(secretID, secretKey, host string, payload []byte, timestamp int64) string {
	date := time.Unix(timestamp, 0).UTC().Format("2006-01-02")
	scope := date + "/" + service + "/" + requestType
	stringToSign := algorithm + "\n" +
		strconv.FormatInt(timestamp, 10) + "\n" +
		scope + "\n" +
		sha256Hex([]byte(canonicalRequest(payload, host)))

	secretDate := hmacSHA256([]byte("TC3"+secretKey), date)
	secretService := hmacSHA256(secretDate, service)
	secretSigning := hmacSHA256(secretService, requestType)
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))

	return algorithm + " Credential=" + secretID + "/" + scope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + signature
}
