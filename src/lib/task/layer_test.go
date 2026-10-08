// 任务层 L1 单测（21 §9.2 P2）：payload → 记录映射（三类事件）· 幂等 · 状态单调 ·
// 撞号拒绝 · 逻辑删除标记 · 控制面以层为权威（CancelSubtree + 执行侧回调）· 启动恢复。
// 全部经内存总线 + data 面桩驱动，不依赖真实 DB / GUI。
package task

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestApplyEventMapping：llm 三类事件（tasks.started / tasks.updated / tasks.done →
// task-started / task-updated / task-done）→ 权威行字段映射（含 P2 增补字段 workdir）。
func TestApplyEventMapping(t *testing.T) {
	cases := []struct {
		name        string
		subject     string
		payload     []byte
		wantState   string
		wantDoneAt  string
		wantSession string
	}{
		{
			name: "tasks.started", subject: subjTaskStarted,
			payload:     nodePayload(nil),
			wantState:   "running",
			wantSession: "s-1",
		},
		{
			name: "tasks.updated", subject: subjTaskUpdated,
			payload: nodePayload(map[string]any{
				"state": "running", "session_id": "s-sub", "simplified": "读取配置文件",
			}),
			wantState:   "running",
			wantSession: "s-sub",
		},
		{
			name: "tasks.done", subject: subjTaskDone,
			payload: nodePayload(map[string]any{
				"state": "done", "finished_at": "2026-09-18T00:01:00Z", "result_summary": "文件已读取",
			}),
			wantState:   "done",
			wantDoneAt:  "2026-09-18T00:01:00Z",
			wantSession: "s-1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, fake := newLayer(t)
			if err := l.Apply(tc.subject, tc.payload); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			row := fake.row("tk-1")
			if row == nil {
				t.Fatal("权威行未写入")
			}
			want := map[string]string{
				"node_id": "tk-1", "task_id": "tk-1", "top_session": "top-1",
				"session_id": tc.wantSession, "kind": "tool", "node_type": "tool",
				"parent_node_id": "tk-root", "created_at": "2026-09-18T00:00:00Z",
				"status": tc.wantState, "state": tc.wantState, "tool_call_id": "tc-1",
				"instance_id": "ins-test", "work_dir": "E:/ws",
			}
			for k, wantV := range want {
				if got := sval(row[k]); got != wantV {
					t.Fatalf("行字段 %s=%q want %q（row=%+v）", k, got, wantV, row)
				}
			}
			if got := sval(row["finished_at"]); got != tc.wantDoneAt {
				t.Fatalf("finished_at=%q want %q", got, tc.wantDoneAt)
			}
			// 标题：simplified 优先（updated 用例带 simplified）
			if tc.name == "tasks.updated" && sval(row["title"]) != "读取配置文件" {
				t.Fatalf("title=%q want 读取配置文件", sval(row["title"]))
			}
			if tc.name == "tasks.done" && sval(row["result_digest"]) == "" {
				t.Fatal("done 事件应带结果摘要（result_digest）")
			}
			// 层内热视图与落库行一致
			rec := l.Record("tk-1")
			if rec == nil || rec.State != tc.wantState || rec.TopSession != "top-1" || rec.WorkDir != "E:/ws" {
				t.Fatalf("层内记录=%+v", rec)
			}
		})
	}
}

// TestApplyReportMapping：gateway mcp-tasks-report（工具终态回报）→ 推进已知节点状态；
// 未登记节点的回报跳过（不建树，节点权威来源是 llm 的 task-* 事件）。
func TestApplyReportMapping(t *testing.T) {
	l, fake := newLayer(t)
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil {
		t.Fatalf("Apply(started): %v", err)
	}
	if err := l.Apply(subjTaskReport, reportPayload(nil)); err != nil {
		t.Fatalf("Apply(report): %v", err)
	}
	rec := l.Record("tk-1")
	if rec == nil || rec.State != "done" || rec.DoneAt == "" || rec.ResultDigest == "" {
		t.Fatalf("回报未推进节点状态: %+v", rec)
	}
	if fake.upsertCount() != 2 {
		t.Fatalf("upsert 次数=%d want 2（节点写入 + 回报推进）", fake.upsertCount())
	}

	// 未登记节点 → 跳过（不写库、不报错）
	if err := l.Apply(subjTaskReport, reportPayload(map[string]any{"task_id": "tk-unknown"})); err != nil {
		t.Fatalf("未登记回报应静默跳过: %v", err)
	}
	if fake.row("tk-unknown") != nil {
		t.Fatal("未登记回报不应建行")
	}
}

// TestRecordFromReportFields：report → Record 纯映射（字段级）。
func TestRecordFromReportFields(t *testing.T) {
	var ev reportEvent
	if err := json.Unmarshal(reportPayload(map[string]any{
		"state": "cancelled", "result_summary": "用户取消",
		"top_session": "top-1", "parent": "tk-parent",
	}), &ev); err != nil {
		t.Fatal(err)
	}
	rec, err := recordFromReport(&ev)
	if err != nil {
		t.Fatalf("recordFromReport: %v", err)
	}
	if rec.TaskID != "tk-1" || rec.State != "cancelled" || rec.Tool != "core_read" ||
		rec.ToolCallID != "tc-1" || rec.SessionID != "s-1" || rec.InstanceID != "ins-test" {
		t.Fatalf("report → Record 映射错: %+v", rec)
	}
	// I-90 载荷增补：树归属（top_session / parent）同映射
	if rec.TopSession != "top-1" || rec.ParentID != "tk-parent" {
		t.Fatalf("report 树归属映射错: top_session=%q parent=%q", rec.TopSession, rec.ParentID)
	}
	if rec.ResultDigest == "" {
		t.Fatal("结果摘要应非空")
	}
	if rec.NodeType != NodeTypeTool {
		t.Fatalf("NodeType=%q want tool", rec.NodeType)
	}
}

// TestApplyReportBuildsNode：I-90 —— gateway 完成回报（带 `top_session`）**独立建节点**，
// 树归属三级回落（parent 载荷 > tool_call_id 匹配 > 顶层）各记日志；
// 无 top_session / 状态不可识别 → 维持既有口径跳过（不建行）。
// 另覆盖 P3 前置修正：回报 `tool_call_id` 命中**已登记**节点 → 只推进该节点（不建重复行）。
func TestApplyReportBuildsNode(t *testing.T) {
	var mu sync.Mutex
	var logs []string
	l, fake := newLayerWith(t, Options{Logf: func(format string, args ...any) {
		mu.Lock()
		logs = append(logs, fmt.Sprintf(format, args...))
		mu.Unlock()
	}})
	logged := func(sub string) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, s := range logs {
			if strings.Contains(s, sub) {
				return true
			}
		}
		return false
	}

	// 预登记一个 llm 侧节点（tk-1，tool_call_id = tc-1，top-1）
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil {
		t.Fatalf("Apply(started): %v", err)
	}

	// ① parent 非空（tool_call_id 不命中已登记节点）→ 挂该父（调用层给出，层不猜不重排）
	if err := l.Apply(subjTaskReport, reportPayload(map[string]any{
		"task_id": "tk-gw-1", "top_session": "top-1", "parent": "tk-root", "tool_call_id": "tc-p1",
	})); err != nil {
		t.Fatalf("Apply(report/parent): %v", err)
	}
	rec := l.Record("tk-gw-1")
	if rec == nil || rec.ParentID != "tk-root" || rec.TopSession != "top-1" ||
		rec.State != StateDone || rec.DoneAt == "" || rec.NodeType != NodeTypeTool {
		t.Fatalf("① parent 回落建节点错: %+v", rec)
	}
	if !logged("按回报 parent 挂父") {
		t.Fatalf("① 未记回落日志: %v", logs)
	}

	// ② tool_call_id 命中**已登记**节点 → 只推进该节点（**不建新行**）
	if err := l.Apply(subjTaskReport, reportPayload(map[string]any{
		"task_id": "tk-gw-2", "top_session": "top-1", "parent": nil,
	})); err != nil {
		t.Fatalf("Apply(report/match): %v", err)
	}
	if r := l.Record("tk-gw-2"); r != nil {
		t.Fatalf("tool_call_id 命中后不得为 gateway 执行 id 建行（重复行）: %+v", r)
	}
	if fake.row("tk-gw-2") != nil {
		t.Fatal("tool_call_id 命中后不得建库行")
	}
	if rec := l.Record("tk-1"); rec == nil || rec.State != StateDone {
		t.Fatalf("② tool_call_id 命中应推进既有节点: %+v", rec)
	}
	if !logged("按 tool_call_id 命中已登记节点") {
		t.Fatalf("② 未记命中日志: %v", logs)
	}

	// ③ parent 空 + tool_call_id 无匹配 → 该 top_session 下顶层节点
	if err := l.Apply(subjTaskReport, reportPayload(map[string]any{
		"task_id": "tk-gw-3", "top_session": "top-1", "parent": nil, "tool_call_id": "tc-p3",
	})); err != nil {
		t.Fatalf("Apply(report/top): %v", err)
	}
	if rec := l.Record("tk-gw-3"); rec == nil || rec.ParentID != "" {
		t.Fatalf("③ 顶层回落错: %+v", rec)
	}
	if !logged("作为 top_session 下的顶层节点") {
		t.Fatalf("③ 未记回落日志: %v", logs)
	}

	// 落库次数：1 条 started + ①建行 + ②推进 tk-1（running→done） + ③建行 = 4
	if n := fake.upsertCount(); n != 4 {
		t.Fatalf("upsert=%d want 4", n)
	}
	// 库行数：tk-1 + tk-gw-1 + tk-gw-3（tk-gw-2 不建行）
	if n := fake.rowCount(); n != 3 {
		t.Fatalf("权威行数=%d want 3（tk-gw-2 不得建行）", n)
	}
	if row := fake.row("tk-gw-1"); row == nil || sval(row["parent_node_id"]) != "tk-root" ||
		sval(row["status"]) != StateDone || sval(row["top_session"]) != "top-1" ||
		sval(row["node_type"]) != "tool" {
		t.Fatalf("① 权威行错: %+v", row)
	}

	// ④ 未登记且无 top_session → 跳过（不建行，不报错）
	if err := l.Apply(subjTaskReport, reportPayload(map[string]any{
		"task_id": "tk-gw-4", "top_session": nil, "tool_call_id": "tc-none",
	})); err != nil {
		t.Fatalf("无 top_session 应静默跳过: %v", err)
	}
	if fake.row("tk-gw-4") != nil {
		t.Fatal("无 top_session 不应建行")
	}
	if !logged("无 top_session") {
		t.Fatalf("④ 未记日志: %v", logs)
	}

	// ⑤ 未登记 + top_session + 状态不可识别 → 跳过（不写脏状态行）
	if err := l.Apply(subjTaskReport, reportPayload(map[string]any{
		"task_id": "tk-gw-5", "top_session": "top-1", "state": "weird", "tool_call_id": "tc-none",
	})); err != nil {
		t.Fatalf("状态非法应静默跳过: %v", err)
	}
	if fake.row("tk-gw-5") != nil {
		t.Fatal("状态不可识别不应建行")
	}
	if !logged("状态不可识别") {
		t.Fatalf("⑤ 未记日志: %v", logs)
	}
}

// TestApplyReportExecMappingNoDuplicateRow：**重复行缺陷修复**（2026-09-18）——完成回报的 `task_id`
// 是 **gateway 执行 id**（如 `tk-0001`），与 llm 侧登记节点 id（`tk-1`）**不同源**：
//
//	① 已有 exec 映射（BindExec）+ 回报（不带 tool_call_id）→ **仍只有一行**（不建 `tk-0001` 行）；
//	② 无映射 + 回报不带匹配的 tool_call_id + 有 `top_session` → 建一行（顶层回落）；
//	③ 无映射 + 无 `top_session` → 不建行且不报错。
//
// （回报 `tool_call_id` 命中已登记节点的口径见 TestApplyReportToolCallIDNoDuplicateRow。）
func TestApplyReportExecMappingNoDuplicateRow(t *testing.T) {
	l, fake := newLayer(t)
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil { // tk-1（tool_call_id=tc-1）
		t.Fatalf("Apply(started): %v", err)
	}
	// ① 有映射 + 回报（gateway 执行 id）→ 只推进既有节点，不建新行
	l.BindExec("tk-1", Exec{GWTaskID: "tk-0001", InstanceID: "ins-test", Tool: "core_file_read"})
	if err := l.Apply(subjTaskReport, reportPayload(map[string]any{
		"task_id": "tk-0001", "top_session": "top-1", "state": "done", "result_summary": "ok",
		"tool_call_id": nil,
	})); err != nil {
		t.Fatalf("Apply(report/映射命中): %v", err)
	}
	if fake.row("tk-0001") != nil {
		t.Fatal("映射命中后不得为 gateway 执行 id 建行（重复行）")
	}
	if n := fake.rowCount(); n != 1 {
		t.Fatalf("权威行数=%d want 1（无重复行）", n)
	}
	rec := l.Record("tk-1")
	if rec == nil || rec.State != StateDone || rec.DoneAt == "" || rec.ResultDigest == "" {
		t.Fatalf("既有节点未按回报推进: %+v", rec)
	}
	if n := fake.upsertCount(); n != 2 {
		t.Fatalf("upsert=%d want 2（登记 + 回报推进；不建新行）", n)
	}

	// ② 无映射 + 无匹配 tool_call_id + 有 top_session → 建一行（顶层回落）
	if err := l.Apply(subjTaskReport, reportPayload(map[string]any{
		"task_id": "tk-0002", "top_session": "top-1", "state": "done", "tool_call_id": nil,
	})); err != nil {
		t.Fatalf("Apply(report/未登记建行): %v", err)
	}
	if r := l.Record("tk-0002"); r == nil || r.ParentID != "" || r.TopSession != "top-1" {
		t.Fatalf("未登记回报建行错: %+v", r)
	}
	if n := fake.rowCount(); n != 2 {
		t.Fatalf("权威行数=%d want 2（新增一行）", n)
	}

	// ③ 无映射 + 无 top_session → 不建行、不报错
	if err := l.Apply(subjTaskReport, reportPayload(map[string]any{
		"task_id": "tk-0003", "top_session": nil,
	})); err != nil {
		t.Fatalf("无 top_session 应静默跳过: %v", err)
	}
	if fake.row("tk-0003") != nil || fake.rowCount() != 2 {
		t.Fatalf("无 top_session 不应建行（行数=%d）", fake.rowCount())
	}
}

// TestApplyReportToolCallIDNoDuplicateRow：**交付 1 回归**（P3 前置修正）——llm 先登记节点
// （`tk-1` / `tool_call_id=tc-1`），gateway 完成回报用**不同 `task_id`（执行 id `tk-0009`）+ 同
// `tool_call_id`** → 层按 `tool_call_id` 命中已登记节点：**库中只有 1 行**、其 `exec.gw_task_id`
// = 回报 id、状态为回报终态（**不为 gateway 执行 id 建第二行**）。
func TestApplyReportToolCallIDNoDuplicateRow(t *testing.T) {
	l, fake := newLayer(t)
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil { // tk-1 / tc-1 / top-1
		t.Fatalf("Apply(started): %v", err)
	}
	if err := l.Apply(subjTaskReport, reportPayload(map[string]any{
		"task_id": "tk-0009", "tool_call_id": "tc-1", "top_session": "top-1", "state": "cancelled",
	})); err != nil {
		t.Fatalf("Apply(report): %v", err)
	}
	if n := fake.rowCount(); n != 1 {
		t.Fatalf("权威行数=%d want 1（不得为回报的执行 id 建行）", n)
	}
	if fake.row("tk-0009") != nil {
		t.Fatal("不得为 gateway 执行 id（tk-0009）建行")
	}
	rec := l.Record("tk-1")
	if rec == nil || rec.State != StateCancelled {
		t.Fatalf("已登记节点未按回报推进终态: %+v", rec)
	}
	exec, ok := l.ExecOf("tk-1")
	if !ok || exec.GWTaskID != "tk-0009" {
		t.Fatalf("gateway 执行 id 未记入 exec.gw_task_id: %+v ok=%v", exec, ok)
	}
	if row := fake.row("tk-1"); sval(row["status"]) != StateCancelled {
		t.Fatalf("权威行状态=%q want cancelled", sval(row["status"]))
	}
	// 后续执行态上报按该映射命中同一行（不建行）
	l.OnExecState(ExecState{
		GWTaskID: "tk-0009", InstanceID: "ins-test", TopSession: "top-1", ToolCallID: "tc-1",
		Phase: ExecPhaseError, State: "cancelled", Message: "cancelled",
	})
	if n := fake.rowCount(); n != 1 {
		t.Fatalf("执行态上报后权威行数=%d want 1", n)
	}
	if rec := l.Record("tk-1"); rec == nil || rec.State != StateCancelled {
		t.Fatalf("不可逆终态不得被改写: %+v", rec)
	}
	if st, ok := l.ExecStateOf("tk-0009"); !ok || st != StateCancelled {
		t.Fatalf("ExecStateOf=%q ok=%v want cancelled", st, ok)
	}
}

// TestApplyIdempotent：同一事件重复到达（含内容一致的不同事件）→ 只落库一次。
func TestApplyIdempotent(t *testing.T) {
	l, fake := newLayer(t)
	payload := nodePayload(nil)
	for i := 0; i < 3; i++ {
		if err := l.Apply(subjTaskStarted, payload); err != nil {
			t.Fatalf("Apply #%d: %v", i, err)
		}
	}
	if n := fake.upsertCount(); n != 1 {
		t.Fatalf("重复事件落库 %d 次 want 1（幂等）", n)
	}
	// 同内容但走 task-updated（running 同态）→ 仍幂等
	if err := l.Apply(subjTaskUpdated, payload); err != nil {
		t.Fatalf("Apply(updated): %v", err)
	}
	if n := fake.upsertCount(); n != 1 {
		t.Fatalf("同态重复落库 %d 次 want 1", n)
	}
}

// TestApplyStateMonotonic：状态单调——不可逆终态与 pending→running 回退被拒绝并记日志，
// 不 panic、不落库；running → pending（异步转后台的真实事件流）必须放行。
func TestApplyStateMonotonic(t *testing.T) {
	l, fake := newLayer(t)

	// 转后台：running → pending（turn.go setState(pending)）→ 允许
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil {
		t.Fatalf("Apply(running): %v", err)
	}
	if err := l.Apply(subjTaskUpdated, nodePayload(map[string]any{"state": "pending"})); err != nil {
		t.Fatalf("running → pending（异步转后台）应放行: %v", err)
	}
	if rec := l.Record("tk-1"); rec == nil || rec.State != "pending" {
		t.Fatalf("转后台后状态=%+v", rec)
	}
	if n := fake.upsertCount(); n != 2 {
		t.Fatalf("upsert=%d want 2", n)
	}

	// 回退：pending → running 被拒绝（21 §7.4 单向）
	err := l.Apply(subjTaskUpdated, nodePayload(map[string]any{"state": "running"}))
	if err == nil || !strings.Contains(err.Error(), "状态转移非法") {
		t.Fatalf("pending → running 应被拒绝，got err=%v", err)
	}
	if rec := l.Record("tk-1"); rec == nil || rec.State != "pending" {
		t.Fatalf("被拒后状态被改写: %+v", rec)
	}
	if n := fake.upsertCount(); n != 2 {
		t.Fatalf("被拒事件不应落库（upsert=%d want 2）", n)
	}

	// 正向：pending → done 允许
	if err := l.Apply(subjTaskDone, nodePayload(map[string]any{
		"state": "done", "finished_at": "2026-09-18T00:02:00Z",
	})); err != nil {
		t.Fatalf("Apply(done): %v", err)
	}
	if n := fake.upsertCount(); n != 3 {
		t.Fatalf("终态应落库（upsert=%d want 3）", n)
	}

	// 不可逆终态：done → running 被拒绝
	err = l.Apply(subjTaskUpdated, nodePayload(map[string]any{"state": "running"}))
	if err == nil || !strings.Contains(err.Error(), "状态转移非法") {
		t.Fatalf("done → running 应被拒绝，got err=%v", err)
	}
	if rec := l.Record("tk-1"); rec == nil || rec.State != "done" {
		t.Fatalf("终态被回退: %+v", rec)
	}

	// 未知状态 → 拒绝（不入脏值）
	err = l.Apply(subjTaskUpdated, nodePayload(map[string]any{"state": "weird"}))
	if err == nil {
		t.Fatal("未知状态应被拒绝")
	}
}

// TestInterruptedResumable：interrupted 为**可恢复终态**——tool-retry 重跑走
// interrupted → running（21 §9.2：不得被状态单调校验拒绝）。
func TestInterruptedResumable(t *testing.T) {
	l, fake := newLayer(t)
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil {
		t.Fatalf("Apply(running): %v", err)
	}
	if err := l.Apply(subjTaskDone, nodePayload(map[string]any{
		"state": "interrupted", "finished_at": "2026-09-18T00:03:00Z",
	})); err != nil {
		t.Fatalf("Apply(interrupted): %v", err)
	}
	// 重跑：interrupted → running（retryReset 广播 tasks.updated）
	if err := l.Apply(subjTaskUpdated, nodePayload(map[string]any{
		"state": "running", "finished_at": nil,
	})); err != nil {
		t.Fatalf("interrupted → running（重跑）应放行: %v", err)
	}
	if rec := l.Record("tk-1"); rec == nil || rec.State != "running" {
		t.Fatalf("重跑后状态=%+v", rec)
	}
	// 重跑终态落库：running → done
	if err := l.Apply(subjTaskDone, nodePayload(map[string]any{
		"state": "done", "finished_at": "2026-09-18T00:04:00Z",
	})); err != nil {
		t.Fatalf("重跑终态: %v", err)
	}
	if rec := l.Record("tk-1"); rec == nil || rec.State != "done" {
		t.Fatalf("重跑终态=%+v", rec)
	}
	if n := fake.upsertCount(); n != 4 {
		t.Fatalf("upsert=%d want 4（running/interrupted/running(重跑)/done）", n)
	}
}

// TestInterruptedNotDowngradedByPendingSnapshot：I-100 收严——`interrupted`（可恢复终态）写入后，
// incoming llm `pending` 粗粒度快照**不得回退**（保留 interrupted，其余字段照常合并、不报错）；
// 唯一例外 interrupted → running（tool-retry 重跑）仍放行（见 TestInterruptedResumable）。
func TestInterruptedNotDowngradedByPendingSnapshot(t *testing.T) {
	var mu sync.Mutex
	var logs []string
	l, _ := newLayerWith(t, Options{Logf: func(format string, args ...any) {
		mu.Lock()
		logs = append(logs, fmt.Sprintf(format, args...))
		mu.Unlock()
	}})
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil {
		t.Fatalf("Apply(running): %v", err)
	}
	if err := l.Apply(subjTaskDone, nodePayload(map[string]any{
		"state": "interrupted", "finished_at": "2026-09-18T00:03:00Z",
	})); err != nil {
		t.Fatalf("Apply(interrupted): %v", err)
	}
	// 陈旧 pending 快照（重启遗留不得被「未执行」粗粒度覆盖）→ 状态保留 interrupted，字段照常合并。
	if err := l.Apply(subjTaskUpdated, nodePayload(map[string]any{
		"state": "pending", "simplified": "陈旧快照",
	})); err != nil {
		t.Fatalf("Apply(pending 陈旧快照): %v", err)
	}
	rec := l.Record("tk-1")
	if rec == nil || rec.State != StateInterrupted {
		t.Fatalf("pending 快照把 interrupted 回退了: %+v", rec)
	}
	if rec.Title != "陈旧快照" {
		t.Fatalf("其余字段未照常合并（title=%q）: %+v", rec.Title, rec)
	}
	mu.Lock()
	joined := strings.Join(logs, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "状态优先级") {
		t.Fatalf("interrupted 回退未被拦截/未记日志: %v", logs)
	}
}

// TestApplyConflictRejected：撞号（同 (workdir, task_id) 语义冲突）→ 拒绝并记错误日志，层不覆盖。
func TestApplyConflictRejected(t *testing.T) {
	l, fake := newLayer(t)
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	err := l.Apply(subjTaskStarted, nodePayload(map[string]any{"top_session": "top-other"}))
	if err == nil || !strings.Contains(err.Error(), "撞号拒绝") {
		t.Fatalf("同 task_id 异 top_session 应被拒，got err=%v", err)
	}
	if rec := l.Record("tk-1"); rec == nil || rec.TopSession != "top-1" {
		t.Fatalf("撞号后被覆盖: %+v", rec)
	}
	if fake.upsertCount() != 1 {
		t.Fatalf("撞号事件不应落库（upsert=%d want 1）", fake.upsertCount())
	}
	// tool_call_id 冲突同样拒绝
	err = l.Apply(subjTaskUpdated, nodePayload(map[string]any{"tool_call_id": "tc-other"}))
	if err == nil || !strings.Contains(err.Error(), "撞号拒绝") {
		t.Fatalf("tool_call_id 冲突应被拒，got err=%v", err)
	}
}

// TestApplyDeletedMarksClosed：「关闭任务」= 逻辑删除（P2 交付物 ④）——task-deleted → 层视图与
// 后续落库均保留 closed/deleted_at（**不物理删除**、不删库行、不广播）。
func TestApplyDeletedMarksClosed(t *testing.T) {
	l, fake := newLayer(t)
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := l.Apply(subjTaskDeleted, []byte(`{"node_id":"tk-1"}`)); err != nil {
		t.Fatalf("Apply(deleted): %v", err)
	}
	rec := l.Record("tk-1")
	if rec == nil || !rec.Closed || rec.DeletedAt == "" {
		t.Fatalf("层视图应标记 closed: %+v", rec)
	}
	if len(fake.deletes) != 0 {
		t.Fatalf("层不得主动删除库行: %+v", fake.deletes)
	}
	// 关闭后的事件不复活（closed 保留 + 不重复落库）
	if err := l.Apply(subjTaskDone, nodePayload(map[string]any{
		"state": "done", "finished_at": "2026-09-18T00:05:00Z",
	})); err != nil {
		t.Fatalf("Apply(done after closed): %v", err)
	}
	rec = l.Record("tk-1")
	if rec == nil || !rec.Closed || rec.State != "done" {
		t.Fatalf("关闭后终态应收敛且保留 closed: %+v", rec)
	}
	if row := fake.lastRow(); sval(row["closed"]) != "true" || sval(row["deleted_at"]) == "" {
		t.Fatalf("落库行应保留 closed/deleted_at: %+v", row)
	}
}

// TestApplyDeletedCascadeView：关闭父节点 → 层视图内后代一并标记 closed。
func TestApplyDeletedCascadeView(t *testing.T) {
	l, _ := newLayer(t)
	if err := l.Apply(subjTaskStarted, nodePayload(map[string]any{"task_id": "tk-root", "parent_id": ""})); err != nil {
		t.Fatalf("Apply(root): %v", err)
	}
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil { // tk-1 → parent tk-root
		t.Fatalf("Apply(child): %v", err)
	}
	if err := l.Apply(subjTaskDeleted, []byte(`{"node_id":"tk-root"}`)); err != nil {
		t.Fatalf("Apply(deleted): %v", err)
	}
	for _, id := range []string{"tk-root", "tk-1"} {
		if rec := l.Record(id); rec == nil || !rec.Closed {
			t.Fatalf("节点 %s 未随父级联标记 closed: %+v", id, rec)
		}
	}
}

// TestCancelSubtreeLayerAuthority：控制面以层为权威（P2 交付物 ⑤）——层判定可取消集合
// （不可逆终态排除）并对持 exec 句柄的节点回调执行侧。
func TestCancelSubtreeLayerAuthority(t *testing.T) {
	var mu sync.Mutex
	var called []string
	l, _ := newLayerWith(t, Options{OnCancelExec: func(taskID string, exec Exec) {
		mu.Lock()
		called = append(called, taskID+"="+exec.GWTaskID)
		mu.Unlock()
	}})
	// 父（无 exec）→ 子（gw-1）→ 孙（已完成，不可逆终态）
	if err := l.Apply(subjTaskStarted, nodePayload(map[string]any{"task_id": "tk-root", "parent_id": ""})); err != nil {
		t.Fatalf("Apply(root): %v", err)
	}
	if err := l.Apply(subjTaskStarted, nodePayload(map[string]any{"task_id": "tk-1", "parent_id": "tk-root"})); err != nil {
		t.Fatalf("Apply(child): %v", err)
	}
	if err := l.Apply(subjTaskStarted, nodePayload(map[string]any{"task_id": "tk-2", "parent_id": "tk-1"})); err != nil {
		t.Fatalf("Apply(grandchild): %v", err)
	}
	if err := l.Apply(subjTaskDone, nodePayload(map[string]any{
		"task_id": "tk-2", "parent_id": "tk-1", "state": "done", "finished_at": "2026-09-18T00:06:00Z",
	})); err != nil {
		t.Fatalf("Apply(done): %v", err)
	}
	l.BindExec("tk-1", Exec{GWTaskID: "gw-1", InstanceID: "ins-test", Tool: "core_file_read"})

	ids := l.CancelSubtree("ins-test", "tk-root", "top-1")
	if len(ids) != 2 || ids[0] != "tk-1" || ids[1] != "tk-root" {
		t.Fatalf("可取消集合=%v want [tk-1 tk-root]（done 节点应被排除）", ids)
	}
	mu.Lock()
	got := append([]string{}, called...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "tk-1=gw-1" {
		t.Fatalf("执行侧回调=%v want [tk-1=gw-1]", got)
	}
	// 层不可知节点 → nil（调用方按自身视图判定）
	if ids := l.CancelSubtree("ins-test", "tk-unknown", "top-1"); ids != nil {
		t.Fatalf("层不可知应返回 nil: %v", ids)
	}
}

// TestRecoverMarksInterrupted：启动恢复（P2 交付物 ⑥）——库中非终态（pending/running/
// awaiting/detached）一律标 interrupted，终态不动，closed 保留。
func TestRecoverMarksInterrupted(t *testing.T) {
	l, fake := newLayer(t)
	rows := []map[string]any{
		{"task_id": "tk-run", "node_id": "tk-run", "top_session": "top-1", "session_id": "s-1",
			"kind": "tool", "node_type": "tool", "title": "遗留运行", "status": "running",
			"state": "running", "created_at": "2026-09-18T00:00:00Z", "instance_id": "ins-test", "work_dir": "E:/ws"},
		{"task_id": "tk-pend", "node_id": "tk-pend", "top_session": "top-1", "session_id": "s-1",
			"kind": "tool", "node_type": "tool", "title": "遗留待执行", "status": "pending",
			"state": "pending", "created_at": "2026-09-18T00:00:00Z", "instance_id": "ins-test"},
		{"task_id": "tk-done", "node_id": "tk-done", "top_session": "top-1", "session_id": "s-1",
			"kind": "tool", "node_type": "tool", "title": "已完成", "status": "done",
			"state": "done", "created_at": "2026-09-18T00:00:00Z", "instance_id": "ins-test"},
		{"task_id": "tk-closed", "node_id": "tk-closed", "top_session": "top-1", "session_id": "s-1",
			"kind": "tool", "node_type": "tool", "title": "已关闭遗留", "status": "running",
			"state": "running", "created_at": "2026-09-18T00:00:00Z", "instance_id": "ins-test",
			"closed": true, "deleted_at": "2026-09-18T00:10:00Z"},
	}
	for _, row := range rows {
		fake.putRow(row)
	}
	n := l.Recover("ins-test")
	if n != 3 {
		t.Fatalf("恢复标注=%d want 3（running/pending/closed-running）", n)
	}
	for _, id := range []string{"tk-run", "tk-pend", "tk-closed"} {
		if got := sval(fake.row(id)["status"]); got != StateInterrupted {
			t.Fatalf("节点 %s status=%q want interrupted", id, got)
		}
		if sval(fake.row(id)["finished_at"]) == "" {
			t.Fatalf("节点 %s 缺 finished_at", id)
		}
	}
	if got := sval(fake.row("tk-done")["status"]); got != "done" {
		t.Fatalf("终态节点不应被改动: %v", got)
	}
	if v, ok := fake.row("tk-closed")["closed"].(bool); !ok || !v {
		t.Fatalf("closed 标记应保留: %+v", fake.row("tk-closed"))
	}
	// 层内热视图同步
	if rec := l.Record("tk-run"); rec == nil || rec.State != StateInterrupted {
		t.Fatalf("层视图未同步: %+v", rec)
	}
}

// TestApplyUnknownSubject：未知主题 → 返回错误（订阅面之外不处理），不 panic。
func TestApplyUnknownSubject(t *testing.T) {
	l, _ := newLayer(t)
	if err := l.Apply("task-nonexistent", []byte(`{}`)); err == nil {
		t.Fatal("未知主题应返回错误")
	}
	// 空载荷 / 缺 task_id → 错误但不 panic
	if err := l.Apply(subjTaskStarted, []byte(`{}`)); err == nil {
		t.Fatal("缺 task_id 应返回错误")
	}
	if err := l.Apply(subjTaskReport, []byte(`not-json`)); err == nil {
		t.Fatal("非法载荷应返回错误")
	}
}

// TestParentCycleBFSNoHang：ParentID 成环（数据异常）时，后代遍历（descendantsLocked，逻辑删除级联）
// 与子树遍历（CancelSubtree）必须带 visited、不得死循环；环内节点各判一次、不重复。
func TestParentCycleBFSNoHang(t *testing.T) {
	l, _ := newLayer(t)
	// 直接构造三节点环：tk-a → tk-c → tk-b → tk-a（ParentID 互为祖先）。
	seedCycle := func() {
		l.mu.Lock()
		for _, id := range []string{"tk-a", "tk-b", "tk-c"} {
			l.recs[id] = &Record{TaskID: id, TopSession: "top-1", InstanceID: "ins-test", State: StateRunning}
		}
		l.recs["tk-a"].ParentID = "tk-c"
		l.recs["tk-b"].ParentID = "tk-a"
		l.recs["tk-c"].ParentID = "tk-b"
		l.mu.Unlock()
	}

	// ① 逻辑删除（markClosedLocked → descendantsLocked）在环上不得挂起
	seedCycle()
	done := make(chan struct{})
	go func() {
		_ = l.Apply(subjTaskDeleted, []byte(`{"node_id":"tk-a"}`))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ParentID 环导致 descendantsLocked 死循环（Apply(task-deleted) 未返回）")
	}
	for _, id := range []string{"tk-a", "tk-b", "tk-c"} {
		if rec := l.Record(id); rec == nil || !rec.Closed {
			t.Fatalf("环内节点 %s 未随级联标记 closed: %+v", id, rec)
		}
	}

	// ② 取消判定（CancelSubtree）在环上不得挂起；环内节点各判一次（无重复）
	seedCycle()
	got := make(chan []string, 1)
	go func() { got <- l.CancelSubtree("ins-test", "tk-a", "top-1") }()
	select {
	case ids := <-got:
		if len(ids) != 3 {
			t.Fatalf("环内可取消集合=%v want 3 项（visited 去重）", ids)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ParentID 环导致 CancelSubtree 死循环")
	}
}

// TestBuildReportedNodeRejectsSelfParent：完成回报建节点时 parent == task_id 自环被拒绝
// （按顶层节点处理，不建自引用行）。
func TestBuildReportedNodeRejectsSelfParent(t *testing.T) {
	var mu sync.Mutex
	var logs []string
	l, fake := newLayerWith(t, Options{Logf: func(format string, args ...any) {
		mu.Lock()
		logs = append(logs, fmt.Sprintf(format, args...))
		mu.Unlock()
	}})

	if err := l.Apply(subjTaskReport, reportPayload(map[string]any{
		"task_id": "tk-self", "top_session": "top-1", "parent": "tk-self", "tool_call_id": "tc-self",
	})); err != nil {
		t.Fatalf("Apply(report/self-parent): %v", err)
	}
	if rec := l.Record("tk-self"); rec == nil || rec.ParentID != "" {
		t.Fatalf("自环 parent==task_id 应被拒绝（ParentID 置空）: %+v", rec)
	}
	row := fake.row("tk-self")
	if row == nil || sval(row["parent_node_id"]) != "" {
		t.Fatalf("自环 parent 不应重建自引用权威行: %+v", row)
	}
	mu.Lock()
	defer mu.Unlock()
	var hit bool
	for _, s := range logs {
		if strings.Contains(s, "拒绝自环") {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("未记自环拒绝日志: %v", logs)
	}
}
