// turn 取消「快速终态 + 后台清理」白盒（第 1 条，2026-11-16）：
//
//   - 快路径：llm-cancel 在毫秒级返回（ask 终止 + 递归关子轮 + 自身 interrupted + Close），
//     不在总线派发线程做任务树 / 执行侧取消（provider 取消阶梯可能长达数十秒）；
//   - 后台 cleanup：`cancelByTurn` + 可选 `CancelTurn` 在 5s 预算内补做（幂等）；
//   - 未回复 ask_user 的 role=tool 行由 running 立即改 interrupted；
//   - 父轮取消 → 委派子轮递归 interrupted（终态屏障顺序：子 → 父）。
package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/persist"
	mcpgateway "github.com/lsong98sh/chonkpilot-agent/gateway"
)

// turnCancelCall 是一次 CancelTurn 调用的归属快照。
type turnCancelCall struct {
	instance string
	turn     string
}

// blockingTurnCancelSink 模拟带取消阶梯的执行池：CancelTurn 记录归属并阻塞 delay（生产
// gateway 的 `CancelTurn` 立即返回，阶梯在后台；此处刻意 sleep 用于证明**快路径不依赖它**）。
// 同时实现 mcpgateway.ExecSink（测试用执行池桩）与可选 turnCanceller。
type blockingTurnCancelSink struct {
	mu     sync.Mutex
	calls  []turnCancelCall
	delay  time.Duration
	signal chan struct{}
}

func newBlockingTurnCancelSink(delay time.Duration) *blockingTurnCancelSink {
	return &blockingTurnCancelSink{delay: delay, signal: make(chan struct{}, 16)}
}

func (b *blockingTurnCancelSink) OnExecState(mcpgateway.ExecState) {}

func (b *blockingTurnCancelSink) ExecStateOf(string) (string, bool) { return "", false }

func (b *blockingTurnCancelSink) CancelExec(string, string) (bool, error) { return false, nil }

func (b *blockingTurnCancelSink) DetachExec(string, string) (string, error) { return "", nil }

func (b *blockingTurnCancelSink) CancelTurn(instanceID, turnID string) (int, error) {
	if b.delay > 0 {
		time.Sleep(b.delay)
	}
	b.mu.Lock()
	b.calls = append(b.calls, turnCancelCall{instance: instanceID, turn: turnID})
	b.mu.Unlock()
	select {
	case b.signal <- struct{}{}:
	default:
	}
	return 1, nil
}

func (b *blockingTurnCancelSink) snapshot() []turnCancelCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]turnCancelCall(nil), b.calls...)
}

func (b *blockingTurnCancelSink) waitCalls(t *testing.T, n int, timeout time.Duration) []turnCancelCall {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if calls := b.snapshot(); len(calls) >= n {
			return calls
		}
		select {
		case <-b.signal:
		case <-deadline:
			t.Fatalf("等待 CancelTurn 调用数 >= %d 超时（已收到 %+v）", n, b.snapshot())
		}
	}
}

// TestLLMCancelFastTerminalAndBackgroundCleanup：处理函数 500ms 内返回；CancelTurn 收到正确
// 参数（快路径一次 + 后台 cleanup 一次）；ask 行 interrupted；后台清理 5s 内完成。
func TestLLMCancelFastTerminalAndBackgroundCleanup(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	const session, turn = "s-fast", "t-fast"
	startTurn(t, s, session, turn)

	tc := s.lookupTurn(turn)
	if tc == nil {
		t.Fatal("turn ctx not found")
	}
	// 生产形状：ask_user 发起即落 running 行（ask_id 同落），再登记等待。
	args := map[string]any{"question": "Q-fast"}
	const askID = "ask-fast-1"
	tc.persistToolRunning("tc-ask-fast", "ask_user", args, askID)
	if got := s.askWithID(turn, "tc-ask-fast", args, "", askID); got != askID {
		t.Fatalf("ask 登记失败: got=%q want=%q", got, askID)
	}

	// 注入会阻塞 200ms 的执行池桩（若快路径同步等它，处理函数会明显 > 500ms）。
	sink := newBlockingTurnCancelSink(200 * time.Millisecond)
	s.execSink = sink

	begin := time.Now()
	v := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "turn": turn,
	})).Wait()
	elapsed := time.Since(begin)
	if v.Err() != nil {
		t.Fatalf("session-cancel: %v", v.Err())
	}
	res, _ := v.Result.(map[string]any)
	if ok, _ := res["cancelled"].(bool); !ok {
		t.Fatalf("llm-cancel 应受理: %+v", res)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("llm-cancel 处理函数阻塞过久（%s > 500ms）：清理必须后置到后台", elapsed)
	}

	// 执行侧 turn 级取消已移到「关轮之后」的后台 cleanup（避免终态快照与工具结果
	// 写库交错），但必须尽快发生：2s 内至少一次，参数 = instance + turn。
	calls := sink.waitCalls(t, 1, 2*time.Second)
	for _, c := range calls {
		if c.instance != "ins-test" || c.turn != turn {
			t.Fatalf("CancelTurn 参数错误: %+v want {%s,%s}", c, "ins-test", turn)
		}
	}

	// ask 登记应被摘除。
	s.mu.Lock()
	nAsks := len(s.asks)
	s.mu.Unlock()
	if nAsks != 0 {
		t.Fatalf("取消后 ask 登记应清空，got=%d", nAsks)
	}

	// 未回复 ask 的 role=tool 行：running → interrupted。
	var found bool
	for _, r := range toolRowsOf(t, turn) {
		if persist.Sval(r["tool_call_id"]) != "tc-ask-fast" {
			continue
		}
		found = true
		if st := persist.Sval(r["tool_call_status"]); st != persist.ToolStatusInterrupted {
			t.Fatalf("未答 ask 行应改为 interrupted，got=%q", st)
		}
	}
	if !found {
		t.Fatal("未找到 ask 的 role=tool 行")
	}
}

// TestLLMCancelMarksUnansweredAskInterrupted：两条未答 ask → 取消后两行 role=tool 均为
// interrupted（用户语义「未回复的 ask_user 删除/终止」）。
func TestLLMCancelMarksUnansweredAskInterrupted(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	const session, turn = "s-int", "t-int"
	startTurn(t, s, session, turn)

	tc := s.lookupTurn(turn)
	if tc == nil {
		t.Fatal("turn ctx not found")
	}
	pairs := []struct{ toolID, askID string }{
		{"tc-int-a", "ask-int-a"},
		{"tc-int-b", "ask-int-b"},
	}
	for _, p := range pairs {
		args := map[string]any{"question": "Q-" + p.askID}
		tc.persistToolRunning(p.toolID, "ask_user", args, p.askID)
		if got := s.askWithID(turn, p.toolID, args, "", p.askID); got != p.askID {
			t.Fatalf("ask 登记失败: got=%q want=%q", got, p.askID)
		}
	}

	if v := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "turn": turn,
	})).Wait(); v.Err() != nil {
		t.Fatalf("session-cancel: %v", v.Err())
	}

	got := map[string]string{}
	for _, r := range toolRowsOf(t, turn) {
		got[persist.Sval(r["tool_call_id"])] = persist.Sval(r["tool_call_status"])
	}
	for _, p := range pairs {
		if st := got[p.toolID]; st != persist.ToolStatusInterrupted {
			t.Fatalf("%s 应为 interrupted，got=%q（全部行=%+v）", p.toolID, st, got)
		}
	}
}

// TestDelegationParentCancelClosesChildFast：父轮取消 → 委派子轮递归 interrupted，且子轮终态
// 先于父轮（终态屏障顺序不变）。
func TestDelegationParentCancelClosesChildFast(t *testing.T) {
	probe := &delegationProbe{}
	llm := delegationLLM(t, delegationScenario{childPrompt: "child block"}, probe)
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	setupDelegation(t, s)
	te := watchTasks(t, s.bus)
	cl := watchCompletes(t, s.bus)
	startDelegationTurn(t, s, "s-parent", "t-parent", "parent delegate")

	// 等子轮任务节点建立（此事件晚于 registerDelegated → 父取消必能命中子轮登记）。
	waitLLMTask(t, te, "s-child")

	if v := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-parent", "turn": "t-parent",
	})).Wait(); v.Err() != nil {
		t.Fatalf("session-cancel(parent): %v", v.Err())
	}

	child := cl.waitSession(t, "s-child", 15*time.Second)
	parent := cl.waitSession(t, "s-parent", 15*time.Second)
	if child.Status != "interrupted" || parent.Status != "interrupted" {
		t.Fatalf("父/子轮次均应为 interrupted：parent=%q child=%q", parent.Status, child.Status)
	}
	list := cl.snapshot()
	if ci, pi := indexOfSession(list, "s-child"), indexOfSession(list, "s-parent"); ci < 0 || pi < 0 || ci > pi {
		t.Fatalf("终态顺序应为 子→父：%+v", list)
	}
}
