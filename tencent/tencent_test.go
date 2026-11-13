package tencent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/jcbowen/sms-go"
)

// TestCanonicalRequest 用独立计算的摘要锁定 TC3 规范请求串拼接。
func TestCanonicalRequest(t *testing.T) {
	payload := []byte(`{"PhoneNumberSet":["+8613800138000"]}`)
	sum := sha256.Sum256(payload)
	want := "POST\n/\n\n" +
		"content-type:application/json; charset=utf-8\n" +
		"host:sms.tencentcloudapi.com\n\n" +
		"content-type;host\n" + hex.EncodeToString(sum[:])
	if got := canonicalRequest(payload, DefaultHost); got != want {
		t.Fatalf("规范请求串不符合预期\n实际: %q\n期望: %q", got, want)
	}
}

// TestBuildAuthorization 回归基线：固定时间戳与密钥下的 TC3 签名。
//
// 基线值由本实现首次运行产出并经固定输入锁定，用于防止派生链与编码漂移。
func TestBuildAuthorization(t *testing.T) {
	payload := []byte(`{"PhoneNumberSet":["+8613800138000"]}`)
	const want = "TC3-HMAC-SHA256 Credential=test-id/2019-02-25/sms/tc3_request, SignedHeaders=content-type;host, Signature=c37afb65dd561b8076ffea9d1ddb5cb9ebc7a1d10dd1f8c269c2da53670a6f62"
	got := buildAuthorization("test-id", "test-key", DefaultHost, payload, 1551113065)
	if got != want {
		t.Fatalf("TC3 授权头回归基线漂移:\n实际: %q\n期望: %q", got, want)
	}
}

// TestNewValidation 验证凭证缺失与非法 Endpoint。
func TestNewValidation(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, sms.ErrNotConfigured) {
		t.Fatalf("期望 ErrNotConfigured，实际 %v", err)
	}
	base := Config{SecretID: "id", SecretKey: "key", SdkAppID: "1400000000", SignName: "签名"}
	if _, err := New(func() Config { c := base; c.Endpoint = "://bad"; return c }()); !errors.Is(err, sms.ErrUnsupportedProvider) {
		t.Fatalf("非法 endpoint 应返回 ErrUnsupportedProvider，实际 %v", err)
	}
	sender, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	if sender.host != DefaultHost || sender.cfg.Region != DefaultRegion {
		t.Fatalf("缺省主机名/地域未回填: %+v", sender)
	}
}

// TestBuildPayload 验证请求体字段与有序模板参数。
func TestBuildPayload(t *testing.T) {
	sender, err := New(Config{SecretID: "id", SecretKey: "key", SdkAppID: "1400000000", SignName: "默认签名"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := sender.buildPayload(sms.Message{
		Phone: "+86 138-0013-8000", Template: "123456",
		ParamOrder: []string{"code", "minutes"},
		Params:     map[string]string{"minutes": "5", "code": "123456"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"PhoneNumberSet":["+8613800138000"],"SmsSdkAppId":"1400000000","SignName":"默认签名","TemplateId":"123456","TemplateParamSet":["123456","5"]}`
	if string(body) != want {
		t.Fatalf("请求体不符合预期\n实际: %s\n期望: %s", body, want)
	}
}

// TestTemplateParamSet 验证显式顺序优先于字典序。
func TestTemplateParamSet(t *testing.T) {
	msg := sms.Message{Params: map[string]string{"b": "2", "a": "1"}}
	if got := templateParamSet(msg); len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("字典序回退异常: %v", got)
	}
	msg.ParamOrder = []string{"b", "a"}
	if got := templateParamSet(msg); got[0] != "2" || got[1] != "1" {
		t.Fatalf("显式顺序未生效: %v", got)
	}
}

// TestSha256HexOfficialAnchors 官方《签名方法 v3》固定示例回归：两个与密钥无关的哈希锚点。
//
// 官方 demo 密钥打码无法复现最终签名，但文档给出了示例 payload 摘要与
// 规范请求串摘要的期望值，可锁定 sha256Hex 与规范化布局计算不漂移。
func TestSha256HexOfficialAnchors(t *testing.T) {
	const payload = `{"Limit": 1, "Filters": [{"Values": ["\u672a\u547d\u540d"], "Name": "instance-name"}]}`
	const wantPayloadHash = "35e9c5b0e3ae67532d3c9f17ead6c90222632e5b1ff7f6e89887f1398934f064"
	if got := sha256Hex([]byte(payload)); got != wantPayloadHash {
		t.Fatalf("payload 摘要与官方值不一致: got=%q want=%q", got, wantPayloadHash)
	}

	// 按官方文档示例规则构造的规范请求串（含可选参与签名的 x-tc-action 头）。
	canonical := "POST\n/\n\n" +
		"content-type:application/json; charset=utf-8\n" +
		"host:cvm.tencentcloudapi.com\n" +
		"x-tc-action:describeinstances\n" +
		"\n" +
		"content-type;host;x-tc-action\n" +
		wantPayloadHash
	const wantCanonicalHash = "7019a55be8395899b900fb5564e4200d984910f34794a27cb3fb7d10ff6a1e84"
	if got := sha256Hex([]byte(canonical)); got != wantCanonicalHash {
		t.Fatalf("规范请求串摘要与官方值不一致: got=%q want=%q\n规范请求串:\n%s", got, wantCanonicalHash, canonical)
	}
}

// TestNormalizePhone 验证国内号码前缀处理与多号码拒绝。
func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"13800138000":        "13800138000",
		"+86 138-0013-8000":  "13800138000",
		"8613800138000":      "13800138000",
		"0086 138-0013-8000": "13800138000",
	}
	for input, want := range cases {
		got, err := normalizePhone(input)
		if err != nil {
			t.Errorf("normalizePhone(%q) 意外出错: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("normalizePhone(%q)=%q，期望 %q", input, got, want)
		}
	}
	if _, err := normalizePhone("13800138000,13900139000"); err == nil {
		t.Fatal("逗号分隔多号码应报错，避免静默拼接错发")
	}
}

// TestParseResponse 验证成功与运营商错误映射，SerialNo 回填 BizID。
func TestParseResponse(t *testing.T) {
	res, err := parseResponse([]byte(`{"Response":{"RequestId":"req-1","SendStatusSet":[{"SerialNo":"serial-1","Code":"Ok","Message":"send success"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != Name || res.RequestID != "req-1" || res.BizID != "serial-1" {
		t.Fatalf("结果不符合预期: %+v", res)
	}
	_, err = parseResponse([]byte(`{"Response":{"RequestId":"req-2","Error":{"Code":"LimitExceeded","Message":"超频"}}}`))
	var apiErr *sms.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "LimitExceeded" {
		t.Fatalf("运营商错误未正确映射: %v", err)
	}
}
