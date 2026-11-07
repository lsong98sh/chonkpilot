// `plugin/v1/task.wait`（P2）：跨进程 tool_result 的终态事件等待——宿主侧订阅既有任务终态
// 主题，到点或终态即返；调用方不再轮询探测。
package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

func newTaskWaitFixture(t *testing.T, session, turn string) (*Server, *turnCtx, *pluginbridge.Registry) {
	t.Helper()
	llm := mockLLMServer()
	t.Cleanup(llm.Close)
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	store := s.newSessionStore("ins-test")
	if err := store.EnsureSession(session); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if err := store.EnsureTurn(turn, session); err != nil {
		t.Fatalf("EnsureTurn: %v", err)
	}
	tc := newTurnCtx(context.Background(), s, StartReq{
		InstanceID: "ins-test", Session: session, Turn: turn, WorkDir: testWorkDir,
	})
	t.Cleanup(tc.Close)
	s.mu.Lock()
	s.registerTurnLocked(instKey("ins-test", turn), tc)
	s.mu.Unlock()
	reg := pluginbridge.NewRegistry()
	s.SetPluginRegistry(reg)
	return s, tc, reg
}

func callTaskWait(t *testing.T, reg *pluginbridge.Registry, turn string, timeout float64, id string) (map[string]any, error) {
	t.Helper()
	h, ok := reg.Lookup("plugin/v1/task.wait")
	if !ok {
		t.Fatal("plugin/v1/task.wait 未注册")
	}
	raw, _ := json.Marshal(map[string]any{
		"instance_id": "ins-test", "turn": turn, "id": id, "timeout": timeout,
	})
	v, err := h(context.Background(), nil, raw)
	m, _ := v.(map[string]any)
	return m, err
}

func TestPluginTaskWaitReturnsOnTerminalEvent(t *testing.T) {
	s, tc, reg := newTaskWaitFixture(t, "s-wait", "t-wait")
	node, err := s.domainNode(tc, "tc-wait", "tool_result", map[string]any{"id": "x"}, "")
	if err != nil {
		t.Fatalf("domainNode: %v", err)
	}

	out := make(chan map[string]any, 1)
	go func() {
		m, _ := callTaskWait(t, reg, "t-wait", 5, node.TaskID)
		out <- m
	}()
	time.Sleep(100 * time.Millisecond) // 等 handler 订阅终态事件
	s.tasks.done(node.TaskID, TaskStateDone, "结果文本", "")

	select {
	case m := <-out:
		if m == nil || str(m["state"]) != "done" || str(m["text"]) != "结果文本" {
			t.Fatalf("终态事件后应返回真实结果: %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("task.wait 未在终态事件后返回（仍在轮询/阻塞）")
	}
}

func TestPluginTaskWaitTimesOutWhileRunning(t *testing.T) {
	s, tc, reg := newTaskWaitFixture(t, "s-wait2", "t-wait2")
	node, err := s.domainNode(tc, "tc-wait2", "tool_result", map[string]any{"id": "x"}, "")
	if err != nil {
		t.Fatalf("domainNode: %v", err)
	}
	raw, _ := json.Marshal(map[string]any{
		"instance_id": "ins-test", "turn": "t-wait2", "id": node.TaskID, "timeout": 0.15,
	})
	h, _ := reg.Lookup("plugin/v1/task.wait")
	start := time.Now()
	v, err := h(context.Background(), nil, raw)
	m, _ := v.(map[string]any)
	if err != nil {
		t.Fatalf("task.wait: %v", err)
	}
	if str(m["state"]) != "running" {
		t.Fatalf("超时时应返回 running: %+v", m)
	}
	if e := time.Since(start); e < 100*time.Millisecond || e > 2*time.Second {
		t.Fatalf("等待时长异常: %v", e)
	}
}
