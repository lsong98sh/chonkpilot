// 端口定义（MOD-1 M0.5）：gateway 内核零 MQ 依赖。
//
// 内核只认 Op（自有操作名，原 MQ 相对主题）与 JSON 字节载荷；「Op → 实际主题」
// 的映射与订阅装配都归宿主 adapter（mq 实现见 mqadapter 包）。内核因此不再 import
// chonkpilot-lib/mq 与 msgkeys（L3 断言见 deps_test.go）。
package mcpgateway

import (
	"context"
	"encoding/json"
)

// Op 是 gateway 自有的操作名（原 MQ 相对主题），不引用 msgkeys。
type Op string

// Unsubscribe 退订句柄（幂等；重复调用无副作用）。
type Unsubscribe func() error

// Dispatcher 是 gateway 的**出站**端口（宿主实现）：
//   - Call = 同步请求-响应（语义同原 mq Emit + Wait：返回结果 JSON 字节，nil = 无结果）
//   - Publish = 单向通知（fire-and-forget）
type Dispatcher interface {
	Call(ctx context.Context, op Op, payload []byte) ([]byte, error)
	Publish(ctx context.Context, op Op, payload []byte) error
}

// Subscriber 是 gateway 的**入站**端口：宿主把消息通道上的方法调用接到 handler，
// handler 返回结果 JSON 字节（nil = 无结果）。
type Subscriber interface {
	Subscribe(op Op, h func(ctx context.Context, payload []byte) ([]byte, error)) (Unsubscribe, error)
}

// Transport = 出站 + 入站（宿主装配时注入 Params.Transport）。
type Transport interface {
	Dispatcher
	Subscriber
}

// call 是一次方法调用的内核替身（原 *mq.Value）：req = 原始载荷；订阅者写回 Result。
type call struct {
	req    []byte
	Result any
}

// JSON 把请求载荷反序列化到 out（无载荷 → 按 null 处理，与 mq.Value.JSON 同义）。
func (c *call) JSON(out any) error {
	if len(c.req) == 0 {
		return json.Unmarshal([]byte("null"), out)
	}
	return json.Unmarshal(c.req, out)
}
