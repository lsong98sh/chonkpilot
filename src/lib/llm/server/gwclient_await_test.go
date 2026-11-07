// gwClient 消费侧 awaiting 适配白盒（2026-11-06，统一异步执行专题稿 §10.4）：
// gateway 在 manual/never 超时点立即返回 structuredContent.status="awaiting" + task_id；
// gwclient.Call 必须与 pending 同样识别为「已转异步」，返回 taskID 并保留完整 result
// （上层可读 options/status），不得当成同步终态结果。
package server

import (
	"context"
	"encoding/json"
	"testing"

	mcpgateway "github.com/lsong98sh/chonkpilot-agent/gateway"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
)

func TestGWClientCallRecognizesAwaiting(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: testBusPrefix})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	sub, err := bus.On(msgkeys.TopicMcpToolsCall, 0, func(_ context.Context, _ string, v *mq.Value) error {
		v.Result = map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "任务等待用户裁决"}},
			"structuredContent": map[string]any{
				"status":    "awaiting",
				"task_id":   "ex-0001",
				"reason":    "timeout",
				"timeout_s": 0.1,
				"options":   []any{"wait", "cancel"},
			},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	g := newGWClient(bus)
	res, taskID, err := g.Call(context.Background(), "t", map[string]any{}, mcpgateway.Context{InstanceID: "ins-1"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if taskID != "ex-0001" {
		t.Fatalf("awaiting 应返回 taskID ex-0001, got %q（res=%v）", taskID, res)
	}
	sc, _ := res["structuredContent"].(map[string]any)
	if sc["status"] != "awaiting" {
		t.Fatalf("result 应保留 structuredContent.status=awaiting: %v", res)
	}
	if sc["reason"] != "timeout" {
		t.Fatalf("result 应保留 reason=timeout: %v", res)
	}
	opts, _ := sc["options"].([]any)
	if len(opts) != 2 || opts[0] != "wait" || opts[1] != "cancel" {
		t.Fatalf("result 应保留 options: %v", sc["options"])
	}
	_, _ = json.Marshal(res) // JSON 可序列化（消息面形态不变）
}
