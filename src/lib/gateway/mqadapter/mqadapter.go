// Package mqadapter 是 gateway 端口（mcpgateway.Transport）的 mq 实现（宿主适配外壳）。
//
// 它把 gateway 内核的 Op（自有操作名，原 MQ 相对主题）与进程内 mq 总线对齐：
//   - Call = Emit + Wait（同主题请求-响应），结果 JSON 化后返回
//   - Publish = Emit 不 await（fire-and-forget）
//   - Subscribe = On（handler 返回的结果 JSON 反序列化写回 v.Result）
//
// gateway 内核（gateway 包）不 import mq / msgkeys；只有本适配包依赖 mq（MOD-1 M0.5）。
package mqadapter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chonkpilot/chonkpilot-lib/mq"

	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
)

// Transport 是基于进程内 mq 的 gateway 通道端口实现。
type Transport struct {
	bus mq.Bus
}

// New 构建 mq 适配器（bus 由宿主统一持有生命周期，本适配器不关闭它）。
func New(bus mq.Bus) *Transport { return &Transport{bus: bus} }

// Call 同步请求-响应：Emit 到 op 并 await 同一操作名，返回结果 JSON（nil = 无结果）。
func (t *Transport) Call(ctx context.Context, op mcpgateway.Op, payload []byte) ([]byte, error) {
	v := t.bus.Emit(ctx, string(op), payload).Wait()
	if err := v.Err(); err != nil {
		return nil, err
	}
	if v.Result == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v.Result)
	if err != nil {
		return nil, fmt.Errorf("mqadapter: marshal result for %q: %w", op, err)
	}
	return raw, nil
}

// Publish 单向通知（fire-and-forget）。
func (t *Transport) Publish(ctx context.Context, op mcpgateway.Op, payload []byte) error {
	t.bus.Emit(ctx, string(op), payload)
	return nil
}

// Subscribe 订阅 op：handler 返回的结果 JSON 反序列化写回 v.Result（无结果 → 不写回）。
func (t *Transport) Subscribe(op mcpgateway.Op, h func(ctx context.Context, payload []byte) ([]byte, error)) (mcpgateway.Unsubscribe, error) {
	sub, err := t.bus.On(string(op), 0, func(ctx context.Context, _ string, v *mq.Value) error {
		raw, herr := h(ctx, v.Payload)
		if herr != nil {
			return herr
		}
		if len(raw) == 0 {
			return nil
		}
		var res any
		if uerr := json.Unmarshal(raw, &res); uerr != nil {
			res = raw
		}
		v.Result = res
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sub.Unsubscribe, nil
}
