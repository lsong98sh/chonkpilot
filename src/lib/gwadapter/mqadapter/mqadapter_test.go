package mqadapter

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"

	mcpgateway "github.com/lsong98sh/chonkpilot-agent/gateway"
)

// TestHooksMapToContractTopics 是「语义事件 → 61 主题」的**契约断言**。
//
// gateway 内核只表达 OnTaskReport / OnMCPChanged / OnToolsTimeout 三个语义事件，
// 不再持有任何主题名；主题名由本适配层决定。因此这条映射的正确性只能在此验证：
// 三个 hook 必须分别落到 mcp-tasks-report / mcp-gateway-changed / mcp-tools-timeout。
func TestHooksMapToContractTopics(t *testing.T) {
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer bus.Close()

	got := make(chan struct {
		topic   string
		payload map[string]any
	}, 3)
	subs := make([]mq.Sub, 0, 3)
	for _, topic := range []string{
		msgkeys.TopicMcpTasksReport,
		msgkeys.TopicMcpGatewayChanged,
		msgkeys.TopicMcpToolsTimeout,
	} {
		subject := topic
		sub, err := bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
			var m map[string]any
			_ = json.Unmarshal(v.Payload, &m)
			got <- struct {
				topic   string
				payload map[string]any
			}{subject, m}
			return nil
		})
		if err != nil {
			t.Fatalf("subscribe %s: %v", subject, err)
		}
		subs = append(subs, sub)
	}
	t.Cleanup(func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	})

	h := New(bus).Hooks()
	h.OnTaskReport(map[string]any{"task_id": "t1", "state": "done"})
	h.OnMCPChanged(map[string]any{"kind": "server", "name": "s1"})
	h.OnToolsTimeout(map[string]any{"task_id": "t2", "reason": "timeout"})

	want := map[string]string{
		msgkeys.TopicMcpTasksReport:    "t1",
		msgkeys.TopicMcpGatewayChanged: "s1",
		msgkeys.TopicMcpToolsTimeout:   "t2",
	}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		select {
		case ev := <-got:
			seen[ev.topic] = true
			// payload 必须原样保留（不得被适配层改写字段名）。
			switch ev.topic {
			case msgkeys.TopicMcpTasksReport:
				if ev.payload["task_id"] != want[ev.topic] {
					t.Fatalf("%s payload = %v", ev.topic, ev.payload)
				}
			case msgkeys.TopicMcpGatewayChanged:
				if ev.payload["name"] != want[ev.topic] {
					t.Fatalf("%s payload = %v", ev.topic, ev.payload)
				}
			case msgkeys.TopicMcpToolsTimeout:
				if ev.payload["task_id"] != want[ev.topic] {
					t.Fatalf("%s payload = %v", ev.topic, ev.payload)
				}
			}
		default:
			t.Fatalf("未在预期时间内收到第 %d 条通知（已收：%v）", i+1, seen)
		}
	}
	for topic := range want {
		if !seen[topic] {
			t.Fatalf("主题 %s 未收到通知", topic)
		}
	}
}

// TestProgressHandlerEmitsContractTopic 是 GATEWAY-2 L8（2026-10-28）「progress → 下行事件」的
// **契约断言**：gateway 只把已按 `progressToken`（= `call_id`）归位的 `ProgressEvent` 交给宿主，
// 主题名与载荷形状由本适配层决定 —— 只需在此锁定：
//   - 主题 = `mcp-tools-progress`（61 §5.3）；
//   - 载荷 = `{instance_id, tool_call_id, progress, total?}`，且 `tool`/`message` 等
//     **契约未声明字段不得透出**；
//   - `total` 缺省（未上报 / 0）→ **不带该键**（前端据此不显示百分比）。
func TestProgressHandlerEmitsContractTopic(t *testing.T) {
	bus, err := mq.New(mq.Options{})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer bus.Close()

	got := make(chan map[string]any, 4)
	sub, err := bus.On(msgkeys.TopicMcpToolsProgress, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		got <- m
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe %s: %v", msgkeys.TopicMcpToolsProgress, err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	h := New(bus).ProgressHandler()

	recv := func() map[string]any {
		t.Helper()
		select {
		case m := <-got:
			return m
		case <-time.After(2 * time.Second):
			t.Fatalf("%s 未在 2s 内收到", msgkeys.TopicMcpToolsProgress)
			return nil
		}
	}

	// ① 带 total：字段逐项对齐（progress/total 为数值）。
	h(mcpgateway.ProgressEvent{
		CallID: "call-abc", InstanceID: "ins-a", Tool: "file_copy",
		Progress: 20, Total: 100, Message: "20/100",
	})
	p := recv()
	if p[msgkeys.McpToolsProgressEventToolCallId] != "call-abc" {
		t.Fatalf("tool_call_id = %v，want call-abc", p[msgkeys.McpToolsProgressEventToolCallId])
	}
	if p[msgkeys.McpToolsProgressEventInstanceId] != "ins-a" {
		t.Fatalf("instance_id = %v，want ins-a", p[msgkeys.McpToolsProgressEventInstanceId])
	}
	if p[msgkeys.McpToolsProgressEventProgress] != float64(20) {
		t.Fatalf("progress = %v，want 20", p[msgkeys.McpToolsProgressEventProgress])
	}
	if p[msgkeys.McpToolsProgressEventTotal] != float64(100) {
		t.Fatalf("total = %v，want 100", p[msgkeys.McpToolsProgressEventTotal])
	}
	declared := map[string]bool{
		msgkeys.McpToolsProgressEventInstanceId: true,
		msgkeys.McpToolsProgressEventToolCallId: true,
		msgkeys.McpToolsProgressEventProgress:   true,
		msgkeys.McpToolsProgressEventTotal:      true,
	}
	for k := range p {
		if !declared[k] {
			t.Fatalf("载荷出现契约未声明的键 %q（payload=%v）", k, p)
		}
	}

	// ② 无 total：不带该键（前端只知已处理量、不显示百分比）。
	h(mcpgateway.ProgressEvent{CallID: "call-abc", InstanceID: "ins-a", Progress: 7})
	p = recv()
	if _, ok := p[msgkeys.McpToolsProgressEventTotal]; ok {
		t.Fatalf("total 缺省时不应写该键（payload=%v）", p)
	}
	if p[msgkeys.McpToolsProgressEventProgress] != float64(7) {
		t.Fatalf("progress = %v，want 7", p[msgkeys.McpToolsProgressEventProgress])
	}
}
