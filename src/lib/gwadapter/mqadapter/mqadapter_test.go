package mqadapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
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
