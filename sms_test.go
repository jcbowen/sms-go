package sms

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// fakeSender 测试用发送器，实现 Sender 接口。
type fakeSender struct {
	result Result
	err    error
	called int
	last   Message
}

// Name 返回测试标识。
func (f *fakeSender) Name() string { return "fake" }

// Send 记录消息并返回预设结果。
func (f *fakeSender) Send(_ context.Context, msg Message) (Result, error) {
	f.called++
	f.last = msg
	return f.result, f.err
}

// blockingSender 阻塞至 ctx 结束，用于验证超时透传。
type blockingSender struct {
	client *http.Client
}

// Name 返回测试标识。
func (b *blockingSender) Name() string { return "blocking" }

// Send 等待 ctx 完成后返回其错误。
func (b *blockingSender) Send(ctx context.Context, _ Message) (Result, error) {
	<-ctx.Done()
	return Result{}, ctx.Err()
}

// SetHTTPClient 记录注入的客户端。
func (b *blockingSender) SetHTTPClient(client *http.Client) { b.client = client }

// TestNewNilSender 验证 nil Sender 的构造期拒绝。
func TestNewNilSender(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrInvalidSender) {
		t.Fatalf("期望 ErrInvalidSender，实际 %v", err)
	}
}

// TestSendValidation 验证必填字段校验与不触发发送。
func TestSendValidation(t *testing.T) {
	sender := &fakeSender{}
	client, err := New(sender)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range []Message{
		{Template: "SMS_1"},
		{Phone: "13800138000"},
		{Phone: " ", Template: " "},
	} {
		if _, err := client.Send(context.Background(), msg); !errors.Is(err, ErrInvalidMessage) {
			t.Errorf("期望 ErrInvalidMessage，实际 %v", err)
		}
	}
	if sender.called != 0 {
		t.Fatal("校验失败时不应调用 Sender")
	}
}

// TestSendSuccess 验证成功路径补齐 Provider 且消息原样透传。
func TestSendSuccess(t *testing.T) {
	sender := &fakeSender{result: Result{RequestID: "req-1"}}
	client, err := New(sender)
	if err != nil {
		t.Fatal(err)
	}
	msg := Message{Phone: "13800138000", Template: "SMS_1", Params: map[string]string{"code": "123456"}}
	res, err := client.Send(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "fake" || res.RequestID != "req-1" {
		t.Fatalf("结果不符合预期: %+v", res)
	}
	if sender.last.Template != "SMS_1" || sender.last.Params["code"] != "123456" {
		t.Fatalf("消息未透传: %+v", sender.last)
	}
}

// TestSendErrorClassification 验证未分类错误按不可重试包装。
func TestSendErrorClassification(t *testing.T) {
	inner := errors.New("通道故障")
	client, err := New(&fakeSender{err: inner})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Send(context.Background(), Message{Phone: "13800138000", Template: "SMS_1"})
	if !errors.Is(err, inner) {
		t.Errorf("底层错误应可通过 errors.Is 提取，实际 %v", err)
	}
	if IsRetryable(err) {
		t.Error("未分类错误不应被标记为可重试")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Provider != "fake" {
		t.Errorf("应包装为带运营商标识的 *Error，实际 %v", err)
	}
}

// TestSendContextTimeout 验证门面超时透传给 Sender。
func TestSendContextTimeout(t *testing.T) {
	sender := &blockingSender{}
	client, err := New(sender, WithTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Send(context.Background(), Message{Phone: "13800138000", Template: "SMS_1"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("期望超时透传，实际 %v", err)
	}
}

// TestWithHTTPClientInjection 验证可选能力接口被正确注入。
func TestWithHTTPClientInjection(t *testing.T) {
	custom := &http.Client{}
	sender := &blockingSender{}
	if _, err := New(sender, WithHTTPClient(custom)); err != nil {
		t.Fatal(err)
	}
	if sender.client != custom {
		t.Fatal("实现 HTTPClientSetter 的 Sender 应收到共享客户端")
	}
}

// TestMaskPhone 验证手机号脱敏口径。
func TestMaskPhone(t *testing.T) {
	cases := map[string]string{
		"13800138000":       "138****8000",
		"+86 138-0013-8000": "138****8000",
		"123":               "****",
	}
	for input, want := range cases {
		if got := maskPhone(input); got != want {
			t.Errorf("maskPhone(%q)=%q，期望 %q", input, got, want)
		}
	}
}

// TestIsRetryable 验证可重试分类。
func TestIsRetryable(t *testing.T) {
	if !IsRetryable(NewError("fake", "", "网络超时", true, nil)) {
		t.Error("显式标注的错误应可重试")
	}
	if IsRetryable(nil) || IsRetryable(errors.New("普通错误")) {
		t.Error("nil 与普通错误不应可重试")
	}
}

// TestIsNotConfigured 验证凭证缺失错误判定。
func TestIsNotConfigured(t *testing.T) {
	if !IsNotConfigured(NewError("aliyun", "", "凭证缺失", false, ErrNotConfigured)) {
		t.Error("包装了 ErrNotConfigured 的错误应被识别")
	}
	if IsNotConfigured(ErrInvalidMessage) {
		t.Error("普通错误不应被误判")
	}
}
