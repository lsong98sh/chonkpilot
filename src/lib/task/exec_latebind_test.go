// 回归：执行态早于节点登记（dsl_run 作业根）→ 节点登记后补绑 exec 句柄，task-stop 可下发执行侧取消。
//
// 生产链路：dsl_run 被调用 → gateway 执行池 spawn（exec started，带 tool_call_id）→ 执行器
// 上行静态树才建 `dsl_job` 节点（task-started）。旧实现只在**完成回报**时才按 tool_call_id
// 补登记 exec 映射 → 长跑作业在完成前 task-stop 拿不到 gw_task_id → 取消无效（步骤永远 running）。
package task

import (
	"testing"
)

func TestCancelSubtreeLateBindsExecOnNodeRegistration(t *testing.T) {
	var cancelled []string
	l, _ := newLayerWith(t, Options{OnCancelExec: func(taskID string, ex Exec) {
		cancelled = append(cancelled, taskID+":"+ex.GWTaskID)
	}})

	// ① 执行态 started 先到（节点尚未登记）
	l.OnExecState(ExecState{
		GWTaskID: "gw-dsl-1", InstanceID: "ins-test", TopSession: "top-1",
		Tool: "dsl_run", ToolCallID: "tc-dsl-1", Phase: ExecPhaseStarted, State: "running",
	})
	if _, ok := l.ExecOf("dsljob-1"); ok {
		t.Fatal("节点未登记前不应有 exec 句柄")
	}

	// ② 节点随后登记（task-started）
	if err := l.Apply(subjTaskStarted, nodePayload(map[string]any{
		"task_id": "dsljob-1", "kind": "dsl_job", "node_type": "tool", "tool": "dsl_run",
		"tool_call_id": "tc-dsl-1", "parent_id": nil,
	})); err != nil {
		t.Fatalf("Apply task-started: %v", err)
	}

	// ③ 晚到的 started 已补绑（exec 映射）
	ex, ok := l.ExecOf("dsljob-1")
	if !ok || ex.GWTaskID != "gw-dsl-1" {
		t.Fatalf("节点登记后应补绑 exec 句柄: %+v ok=%v", ex, ok)
	}

	// ④ task-stop 路径（层判定 + 执行侧回调）
	ids := l.CancelSubtree("ins-test", "dsljob-1", "")
	if len(ids) != 1 || ids[0] != "dsljob-1" {
		t.Fatalf("CancelSubtree ids=%v，期望 [dsljob-1]", ids)
	}
	if len(cancelled) != 1 || cancelled[0] != "dsljob-1:gw-dsl-1" {
		t.Fatalf("执行侧回调=%v，期望 [dsljob-1:gw-dsl-1]", cancelled)
	}
}

// TestExecStateNotBoundToStaleNodeOfOtherInstance：执行态 / 节点按 tool_call_id 匹配必须带归属。
// 启动恢复后的上一进程节点（interrupted）与新进程执行同名 tool_call_id（ex id 跨进程复用）→
// 不得把新执行态错绑到遗留节点（否则新节点永远拿不到 exec 句柄，task-stop 失效）。
func TestExecStateNotBoundToStaleNodeOfOtherInstance(t *testing.T) {
	l, _ := newLayer(t)

	// 上一进程遗留节点（已启动恢复为 interrupted），同 tool_call_id
	if err := l.Apply(subjTaskStarted, nodePayload(map[string]any{
		"task_id": "dsljob-old", "kind": "dsl_job", "tool": "dsl_run", "tool_call_id": "tc-1",
		"instance_id": "ins-old", "top_session": "top-1", "parent_id": nil, "state": "interrupted",
	})); err != nil {
		t.Fatalf("Apply stale: %v", err)
	}

	// 新进程执行态（同 tool_call_id / 同 top_session，不同 instance）
	l.OnExecState(ExecState{
		GWTaskID: "ex-0001", InstanceID: "ins-new", TopSession: "top-1",
		Tool: "dsl_run", ToolCallID: "tc-1", Phase: ExecPhaseStarted, State: "running",
	})
	if ex, ok := l.ExecOf("dsljob-old"); ok {
		t.Fatalf("新执行态不得绑到其他 instance 的遗留节点: %+v", ex)
	}

	// 新节点登记 → 执行态补绑到新节点
	if err := l.Apply(subjTaskStarted, nodePayload(map[string]any{
		"task_id": "dsljob-new", "kind": "dsl_job", "tool": "dsl_run", "tool_call_id": "tc-1",
		"instance_id": "ins-new", "top_session": "top-1", "parent_id": nil,
	})); err != nil {
		t.Fatalf("Apply new: %v", err)
	}
	ex, ok := l.ExecOf("dsljob-new")
	if !ok || ex.GWTaskID != "ex-0001" {
		t.Fatalf("新节点应补绑执行态: %+v ok=%v", ex, ok)
	}
	if _, ok := l.ExecOf("dsljob-old"); ok {
		t.Fatal("遗留节点不应获得新执行句柄")
	}
}
