// Package mqadapter 是 gateway 端口（mcpgateway.Transport）的 mq 实现（宿主适配外壳）。
//
// 它把 gateway 内核的 Op（自有操作名，原 MQ 相对主题）与进程内 mq 总线对齐：
//   - Call = Emit + Wait（同主题请求-响应），结果 JSON 化后返回
//   - Subscribe = On（handler 返回的结果 JSON 反序列化写回 v.Result）
//
// **出站通知的主题名在本包决定**（2026-10-21）：gateway 内核只经 `mcpgateway.Hooks`
// 表达语义事件（任务完成回报 / 目录接入变化 / 工具超时待裁决），不再 import msgkeys；
// 本包通过 `Hooks()` 把语义事件映射到 61-消息一览的主题。
//
// gateway 内核（gateway 包）不 import mq / msgkeys；只有本适配包依赖它们（MOD-1 M0.5）。
package mqadapter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"

	mcpgateway "github.com/lsong98sh/chonkpilot-agent/gateway"
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

// Hooks 返回 gateway 出站通知的 mq 实现：语义事件 → 61-消息一览主题（fire-and-forget）。
//
// 装配方把它填进 Params.Hooks；未装配的项由 gateway 在 Start 时告警。
func (t *Transport) Hooks() mcpgateway.Hooks {
	return mcpgateway.Hooks{
		OnTaskReport:   func(payload map[string]any) { t.emit(msgkeys.TopicMcpTasksReport, payload) },
		OnMCPChanged:   func(payload map[string]any) { t.emit(msgkeys.TopicMcpGatewayChanged, payload) },
		OnToolsTimeout: func(payload map[string]any) { t.emit(msgkeys.TopicMcpToolsTimeout, payload) },
	}
}

// ProgressHandler 返回下游 `notifications/progress` 的 mq 实现（GATEWAY-2 L8，2026-10-28）：
// gateway 已按 `progressToken`（= `call_id`）把通知归位到具体调用，本函数把它转成下行事件
// `mcp-tools-progress`（61 §5.3；广播，桥按 `instance_id` 精确过滤）。
//
// `total` 缺省语义：未上报（0）→ **不带该键**（前端只知已处理量、不显示百分比）。
//
// 装配方把它填进 Params.ProgressHandler；未装配 → gateway 丢弃 progress + warn。
func (t *Transport) ProgressHandler() func(mcpgateway.ProgressEvent) {
	return func(ev mcpgateway.ProgressEvent) {
		payload := map[string]any{
			msgkeys.McpToolsProgressEventInstanceId: ev.InstanceID,
			msgkeys.McpToolsProgressEventToolCallId: ev.CallID,
			msgkeys.McpToolsProgressEventProgress:   ev.Progress,
		}
		if ev.Total > 0 {
			payload[msgkeys.McpToolsProgressEventTotal] = int64(ev.Total)
		}
		t.emit(msgkeys.TopicMcpToolsProgress, payload)
	}
}

// emit 序列化后单向发送（主题名由本包决定；序列化失败静默丢弃，保持 fire-and-forget）。
func (t *Transport) emit(topic string, payload map[string]any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	t.bus.Emit(context.Background(), topic, raw)
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
