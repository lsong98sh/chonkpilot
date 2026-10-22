package llmtest

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/transport"
	"github.com/chonkpilot/chonkpilot-llm/pluginbridge"
)

// TestPluginBridgeEndToEnd 是 BRIDGE-2 的 L2 集成用例：
// 真实 llm-server + 插件桥 + 帧驱动插件（hello / filter / 外部工具 / 订阅）。
func TestPluginBridgeEndToEnd(t *testing.T) {
	bus := newBus(t)
	llm := startLLM(t, bus, textLLMServer("ok").URL, true)
	registerInstance(t, bus, "inst-pb")

	addr := filepath.Join(t.TempDir(), "plugin.sock")
	pb := pluginbridge.New(pluginbridge.Options{Bus: bus, Addr: addr, Version: "test"})
	if err := pb.Start(); err != nil {
		t.Fatalf("pluginbridge start: %v", err)
	}
	t.Cleanup(func() { _ = pb.Close() })
	llm.SetFilters(pb.Filters())
	llm.SetPluginRegistry(pb.Registry())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cli, err := transport.Dial(ctx, transport.Options{Backend: transport.BackendUDS, Addr: pb.Addr()})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	events := make(chan transport.Frame, 16)
	go func() {
		for {
			f, rerr := cli.Recv()
			if rerr != nil {
				close(events)
				return
			}
			if f.Kind != transport.KindReq {
				events <- f
				continue
			}
			switch f.Name {
			case "plugin.filter.run":
				_ = cli.Send(transport.Frame{Kind: transport.KindRes, ID: f.ID, OK: true, Result: map[string]any{"action": "drop"}})
			case "plugin.tool.call":
				_ = cli.Send(transport.Frame{Kind: transport.KindRes, ID: f.ID, OK: true, Result: map[string]any{
					"result": map[string]any{"from": "plugin"},
				}})
			default:
				_ = cli.Send(transport.Frame{Kind: transport.KindRes, ID: f.ID, Errors: []string{"unsupported"}})
			}
		}
	}()

	// hello + 订阅 + filter 注册
	if res, err := cli.Call(ctx, "plugin.hello", map[string]any{"id": "e2e.bot"}); err != nil || !res.OK {
		t.Fatalf("hello: %+v %v", res, err)
	}
	if res, err := cli.Call(ctx, "plugin.register", map[string]any{
		"subscriptions": []map[string]any{{"pattern": "session-complete", "mode": "async"}},
	}); err != nil || !res.OK {
		t.Fatalf("register: %+v %v", res, err)
	}

	// T7 原子能力：turn.context（先正常起一轮，再按 (instance, turn) 取上下文）
	if res, err := cli.Call(ctx, "plugin/v1/session.start", map[string]any{
		"instance_id": "inst-pb", "session": "s0", "turn": "t0", "request_id": "r0",
	}); err != nil || !res.OK {
		t.Fatalf("session.start(s0): %+v %v", res, err)
	}
	resCtx, err := cli.Call(ctx, "plugin/v1/turn.context", map[string]any{"instance_id": "inst-pb", "turn": "t0"})
	if err != nil || !resCtx.OK {
		t.Fatalf("turn.context: %+v %v", resCtx, err)
	}
	if m, _ := resCtx.Value.(map[string]any); m["found"] != true || m["session"] != "s0" {
		t.Fatalf("turn.context result=%+v", resCtx.Value)
	}

	// T7 原子能力：task.node.start / task.result / tool.stop / task.node.done
	resNode, err := cli.Call(ctx, "plugin/v1/task.node.start", map[string]any{
		"instance_id": "inst-pb", "turn": "t0", "tool": "tool_result",
		"tool_call_id": "call-1", "args": map[string]any{"id": "x"},
	})
	if err != nil || !resNode.OK {
		t.Fatalf("task.node.start: %+v %v", resNode, err)
	}
	nodeID, _ := resNode.Value.(map[string]any)["task_id"].(string)
	if nodeID == "" {
		t.Fatalf("task.node.start 未返回 task_id: %+v", resNode.Value)
	}
	resRunning, err := cli.Call(ctx, "plugin/v1/task.result", map[string]any{
		"instance_id": "inst-pb", "turn": "t0", "id": nodeID,
	})
	if err != nil || !resRunning.OK {
		t.Fatalf("task.result(running): %+v %v", resRunning, err)
	}
	if m, _ := resRunning.Value.(map[string]any); m["found"] != true || m["state"] != "running" {
		t.Fatalf("task.result(running) result=%+v", resRunning.Value)
	}
	resStop, err := cli.Call(ctx, "plugin/v1/tool.stop", map[string]any{
		"instance_id": "inst-pb", "turn": "t0", "args": map[string]any{"task_id": "no-such"},
	})
	if err != nil || !resStop.OK {
		t.Fatalf("tool.stop: %+v %v", resStop, err)
	}
	if m, _ := resStop.Value.(map[string]any); !strings.Contains(m["text"].(string), "不存在或已结束") {
		t.Fatalf("tool.stop text=%+v", resStop.Value)
	}
	if res, err := cli.Call(ctx, "plugin/v1/task.node.done", map[string]any{
		"task_id": nodeID, "state": "done", "result": "ok",
	}); err != nil || !res.OK {
		t.Fatalf("task.node.done: %+v %v", res, err)
	}
	resDone, err := cli.Call(ctx, "plugin/v1/task.result", map[string]any{
		"instance_id": "inst-pb", "turn": "t0", "id": nodeID,
	})
	if err != nil || !resDone.OK {
		t.Fatalf("task.result(done): %+v %v", resDone, err)
	}
	if m, _ := resDone.Value.(map[string]any); m["state"] != "done" || m["text"] != "ok" {
		t.Fatalf("task.result(done) result=%+v", resDone.Value)
	}

	if res, err := cli.Call(ctx, "plugin.filter.register", map[string]any{"stage": "llm.before_start", "id": "f1"}); err != nil || !res.OK {
		t.Fatalf("filter register: %+v %v", res, err)
	}

	// filter drop → llm-server 拒绝 session-start（fail-closed）
	res, err := cli.Call(ctx, "plugin/v1/session.start", map[string]any{
		"instance_id": "inst-pb", "session": "s1", "turn": "t1", "request_id": "r1",
	})
	if err != nil {
		t.Fatalf("session.start call: %v", err)
	}
	m, _ := res.Value.(map[string]any)
	if ok, _ := m["accepted"].(bool); ok {
		t.Fatalf("filter drop 后 session-start 不应受理: %+v", res.Value)
	}
	if s, _ := m["error"].(string); !strings.Contains(s, "filter_dropped") {
		t.Fatalf("error=%q", m["error"])
	}

	// 外部工具：bus 主题 → 插件
	if res, err := cli.Call(ctx, "plugin.tool.bind", map[string]any{"subject": "ext-tool-call"}); err != nil || !res.OK {
		t.Fatalf("tool.bind: %+v %v", res, err)
	}
	v := bus.Emit(ctx, "ext-tool-call", map[string]any{"name": "demo"}).Wait()
	if v.Err() != nil {
		t.Fatalf("ext tool: %v", v.Err())
	}
	if got, _ := v.Result.(map[string]any); got["from"] != "plugin" {
		t.Fatalf("ext tool result=%+v", v.Result)
	}

	// 订阅下行：session-complete → evt 帧
	bus.Emit(ctx, "session-complete", map[string]any{"session": "s1", "status": "complete"}).Wait()
	select {
	case f := <-events:
		if f.Name != "session-complete" {
			t.Fatalf("evt name=%q", f.Name)
		}
		var payload map[string]any
		_ = json.Unmarshal(f.Payload, &payload)
		if payload["status"] != "complete" {
			t.Fatalf("evt payload=%s", f.Payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("等待 session-complete 事件超时")
	}
}
