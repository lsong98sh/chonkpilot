// 「manual 在飞 → 取消级联命中执行侧」白盒（RB-6 / I-83 残留缺口的回归证据）：
//
// I-83 残留缺口（登记于 2026-09-17）称：`turn.go` 的 `setGwTask` 只在「已转异步」分支执行 →
// 在飞的 manual/never 任务未登记 `gwTaskID` → 级联取消打不到执行侧。
//
// **复核结论（2026-09-21，RB-6）**：该缺口已被 **P3-① 执行态纳入**（2026-09-18/19）关闭 ——
// gateway 在 `doCall` 启动执行后**立即**上报执行态 `started`（带 `gw_task_id` + `tool_call_id`），
// `taskExecSink.OnExecState` → `Layer.OnExecState` 按 `tool_call_id` 命中 llm 侧节点并**补登记
// exec 映射**（`src/lib/task/exec.go` 解析顺序 ②）→ 层随后对持有 exec 句柄的节点回调
// `OnCancelExec` → `sink.CancelExec(instance, gw_task_id)`。**无需在 turn.go 追加登记**（该处亦无
// gw 执行 id 可得：同步调用在 manual/never 待裁决期间仍阻塞）。
//
// 本用例即 RB-6 的验收白盒：**不调 setGwTask**（模拟真实在飞 manual），仅经执行态 started 补登记
// → 级联取消（session-cancel → cancelByTurn → CancelSubtree）→ 断言命中 `sink.CancelExec`。
package server

import (
	"context"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
	tasklayer "github.com/chonkpilot/chonkpilot-task"
)

// TestManualInflightCascadeCancelReachesExec：manual 在飞（未 setGwTask）→ 级联取消命中执行侧。
func TestManualInflightCascadeCancelReachesExec(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-rb6", "t-rb6")
	resetExecControl()

	node, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-rb6", TurnID: "t-rb6",
		InstanceID: "ins-test", TopSession: "s-rb6", Name: "inflight", ToolCallID: "tc-rb6",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// gateway 执行态 started（生产链路：doCall emitExec(started) → taskExecSink.OnExecState → 层）
	s.taskLayer.OnExecState(tasklayer.ExecState{
		GWTaskID: "gw-rb6", InstanceID: "ins-test", TopSession: "s-rb6",
		Tool: "core_file_read", ToolCallID: "tc-rb6", Phase: tasklayer.ExecPhaseStarted, State: "running",
	})
	// ① 层已按 tool_call_id 补登记 exec 映射（在飞 manual/never 亦如此）
	if ex, ok := s.taskLayer.ExecOf(node.TaskID); !ok || ex.GWTaskID != "gw-rb6" {
		t.Fatalf("层应补登记 exec 映射: %+v ok=%v", ex, ok)
	}

	// ② 级联取消（session-cancel）→ 命中执行侧
	if err := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-rb6", "turn": "t-rb6",
	})).Wait().Err(); err != nil {
		t.Fatalf("session-cancel: %v", err)
	}
	if got := execCancelledRefs(); len(got) != 1 || got[0] != "gw-rb6" {
		t.Fatalf("级联取消应命中执行侧：sink.CancelExec=%v want [gw-rb6]", got)
	}
}

// TestNeverInflightCascadeCancelReachesExecViaAdapter（RB-6 补强）：`never` 模式在飞（未 setGwTask）
// —— 与上一用例如出一辙，但**经生产衔接** `taskExecSink.OnExecState`（tasks.go 的字段透传：
// gateway ExecState → 层 ExecState，tool_call_id 必须原样带过）补登记 → 级联取消命中执行侧。
// 该适配器即 gateway 执行态进入层的唯一入口，独立覆盖「gateway → 层」交界。
func TestNeverInflightCascadeCancelReachesExecViaAdapter(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-rb6n", "t-rb6n")
	resetExecControl()

	node, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-rb6n", TurnID: "t-rb6n",
		InstanceID: "ins-test", TopSession: "s-rb6n", Name: "inflight-never", ToolCallID: "tc-rb6n",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// 生产链路：gateway doCall emitExec(started) → taskExecSink.OnExecState → 层
	sink := &taskExecSink{layer: s.taskLayer}
	sink.OnExecState(mcpgateway.ExecState{
		GWTaskID: "gw-rb6n", InstanceID: "ins-test", TopSession: "s-rb6n",
		Tool: "core_file_read", ToolCallID: "tc-rb6n", Phase: mcpgateway.ExecPhaseStarted, State: "running",
	})
	if ex, ok := s.taskLayer.ExecOf(node.TaskID); !ok || ex.GWTaskID != "gw-rb6n" {
		t.Fatalf("层应经适配器补登记 exec 映射: %+v ok=%v", ex, ok)
	}
	if err := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-rb6n", "turn": "t-rb6n",
	})).Wait().Err(); err != nil {
		t.Fatalf("session-cancel: %v", err)
	}
	if got := execCancelledRefs(); len(got) != 1 || got[0] != "gw-rb6n" {
		t.Fatalf("级联取消应命中执行侧：sink.CancelExec=%v want [gw-rb6n]", got)
	}
}

// ── 回归（2026-09-22）：llm-cancel 级联取消**不得在持 s.mu 时重入总线** ──────────────
//
// 生产链路（宿主实测：run_llm 五.8「等待时 Cancel」→ 宿主窗口整体冻结、/eval 全部
// "ExecuteScript timed out"、/screenshot 500）：
//
//	llm-cancel → onLLMCancel **持 s.mu** → cancelByTurn → 层 CancelSubtree → OnCancelExec
//	  → sink.CancelExec → gateway ep.cancel → reportTaskDone →【同 goroutine 同步】
//	    Emit(mcp-tasks-report) → onGatewayTaskDone → s.mu.Lock()（非重入互斥）→ **自锁（无超时）**
//
// 总线 Emit 按 order **同步执行订阅者**（mq/bus.go 注释），故级联取消必须在 s.mu 之外。
// 本用例以**重入 sink**（CancelExec 内同步 emit mcp-tasks-report）复刻该链，并用超时守护把
// 「自锁」变成可判定的失败（而非挂死测试进程）。
type reentrantCancelSink struct {
	bus  mq.Bus
	refs []string
}

func (r *reentrantCancelSink) OnExecState(mcpgateway.ExecState)          {}
func (r *reentrantCancelSink) ExecStateOf(string) (string, bool)         { return "", false }
func (r *reentrantCancelSink) DetachExec(string, string) (string, error) { return "", nil }

// CancelExec 复刻 gateway `ep.cancel` 的取消后回报（reportTaskDone → 同步 emit mcp-tasks-report）。
func (r *reentrantCancelSink) CancelExec(instanceID, gwTaskID string) (bool, error) {
	r.refs = append(r.refs, gwTaskID)
	r.bus.Emit(context.Background(), "mcp-tasks-report", jb(map[string]any{
		"instance_id": instanceID, "task_id": gwTaskID, "tool_call_id": "tc-dl",
		"tool": "core_file_read", "state": "cancelled", "result_summary": "",
	}))
	return true, nil
}

func TestLLMCancelCascadeDoesNotSelfDeadlock(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-dl", "t-dl")

	if _, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-dl", TurnID: "t-dl",
		InstanceID: "ins-test", TopSession: "s-dl", Name: "inflight", ToolCallID: "tc-dl",
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	// 层补登记 exec 句柄（生产链路：gateway 执行态 started → OnExecState）
	s.taskLayer.OnExecState(tasklayer.ExecState{
		GWTaskID: "gw-dl", InstanceID: "ins-test", TopSession: "s-dl",
		Tool: "core_file_read", ToolCallID: "tc-dl", Phase: tasklayer.ExecPhaseStarted, State: "running",
	})
	sink := &reentrantCancelSink{bus: s.bus}
	s.execSink = sink

	done := make(chan error, 1)
	go func() {
		done <- s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
			"instance_id": "ins-test", "session": "s-dl", "turn": "t-dl",
		})).Wait().Err()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("session-cancel: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("llm-cancel 级联取消自锁（持 s.mu 重入总线 → onGatewayTaskDone 等同一把锁）：宿主表现为 UI 线程冻结")
	}
	if len(sink.refs) != 1 || sink.refs[0] != "gw-dl" {
		t.Fatalf("级联取消应命中执行侧：sink.CancelExec=%v want [gw-dl]", sink.refs)
	}
}
