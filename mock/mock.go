// Package mock 提供不联网的短信发送器，用于本地联调与单元测试。
//
// 它满足 github.com/jcbowen/sms-go 的 Sender 接口，记录每次发送的消息，
// 可注入错误以验证业务层的失败路径。禁止在生产环境启用。
package mock

import (
	"context"
	"strconv"
	"sync"

	"github.com/jcbowen/sms-go"
)

// Name 运营商标识。
const Name = "mock"

// Sender 内存短信发送器，并发安全。
type Sender struct {
	mu       sync.Mutex
	messages []sms.Message
	err      error
	seq      int
}

// New 创建内存发送器。
//
// 参数：无
//
// 返回值：
//   - *Sender: 初始状态无消息、无错误的发送器
//
// 异常：无
//
// 使用示例：
//
//	sender := mock.New()
func New() *Sender {
	return &Sender{}
}

// Name 返回运营商标识。
//
// 参数：无
//
// 返回值：
//   - string: 固定为 "mock"
//
// 异常：无
//
// 使用示例：
//
//	_ = sender.Name()
func (s *Sender) Name() string {
	return Name
}

// SetError 设置后续 Send 调用要返回的错误，传 nil 恢复成功。
//
// 参数：
//   - err: 要注入的错误，nil 表示清除
//
// 返回值：无
//
// 异常：无
//
// 使用示例：
//
//	sender.SetError(errors.New("通道故障"))
func (s *Sender) SetError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// Messages 返回已发送消息的顺序快照。
//
// 参数：无
//
// 返回值：
//   - []sms.Message: 消息副本，修改不影响内部状态
//
// 异常：无
//
// 使用示例：
//
//	if len(sender.Messages()) != 1 { t.Fatal("未记录发送") }
func (s *Sender) Messages() []sms.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sms.Message(nil), s.messages...)
}

// Reset 清空消息记录并清除注入的错误。
//
// 参数：无
//
// 返回值：无
//
// 异常：无
//
// 使用示例：
//
//	sender.Reset()
func (s *Sender) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = nil
	s.err = nil
	s.seq = 0
}

// Send 记录一条短信并返回模拟受理结果。
//
// 参数：
//   - ctx: 上下文，遵守取消信号
//   - msg: 待发送的统一消息
//
// 返回值：
//   - sms.Result: 含递增的模拟 RequestID
//   - error: SetError 注入的错误或 ctx 取消错误
//
// 异常：
//   - 不发起任何网络请求
//
// 使用示例：
//
//	res, err := sender.Send(context.Background(), sms.Message{Phone: "13800138000"})
func (s *Sender) Send(ctx context.Context, msg sms.Message) (sms.Result, error) {
	if err := ctx.Err(); err != nil {
		return sms.Result{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return sms.Result{}, s.err
	}
	s.messages = append(s.messages, msg)
	s.seq++
	return sms.Result{
		Provider:  Name,
		RequestID: "mock-" + strconv.Itoa(s.seq),
	}, nil
}
