// 实例隔离白盒测试（2026-09-19 第一批：缺口 3 / 缺口 5）：
// 同进程多 instance 的任务池与轮次运行态**互不可见、互不取消**；instance 为空时保持旧语义
// （全桶回退）；单 instance 行为与此前逐字节等价（既有测试全绿）。
package server

import (
	"context"
	"testing"
	"time"
)

// TestTaskPoolIsolationAcrossInstances：两个 instance 使用**同名** turn / tool_call_id / gw_task_id
// → 归属过滤的查询与级联取消均只命中本 instance（缺口 3）。
func TestTaskPoolIsolationAcrossInstances(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	mk := func(inst string) *TaskNode {
		t.Helper()
		n, err := s.tasks.start(&TaskNode{
			InstanceID: inst, TopSession: "s-shared", SessionID: "s-shared", TurnID: "t-shared",
			ToolCallID: "tc-shared", Tool: "core_file_read", Name: "隔离用例",
		})
		if err != nil {
			t.Fatalf("start(%s): %v", inst, err)
		}
		return n
	}
	a, b := mk("ins-a"), mk("ins-b")
	if a.TaskID == b.TaskID {
		t.Fatal("两个 instance 的任务应各自建节点")
	}

	// ① 互不可见：按 tool_call_id / gw_task_id 反查只命中本 instance；instance 空 → 全桶（旧语义）
	if n := s.tasks.findByToolCall("ins-a", "tc-shared"); n == nil || n.TaskID != a.TaskID {
		t.Fatalf("ins-a 反查应命中 A：%+v", n)
	}
	if n := s.tasks.findByToolCall("ins-b", "tc-shared"); n == nil || n.TaskID != b.TaskID {
		t.Fatalf("ins-b 反查应命中 B：%+v", n)
	}
	if n := s.tasks.findByToolCall("", "tc-shared"); n == nil {
		t.Fatal("instance 为空应回退全桶（旧语义）")
	}
	if n := s.tasks.nodeIn("ins-a", b.TaskID); n != nil {
		t.Fatalf("ins-a 不应看到 ins-b 的节点：%+v", n)
	}
	s.tasks.setGwTask(a.TaskID, "gw-shared")
	s.tasks.setGwTask(b.TaskID, "gw-shared")
	if n := s.tasks.findByGwTask("ins-a", "gw-shared"); n == nil || n.TaskID != a.TaskID {
		t.Fatalf("ins-a gw 反查应命中 A：%+v", n)
	}
	if n := s.tasks.findByGwTask("ins-b", "gw-shared"); n == nil || n.TaskID != b.TaskID {
		t.Fatalf("ins-b gw 反查应命中 B：%+v", n)
	}

	// ② 互不取消：取消 ins-a 的 t-shared → 只 A 终态，B 仍在运行
	if got := s.tasks.cancelByTurn("ins-a", "t-shared"); got != 1 {
		t.Fatalf("cancelByTurn(ins-a) = %d, want 1", got)
	}
	if n := s.tasks.get(a.TaskID); n == nil || n.State != TaskStateCancelled {
		t.Fatalf("A 应被取消：%+v", n)
	}
	if n := s.tasks.get(b.TaskID); n == nil || n.State != TaskStateRunning {
		t.Fatalf("B 不应被 A 的取消影响：%+v", n)
	}

	// ③ 同 session 的 llm 子节点按 instance 定位（不跨 instance 挂错父节点）
	llmNode, err := s.tasks.start(&TaskNode{
		Kind: TaskKindLLM, InstanceID: "ins-a", TopSession: "s-shared", SessionID: "sub-1", Name: "llm",
	})
	if err != nil {
		t.Fatalf("start llm 节点: %v", err)
	}
	if n := s.tasks.latestLLMSessionNode("ins-a", "s-shared", "sub-1"); n == nil || n.TaskID != llmNode.TaskID {
		t.Fatalf("ins-a 应命中自己的 llm 节点：%+v", n)
	}
	if n := s.tasks.latestLLMSessionNode("ins-b", "s-shared", "sub-1"); n != nil {
		t.Fatalf("ins-b 不应命中 ins-a 的 llm 节点：%+v", n)
	}

	// ④ instance 空 = 旧语义（全桶）：B 仍非终态 → 仍被取消
	if got := s.tasks.cancelByTurn("", "t-shared"); got != 1 {
		t.Fatalf("cancelByTurn(\"\") 旧语义应命中 B，got %d want 1", got)
	}
	if n := s.tasks.get(b.TaskID); n == nil || n.State != TaskStateCancelled {
		t.Fatalf("B 应被全桶取消取消：%+v", n)
	}
}

// TestTurnRuntimeIsolationAcrossInstances：同 session / turn、不同 instance → 运行态（turns / busy）
// 各一份；取消 A 只停 A，B 不受影响（缺口 5）。
func TestTurnRuntimeIsolationAcrossInstances(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	// 直接建运行态（等价 llm-start 注册后；不喂输入 → 轮次保持运行中，便于断言取消隔离）
	a := s.recoverTurnCtx("ins-a", "s-dup", "t-dup")
	b := s.recoverTurnCtx("ins-b", "s-dup", "t-dup")
	t.Cleanup(func() { a.Close(); b.Close() })

	if s.lookupTurnIn("ins-a", "t-dup") != a || s.lookupTurnIn("ins-b", "t-dup") != b {
		t.Fatal("两 instance 应各自登记运行轮次（互不覆盖）")
	}
	s.mu.Lock()
	_, aBusy := s.busy[instKey("ins-a", "s-dup")]
	_, bBusy := s.busy[instKey("ins-b", "s-dup")]
	s.mu.Unlock()
	if !aBusy || !bBusy {
		t.Fatalf("busy 应按 instance 分桶：a=%v b=%v", aBusy, bBusy)
	}

	// 取消 ins-a（载荷带 instance_id）→ 只 A 终态并移出运行态
	w := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-a", "session": "s-dup", "turn": "t-dup",
	}))
	res, _ := w.Wait().Result.(map[string]any)
	if ok, _ := res["cancelled"].(bool); !ok {
		t.Fatalf("ins-a 的轮次应被取消：%+v", res)
	}
	// 运行态由 loop 收尾异步清出 → 轮询等待（只等 A）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && s.lookupTurnIn("ins-a", "t-dup") != nil {
		time.Sleep(10 * time.Millisecond)
	}
	if s.lookupTurnIn("ins-a", "t-dup") != nil {
		t.Fatal("ins-a 的轮次应已移出运行态")
	}
	if s.lookupTurnIn("ins-b", "t-dup") != b {
		t.Fatal("ins-b 的轮次不应被 ins-a 的取消影响（缺口 5）")
	}

	// 再取消 ins-b → 同样成功（互不干扰）
	w2 := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-b", "session": "s-dup", "turn": "t-dup",
	}))
	res2, _ := w2.Wait().Result.(map[string]any)
	if ok, _ := res2["cancelled"].(bool); !ok {
		t.Fatalf("ins-b 的轮次应被取消：%+v", res2)
	}
}
