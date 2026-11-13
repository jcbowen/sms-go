package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jcbowen/sms-go"
)

// TestPercentEncode 锁定 RFC3986 编码与 url.QueryEscape 的三处差异。
func TestPercentEncode(t *testing.T) {
	cases := map[string]string{
		"a b":   "a%20b",
		"~":     "~",
		"*":     "%2A",
		"a/b+c": "a%2Fb%2Bc",
		"中文":    "%E4%B8%AD%E6%96%87",
		"{}":    "%7B%7D",
	}
	for input, want := range cases {
		if got := percentEncode(input); got != want {
			t.Errorf("percentEncode(%q)=%q，期望 %q", input, got, want)
		}
	}
}

// TestCanonicalQuery 锁定按键字典序排列的规范化查询串。
func TestCanonicalQuery(t *testing.T) {
	params := map[string]string{"SignName": "签名", "PhoneNumbers": "13800138000", "TemplateCode": "SMS_1"}
	want := "PhoneNumbers=13800138000&SignName=%E7%AD%BE%E5%90%8D&TemplateCode=SMS_1"
	if got := canonicalQuery(params); got != want {
		t.Fatalf("canonicalQuery=%q，期望 %q", got, want)
	}
}

// TestHashHex 锁定空请求体的 SHA256 摘要（官方文档示例值）。
func TestHashHex(t *testing.T) {
	const want = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := hashHex(nil); got != want {
		t.Fatalf("hashHex(nil)=%q，期望官方空体摘要 %q", got, want)
	}
}

// TestV3SignatureOfficialFixedCase 官方《V3版本请求体&签名机制》固定参数回归基线。
//
// 输入与期望输出全部取自官方文档示例，任何一步不一致都说明签名实现偏离规范。
func TestV3SignatureOfficialFixedCase(t *testing.T) {
	query := map[string]string{
		"ImageId":  "win2019_1809_x64_dtc_zh-cn_40G_alibase_20230811.vhd",
		"RegionId": "cn-shanghai",
	}
	headers := []signedHeader{
		{Name: "host", Value: "ecs.cn-shanghai.aliyuncs.com"},
		{Name: "x-acs-action", Value: "RunInstances"},
		{Name: "x-acs-content-sha256", Value: hashHex(nil)},
		{Name: "x-acs-date", Value: "2023-10-26T10:22:32Z"},
		{Name: "x-acs-signature-nonce", Value: "3156853299f313e23d1673dc12e1703d"},
		{Name: "x-acs-version", Value: "2014-05-26"},
	}

	const wantQuery = "ImageId=win2019_1809_x64_dtc_zh-cn_40G_alibase_20230811.vhd&RegionId=cn-shanghai"
	if got := canonicalQuery(query); got != wantQuery {
		t.Fatalf("CanonicalQueryString=%q，期望 %q", got, wantQuery)
	}

	canonicalHeaders, signedList := buildCanonicalHeaders(headers)
	const wantSignedList = "host;x-acs-action;x-acs-content-sha256;x-acs-date;x-acs-signature-nonce;x-acs-version"
	if signedList != wantSignedList {
		t.Fatalf("SignedHeaders=%q，期望 %q", signedList, wantSignedList)
	}

	canonicalRequest := strings.Join([]string{
		"POST",
		"/",
		wantQuery,
		canonicalHeaders,
		signedList,
		hashHex(nil),
	}, "\n")

	const wantHashedRequest = "7ea06492da5221eba5297e897ce16e55f964061054b7695beedaac1145b1e259"
	if got := hashHex([]byte(canonicalRequest)); got != wantHashedRequest {
		t.Fatalf("HashedCanonicalRequest=%q，期望官方值 %q\n规范化请求串:\n%s", got, wantHashedRequest, canonicalRequest)
	}

	const wantAuthorization = "ACS3-HMAC-SHA256 Credential=YourAccessKeyId," +
		"SignedHeaders=host;x-acs-action;x-acs-content-sha256;x-acs-date;x-acs-signature-nonce;x-acs-version," +
		"Signature=06563a9e1b43f5dfe96b81484da74bceab24a1d853912eee15083a6f0f3283c0"
	got := buildAuthorization("YourAccessKeyId", "YourAccessKeySecret", canonicalRequest, signedList)
	if got != wantAuthorization {
		t.Fatalf("Authorization=%q，期望官方值 %q", got, wantAuthorization)
	}
}

// TestBuildCanonicalHeaders 验证名称转小写、值去首尾空格与字典序排序。
func TestBuildCanonicalHeaders(t *testing.T) {
	canonical, signedList := buildCanonicalHeaders([]signedHeader{
		{Name: "X-Acs-Version", Value: " 2017-05-25 "},
		{Name: "Host", Value: "dysmsapi.aliyuncs.com"},
	})
	if canonical != "host:dysmsapi.aliyuncs.com\nx-acs-version:2017-05-25\n" {
		t.Fatalf("规范化消息头不符合预期: %q", canonical)
	}
	if signedList != "host;x-acs-version" {
		t.Fatalf("已签名头列表不符合预期: %q", signedList)
	}
}

// TestNewValidation 验证凭证缺失时返回 ErrNotConfigured。
func TestNewValidation(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, sms.ErrNotConfigured) {
		t.Fatalf("期望 ErrNotConfigured，实际 %v", err)
	}
	sender, err := New(Config{AccessKeyID: "id", AccessKeySecret: "secret", SignName: "签名"})
	if err != nil {
		t.Fatal(err)
	}
	if sender.cfg.Endpoint != DefaultEndpoint {
		t.Fatal("缺省接口地址未回填")
	}
	custom, err := New(Config{AccessKeyID: "id", AccessKeySecret: "secret", SignName: "签名", Endpoint: "https://proxy.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if custom.cfg.Endpoint != "https://proxy.example.com/" || custom.host() != "proxy.example.com" {
		t.Fatalf("Endpoint 归一化或主机名解析异常: %q / %q", custom.cfg.Endpoint, custom.host())
	}
}

// TestBuildParams 验证 V3 业务参数组装与手机号规范化。
func TestBuildParams(t *testing.T) {
	sender, err := New(Config{AccessKeyID: "id", AccessKeySecret: "secret", SignName: "默认签名"})
	if err != nil {
		t.Fatal(err)
	}
	params, err := sender.buildParams(sms.Message{
		Phone: "+86 138-0013-8000", Template: "SMS_1",
		Params: map[string]string{"code": "123456"}, SignName: "覆盖签名",
	}, "覆盖签名")
	if err != nil {
		t.Fatal(err)
	}
	if params["PhoneNumbers"] != "13800138000" || params["SignName"] != "覆盖签名" {
		t.Fatalf("参数不符合预期: %+v", params)
	}
	if params["TemplateParam"] != `{"code":"123456"}` {
		t.Fatalf("模板参数序列化异常: %s", params["TemplateParam"])
	}
	if len(params) != 4 {
		t.Fatalf("V3 业务参数应恰为 4 项，实际 %+v", params)
	}
}

// TestNormalizePhone 验证国内号码前缀处理与多号码拒绝。
func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{"13800138000": "13800138000", "+86 138-0013-8000": "13800138000", "8613800138000": "13800138000"}
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

// TestSend 通过 httptest 验证 V3 请求构造（query 传参、x-acs-* 头、Authorization）与响应解析。
func TestSend(t *testing.T) {
	var captured struct {
		rawQuery string
		header   http.Header
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.rawQuery = r.URL.RawQuery
		captured.header = r.Header.Clone()
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Code": "OK", "Message": "OK", "RequestId": "req-1", "BizId": "biz-1",
		})
	}))
	defer srv.Close()

	sender, err := New(Config{AccessKeyID: "test-id", AccessKeySecret: "test-secret", SignName: "默认签名", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	res, err := sender.send(context.Background(), sms.Message{
		Phone: "+86 138-0013-8000", Template: "SMS_1",
		Params: map[string]string{"code": "123456"},
	}, time.Date(2024, 12, 2, 6, 53, 9, 0, time.UTC), "d410180a5abf7fe235dd9b74aca91fc0")
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != Name || res.RequestID != "req-1" || res.BizID != "biz-1" {
		t.Fatalf("结果不符合预期: %+v", res)
	}

	// 业务参数全部经 query string 传递，body 为空。
	for _, want := range []string{"PhoneNumbers=13800138000", "SignName=", "TemplateCode=SMS_1", "TemplateParam=%7B%22code%22%3A%22123456%22%7D"} {
		if !strings.Contains(captured.rawQuery, want) {
			t.Errorf("query 缺少 %s: %s", want, captured.rawQuery)
		}
	}
	if captured.header.Get("X-Acs-Action") != "SendSms" || captured.header.Get("X-Acs-Version") != apiVersion {
		t.Fatalf("x-acs-action/version 不符合预期: %v", captured.header)
	}
	if captured.header.Get("X-Acs-Date") != "2024-12-02T06:53:09Z" {
		t.Fatalf("x-acs-date 不符合预期: %q", captured.header.Get("X-Acs-Date"))
	}
	if captured.header.Get("X-Acs-Signature-Nonce") != "d410180a5abf7fe235dd9b74aca91fc0" {
		t.Fatalf("x-acs-signature-nonce 不符合预期: %q", captured.header.Get("X-Acs-Signature-Nonce"))
	}
	auth := captured.header.Get("Authorization")
	if !strings.HasPrefix(auth, "ACS3-HMAC-SHA256 Credential=test-id,") ||
		!strings.Contains(auth, "SignedHeaders=host;x-acs-action;x-acs-content-sha256;x-acs-date;x-acs-signature-nonce;x-acs-version,") {
		t.Fatalf("Authorization 不符合预期: %q", auth)
	}
}

// TestParseResponse 验证成功与运营商错误映射。
func TestParseResponse(t *testing.T) {
	res, err := parseResponse([]byte(`{"Code":"OK","Message":"OK","RequestId":"req-1","BizId":"biz-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != Name || res.RequestID != "req-1" || res.BizID != "biz-1" {
		t.Fatalf("结果不符合预期: %+v", res)
	}
	_, err = parseResponse([]byte(`{"Code":"isv.BUSINESS_LIMIT_CONTROL","Message":"触发频控","RequestId":"req-2"}`))
	var apiErr *sms.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "isv.BUSINESS_LIMIT_CONTROL" {
		t.Fatalf("运营商错误未正确映射: %v", err)
	}
	if sms.IsRetryable(err) {
		t.Error("运营商频控不应自动重试")
	}
}
