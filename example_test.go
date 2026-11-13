package sms_test

import (
	"context"
	"fmt"
	"time"

	"github.com/jcbowen/sms-go"
	"github.com/jcbowen/sms-go/aliyun"
	"github.com/jcbowen/sms-go/mock"
	"github.com/jcbowen/sms-go/tencent"
)

// Example 演示真实运营商的接入方式。
//
// 该示例不带 Output 注释，因此 go test 只编译不执行，不会发起网络请求
// （见 go.dev/blog/examples#output-comments）。
func Example() {
	sender, err := aliyun.New(aliyun.Config{
		AccessKeyID:     "从后台配置读取",
		AccessKeySecret: "从后台配置读取",
		SignName:        "审核通过的签名",
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	client, err := sms.New(sender, sms.WithTimeout(5*time.Second))
	if err != nil {
		fmt.Println(err)
		return
	}
	_, _ = client.Send(context.Background(), sms.Message{
		Phone:    "13800138000",
		Template: "SMS_000000001",
		Params:   map[string]string{"code": "123456"},
	})

	// 切换运营商只需替换 Sender 构造，业务调用不变。
	tencentSender, err := tencent.New(tencent.Config{
		SecretID:  "从后台配置读取",
		SecretKey: "从后台配置读取",
		SdkAppID:  "1400000000",
		SignName:  "审核通过的签名",
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	_, _ = sms.New(tencentSender)
}

// ExampleClient_Send 演示用 mock 发送器验证业务链路。
func ExampleClient_Send() {
	sender := mock.New()
	client, err := sms.New(sender)
	if err != nil {
		panic(err)
	}
	res, err := client.Send(context.Background(), sms.Message{
		Phone:      "13800138000",
		Template:   "SMS_000000001",
		ParamOrder: []string{"code"},
		Params:     map[string]string{"code": "123456"},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Provider, res.RequestID, sender.Messages()[0].Params["code"])
	// Output: mock mock-1 123456
}
