// 执行态纳入 L1 单测（21-任务层设计方案 §9.3 P3-①）：`Layer.OnExecState` 解析既有节点 → 状态映射
// （只用既有枚举）+ `exec_json` 明细；未命中回落（建行 / 跳过）与 `ExecStateOf`（P3-② 控制面查询）。
// 全部经内存总线 + data 面桩驱动，不依赖真实 DB / GUI。
package task

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// execJSONOf 解出记录的 exec_json（空 / 非法 → 直接失败）。
func execJSONOf(t *testing.T, rec *Record) map[string]any {
	t.Helper()
	if rec == nil || rec.ExecJSON == "" {
		t.Fatalf("exec_json 为空: %+v", rec)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(rec.ExecJSON), &m); err != nil {
		t.Fatalf("exec_json 非法（%v）: %s", err, rec.ExecJSON)
	}
	return m
}

// TestOnExecStateResolvesExistingNode：P3-① —— 执行态命中既有节点（exec 映射）→ **只更新该行**
// （状态映射 + exec_json 明细），**不新增行**（重复行回归）。
func TestOnExecStateResolvesExistingNode(t *testing.T) {
	l, fake := newLayer(t)
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil { // tk-1 / tc-1 / top-1
		t.Fatalf("Apply(started): %v", err)
	}
	l.BindExec("tk-1", Exec{GWTaskID: "tk-0001", InstanceID: "ins-test", Tool: "core_file_read"})

	// started → running（同态）+ exec_json 明细（含 workdir / provider_key）
	l.OnExecState(ExecState{
		GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "top-1", Tool: "core_file_read",
		ToolCallID: "tc-1", WorkDir: "E:/ws", ProviderKey: "self", Phase: ExecPhaseStarted, State: "running",
		Detail: map[string]any{"mode": "manual", "threshold_s": 30},
	})
	rec := l.Record("tk-1")
	if rec == nil || rec.State != StateRunning {
		t.Fatalf("started 执行态状态错: %+v", rec)
	}
	m := execJSONOf(t, rec)
	if m["phase"] != ExecPhaseStarted || m["state"] != "running" ||
		m["workdir"] != "E:/ws" || m["provider_key"] != "self" {
		t.Fatalf("started exec_json 错: %v", m)
	}
	if exec, ok := l.ExecOf("tk-1"); !ok || exec.ProviderKey != "self" {
		t.Fatalf("exec.provider_key 未回填: %+v ok=%v", exec, ok)
	}

	// awaiting → awaiting（层状态按口径映射）+ 明细含 timeout_s / options / mode
	l.OnExecState(ExecState{
		GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "top-1", ToolCallID: "tc-1",
		Phase: ExecPhaseAwaiting, State: "running",
		Detail: map[string]any{"mode": "manual", "timeout_s": 1, "options": []string{"detach", "cancel"}},
	})
	rec = l.Record("tk-1")
	if rec == nil || rec.State != StateAwaiting {
		t.Fatalf("awaiting 应落 awaiting: %+v", rec)
	}
	m = execJSONOf(t, rec)
	detail, _ := m["detail"].(map[string]any)
	if m["phase"] != ExecPhaseAwaiting || detail == nil || detail["mode"] != "manual" ||
		detail["timeout_s"] != float64(1) {
		t.Fatalf("awaiting exec_json 明细错: %v", m)
	}

	// detached{auto} → detached，明细含 trigger=auto / threshold_s
	l.OnExecState(ExecState{
		GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "top-1", ToolCallID: "tc-1",
		Phase: ExecPhaseDetached, State: "running",
		Detail: map[string]any{"trigger": "auto", "threshold_s": 5},
	})
	rec = l.Record("tk-1")
	if rec == nil || rec.State != StateDetached {
		t.Fatalf("detached 应落 detached: %+v", rec)
	}
	m = execJSONOf(t, rec)
	detail, _ = m["detail"].(map[string]any)
	if m["phase"] != ExecPhaseDetached || detail == nil || detail["trigger"] != "auto" ||
		detail["threshold_s"] != float64(5) {
		t.Fatalf("detached exec_json 明细错: %v", m)
	}

	// 库侧同步：状态与 exec_json 均落权威行；**无重复行**
	if n := fake.rowCount(); n != 1 {
		t.Fatalf("权威行数=%d want 1（执行态不得新增行）", n)
	}
	if fake.row("tk-0001") != nil {
		t.Fatal("不得为 gateway 执行 id 建行（重复行）")
	}
	row := fake.row("tk-1")
	if sval(row["state"]) != StateDetached || !strings.Contains(sval(row["exec_json"]), `"phase":"detached"`) {
		t.Fatalf("权威行未同步执行态: %+v", row)
	}

	// 终态 done → done + done_at（完成路径已有 mcp-tasks-report；此处幂等，不重复建行）
	l.OnExecState(ExecState{
		GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "top-1", ToolCallID: "tc-1",
		Phase: ExecPhaseDone, State: "detached",
	})
	rec = l.Record("tk-1")
	if rec == nil || rec.State != StateDone || rec.DoneAt == "" {
		t.Fatalf("done 执行态状态错: %+v", rec)
	}
	if n := fake.rowCount(); n != 1 {
		t.Fatalf("权威行数=%d want 1", n)
	}

	// 不可逆终态：done 之后 error → **非法转移拒绝**（状态保持 done、不落库）
	before := fake.upsertCount()
	l.OnExecState(ExecState{
		GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "top-1", ToolCallID: "tc-1",
		Phase: ExecPhaseError, State: "done", Message: "respawn failed",
	})
	if rec := l.Record("tk-1"); rec == nil || rec.State != StateDone {
		t.Fatalf("不可逆终态被改写: %+v", rec)
	}
	if fake.upsertCount() != before {
		t.Fatalf("非法转移不得落库（upsert %d → %d）", before, fake.upsertCount())
	}

	// error（异常路径合法来源：running → error）→ error + exec_json 短摘要（不含堆栈）
	if err := l.Apply(subjTaskUpdated, nodePayload(map[string]any{
		"task_id": "tk-2", "tool_call_id": "tc-2",
	})); err != nil {
		t.Fatalf("Apply(tk-2): %v", err)
	}
	l.BindExec("tk-2", Exec{GWTaskID: "tk-0002", InstanceID: "ins-test", Tool: "core_file_read"})
	l.OnExecState(ExecState{
		GWTaskID: "tk-0002", InstanceID: "ins-test", TopSession: "top-1", ToolCallID: "tc-2",
		Phase: ExecPhaseError, State: "running", Message: "upstream call failed",
	})
	rec = l.Record("tk-2")
	if rec == nil || rec.State != StateError {
		t.Fatalf("error 执行态状态错: %+v", rec)
	}
	if m := execJSONOf(t, rec); m["message"] != "upstream call failed" || m["phase"] != ExecPhaseError {
		t.Fatalf("error exec_json 缺 message: %v", m)
	}
	if n := fake.rowCount(); n != 2 {
		t.Fatalf("权威行数=%d want 2（执行态不得新增行）", n)
	}
}

// TestOnExecStateBindsMappingByToolCall：执行态未命中 exec 映射但 `tool_call_id` 命中**已登记**节点
// → 更新该节点并**补登记 exec 映射**（doCall 的 started 上报早于 llm 的 setGwTask）→
// 随后的完成回报经该映射命中同一节点（无重复行）。
func TestOnExecStateBindsMappingByToolCall(t *testing.T) {
	l, fake := newLayer(t)
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil {
		t.Fatalf("Apply(started): %v", err)
	}
	l.OnExecState(ExecState{
		GWTaskID: "tk-9001", InstanceID: "ins-test", TopSession: "top-1", Tool: "core_file_read",
		ToolCallID: "tc-1", Phase: ExecPhaseStarted, State: "running",
	})
	if rec := l.Record("tk-9001"); rec != nil {
		t.Fatalf("不得为未登记执行 id 建行: %+v", rec)
	}
	if n := fake.rowCount(); n != 1 {
		t.Fatalf("权威行数=%d want 1", n)
	}
	exec, ok := l.ExecOf("tk-1")
	if !ok || exec.GWTaskID != "tk-9001" {
		t.Fatalf("未补登记 exec 映射: %+v ok=%v", exec, ok)
	}
	if st, ok := l.ExecStateOf("tk-9001"); !ok || st != StateRunning {
		t.Fatalf("ExecStateOf=%q ok=%v want running", st, ok)
	}
	// 后续完成回报（同执行 id）→ 经映射命中同一节点
	if err := l.Apply(subjTaskReport, reportPayload(map[string]any{
		"task_id": "tk-9001", "top_session": "top-1", "state": "cancelled",
	})); err != nil {
		t.Fatalf("Apply(report): %v", err)
	}
	if rec := l.Record("tk-1"); rec == nil || rec.State != StateCancelled {
		t.Fatalf("回报未按映射命中: %+v", rec)
	}
	if n := fake.rowCount(); n != 1 {
		t.Fatalf("权威行数=%d want 1（映射命中不得建行）", n)
	}
	if st, ok := l.ExecStateOf("tk-9001"); !ok || st != StateCancelled {
		t.Fatalf("ExecStateOf=%q ok=%v want cancelled", st, ok)
	}
	if _, ok := l.ExecStateOf("tk-none"); ok {
		t.Fatal("未登记执行 id 应返回 false（回落 gateway 自身判定）")
	}
}

// TestOnExecStateBuildsNodeAndSkips：未命中回落——带 `top_session` 且非 started → 建行（父子回落）；
// `started`（早于调用层登记窗口）/ 无 `top_session` / phase 不可识别 → 跳过并记日志（不建重复行）。
func TestOnExecStateBuildsNodeAndSkips(t *testing.T) {
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

	// ① started 未命中已登记节点 → 跳过建行
	l.OnExecState(ExecState{
		GWTaskID: "tk-1001", InstanceID: "ins-test", TopSession: "top-1", Tool: "script_run",
		ToolCallID: "tc-x", Phase: ExecPhaseStarted, State: "running",
	})
	if fake.row("tk-1001") != nil || fake.rowCount() != 0 {
		t.Fatalf("started 未登记不得建行（行数=%d）", fake.rowCount())
	}
	if !logged("跳过建行") {
		t.Fatalf("started 跳过未记日志: %v", logs)
	}

	// ② 未命中 + 有 top_session + detached → 建行（tool_call_id 无匹配 → 顶层）
	l.OnExecState(ExecState{
		GWTaskID: "tk-1002", InstanceID: "ins-test", TopSession: "top-1", Tool: "script_run",
		ToolCallID: "tc-y", WorkDir: "E:/ws", Phase: ExecPhaseDetached, State: "running",
		Detail: map[string]any{"trigger": "manual"},
	})
	rec := l.Record("tk-1002")
	if rec == nil || rec.State != StateDetached || rec.ParentID != "" || rec.TopSession != "top-1" ||
		rec.WorkDir != "E:/ws" {
		t.Fatalf("未命中建行错: %+v", rec)
	}
	if m := execJSONOf(t, rec); m["phase"] != ExecPhaseDetached {
		t.Fatalf("建行 exec_json 错: %v", m)
	}
	if row := fake.row("tk-1002"); sval(row["workdir"]) != "E:/ws" || sval(row["state"]) != StateDetached {
		t.Fatalf("建行未落 workdir/state: %+v", row)
	}
	if !logged("作为 top_session 下的顶层节点") {
		t.Fatalf("建行未记回落日志: %v", logs)
	}

	// ③ 未命中 + 无 top_session → 跳过（不建行、不报错）
	l.OnExecState(ExecState{
		GWTaskID: "tk-1003", InstanceID: "ins-test", Phase: ExecPhaseDetached, State: "running",
	})
	if fake.row("tk-1003") != nil {
		t.Fatal("无 top_session 不得建行")
	}
	if !logged("无 top_session") {
		t.Fatalf("无 top_session 未记日志: %v", logs)
	}
	if n := fake.rowCount(); n != 1 {
		t.Fatalf("权威行数=%d want 1", n)
	}
}

// TestOnExecStateFrontVisibleStatusUnchanged：P3 **相位门控**（存疑 1 收口）——新增的两类执行态
// detached / awaiting 只落**库内** `state` 与 `exec_json.phase`，**前端可见 status 恒为 running**
// （与 P3 之前同值：前端只认 running 才显示状态图标）。命中既有节点与执行态建行两条路径都要满足；
// 层 / 权威一致性校验（Verify，两侧同用前端可见口径）仍须零差异。
func TestOnExecStateFrontVisibleStatusUnchanged(t *testing.T) {
	l, fake := newLayer(t)
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil { // tk-1 / tc-1 / top-1
		t.Fatalf("Apply(started): %v", err)
	}
	l.BindExec("tk-1", Exec{GWTaskID: "tk-0001", InstanceID: "ins-test", Tool: "core_file_read"})

	// ① 命中既有节点（exec 映射）：awaiting → detached 依次推进
	for _, tc := range []struct {
		phase, wantState string
		detail           map[string]any
	}{
		{ExecPhaseAwaiting, StateAwaiting, map[string]any{"mode": "manual", "timeout_s": 1, "options": []string{"detach", "cancel"}}},
		{ExecPhaseDetached, StateDetached, map[string]any{"trigger": "manual"}},
	} {
		l.OnExecState(ExecState{
			GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "top-1", ToolCallID: "tc-1",
			Phase: tc.phase, State: "running", Detail: tc.detail,
		})
		rec := l.Record("tk-1")
		if rec == nil || rec.State != tc.wantState {
			t.Fatalf("层内权威状态错（phase=%s）: %+v", tc.phase, rec)
		}
		if m := execJSONOf(t, rec); m["phase"] != tc.phase {
			t.Fatalf("exec_json.phase 错（want %s）: %v", tc.phase, m)
		}
		row := fake.row("tk-1")
		if got := sval(row["status"]); got != StateRunning {
			t.Fatalf("前端可见 status=%q want %q（phase=%s：detached/awaiting 不得进入前端可见字段）",
				got, StateRunning, tc.phase)
		}
		if got := sval(row["state"]); got != tc.wantState {
			t.Fatalf("库内 state=%q want %q（phase=%s）", got, tc.wantState, tc.phase)
		}
	}

	// ② 未命中建行路径（tool_call_id 无匹配）同样门控
	l.OnExecState(ExecState{
		GWTaskID: "tk-0002", InstanceID: "ins-test", TopSession: "top-1", Tool: "script_run",
		ToolCallID: "tc-none", Phase: ExecPhaseAwaiting, State: "running",
	})
	if rec := l.Record("tk-0002"); rec == nil || rec.State != StateAwaiting {
		t.Fatalf("未命中建行状态错: %+v", rec)
	}
	if got := sval(fake.row("tk-0002")["status"]); got != StateRunning {
		t.Fatalf("建行前端可见 status=%q want running", got)
	}

	// ③ 层 / 权威一致性校验仍零差异（两侧同用前端可见口径）
	rep, err := l.Verify("ins-test", "top-1")
	if err != nil || !rep.Match() {
		t.Fatalf("Verify 应零差异: err=%v\n%s", err, rep.String())
	}
	if n := fake.rowCount(); n != 2 {
		t.Fatalf("权威行数=%d want 2（执行态不得建行）", n)
	}
}

// ── P3 修正（exec_json 只增不减 / 状态优先级）────────────────────────────────────
//
// 缺口（真机复现）：gateway 执行态写入的 `state=detached` + `exec_json` 会被**随后的 llm 节点/终态
// 快照**（全量 upsert、不带 exec 信息）覆盖为 `pending`/终态并抹空 `exec_json`。以下用例锁定修复口径：
// 优先级 终态(done/error/cancelled) > 执行态(detached/awaiting) > llm 粗粒度(pending/running)。

// execDetailOf 解出 exec_json.detail（缺失 → nil）。
func execDetailOf(t *testing.T, rec *Record) map[string]any {
	t.Helper()
	m := execJSONOf(t, rec)
	detail, _ := m["detail"].(map[string]any)
	return detail
}

// TestLlmSnapshotDoesNotDowngradeDetached：`detached` 之后收到 llm `tasks.updated(state=pending)`
// → **状态仍为 detached**（粗粒度不得降级执行态）、`exec_json` 仍在且字段完整（gw_task_id /
// provider_key / trigger / threshold_s）；其余字段照常合并；重复同载荷幂等（不重复落库）。
func TestLlmSnapshotDoesNotDowngradeDetached(t *testing.T) {
	var mu sync.Mutex
	var logs []string
	l, fake := newLayerWith(t, Options{Logf: func(format string, args ...any) {
		mu.Lock()
		logs = append(logs, fmt.Sprintf(format, args...))
		mu.Unlock()
	}})
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil { // tk-1 / tc-1 / top-1
		t.Fatalf("Apply(started): %v", err)
	}
	l.BindExec("tk-1", Exec{GWTaskID: "tk-0001", InstanceID: "ins-test", Tool: "core_file_read"})
	l.OnExecState(ExecState{ // 转后台 → detached + exec_json
		GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "top-1", Tool: "core_file_read",
		ToolCallID: "tc-1", WorkDir: "E:/ws", ProviderKey: "self", Phase: ExecPhaseDetached,
		State: "running", Detail: map[string]any{"trigger": "auto", "threshold_s": 5},
	})
	if rec := l.Record("tk-1"); rec == nil || rec.State != StateDetached {
		t.Fatalf("前置：detached 未落: %+v", rec)
	}

	// llm 侧转后台快照（state=pending）→ 不得降级、不得抹掉 exec_json
	turnPending := nodePayload(map[string]any{"state": "pending", "simplified": "转后台任务"})
	if err := l.Apply(subjTaskUpdated, turnPending); err != nil {
		t.Fatalf("Apply(updated/pending): %v", err)
	}
	rec := l.Record("tk-1")
	if rec == nil || rec.State != StateDetached {
		t.Fatalf("llm pending 把 detached 降级了: %+v", rec)
	}
	m := execJSONOf(t, rec)
	detail := execDetailOf(t, rec)
	if m["gw_task_id"] != "tk-0001" || m["provider_key"] != "self" || m["phase"] != ExecPhaseDetached {
		t.Fatalf("exec_json 被 llm 快照抹掉/改写: %v", m)
	}
	if detail == nil || detail["trigger"] != "auto" || detail["threshold_s"] != float64(5) {
		t.Fatalf("exec_json.detail 丢失: %v", m)
	}
	if rec.Title != "转后台任务" {
		t.Fatalf("其余字段未照常合并（title=%q）: %+v", rec.Title, rec)
	}
	if row := fake.row("tk-1"); sval(row["state"]) != StateDetached ||
		!strings.Contains(sval(row["exec_json"]), `"phase":"detached"`) ||
		!strings.Contains(sval(row["exec_json"]), `"trigger":"auto"`) {
		t.Fatalf("权威行未保留 detached/exec_json: %+v", row)
	}
	mu.Lock()
	joined := strings.Join(logs, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "状态优先级") {
		t.Fatalf("降级未记日志: %v", logs)
	}
	// 幂等：同载荷再发 → 状态/exec_json 均不变 → 不重复落库
	before := fake.upsertCount()
	if err := l.Apply(subjTaskUpdated, turnPending); err != nil {
		t.Fatalf("Apply(updated/pending#2): %v", err)
	}
	if got := fake.upsertCount(); got != before {
		t.Fatalf("重复快照重复落库（upsert %d → %d）", before, got)
	}
	if rec := l.Record("tk-1"); rec == nil || rec.State != StateDetached {
		t.Fatalf("重复快照后状态错: %+v", rec)
	}
}

// TestTerminalOverridesExecStateKeepsExecJSON：`detached` 之后收到终态 → **终态必须覆盖**
// （llm `tasks.done` 与 gateway `mcp-tasks-report` 两条路径），且 `exec_json` 仍在
// （含 gw_task_id / trigger / provider_key）。
func TestTerminalOverridesExecStateKeepsExecJSON(t *testing.T) {
	l, fake := newLayer(t)

	// ① llm tasks.done
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil { // tk-1
		t.Fatalf("Apply(started/tk-1): %v", err)
	}
	l.BindExec("tk-1", Exec{GWTaskID: "tk-0001", InstanceID: "ins-test", Tool: "core_file_read"})
	l.OnExecState(ExecState{
		GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "top-1", ToolCallID: "tc-1",
		ProviderKey: "self", Phase: ExecPhaseDetached, State: "running",
		Detail: map[string]any{"trigger": "auto", "threshold_s": 5},
	})
	if err := l.Apply(subjTaskDone, nodePayload(map[string]any{
		"state": "done", "finished_at": "2026-09-18T00:10:00Z", "result_summary": "完成",
	})); err != nil {
		t.Fatalf("Apply(done/tk-1): %v", err)
	}
	rec := l.Record("tk-1")
	if rec == nil || rec.State != StateDone || rec.DoneAt != "2026-09-18T00:10:00Z" {
		t.Fatalf("终态未覆盖 detached: %+v", rec)
	}
	m := execJSONOf(t, rec)
	detail := execDetailOf(t, rec)
	if m["gw_task_id"] != "tk-0001" || m["provider_key"] != "self" ||
		detail == nil || detail["trigger"] != "auto" {
		t.Fatalf("终态覆盖后 exec_json 丢失: %v", m)
	}
	if row := fake.row("tk-1"); sval(row["state"]) != StateDone || sval(row["exec_json"]) == "" {
		t.Fatalf("权威行终态/exec_json 错: %+v", row)
	}

	// ② gateway mcp-tasks-report（真实终态通道）
	if err := l.Apply(subjTaskStarted, nodePayload(map[string]any{
		"task_id": "tk-2", "tool_call_id": "tc-2",
	})); err != nil {
		t.Fatalf("Apply(started/tk-2): %v", err)
	}
	l.BindExec("tk-2", Exec{GWTaskID: "tk-0002", InstanceID: "ins-test", Tool: "core_file_read"})
	l.OnExecState(ExecState{
		GWTaskID: "tk-0002", InstanceID: "ins-test", TopSession: "top-1", ToolCallID: "tc-2",
		ProviderKey: "self", Phase: ExecPhaseDetached, State: "running",
		Detail: map[string]any{"trigger": "manual"},
	})
	if err := l.Apply(subjTaskReport, reportPayload(map[string]any{
		"task_id": "tk-0002", "tool_call_id": "tc-2", "top_session": "top-1", "state": "done",
	})); err != nil {
		t.Fatalf("Apply(report/tk-2): %v", err)
	}
	rec = l.Record("tk-2")
	if rec == nil || rec.State != StateDone {
		t.Fatalf("回报未推进终态: %+v", rec)
	}
	if m := execJSONOf(t, rec); m["gw_task_id"] != "tk-0002" || m["provider_key"] != "self" {
		t.Fatalf("终态回报后 exec_json 丢失: %v", m)
	}
	if n := fake.rowCount(); n != 2 {
		t.Fatalf("权威行数=%d want 2（不得建重复行）", n)
	}
}

// TestAwaitingNotDowngradedByRunningSnapshot：`awaiting` 之后收到 llm `running` 快照
// → 仍为 `awaiting`（执行态不被粗粒度降级），exec_json 保留。
func TestAwaitingNotDowngradedByRunningSnapshot(t *testing.T) {
	l, fake := newLayer(t)
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil {
		t.Fatalf("Apply(started): %v", err)
	}
	l.BindExec("tk-1", Exec{GWTaskID: "tk-0001", InstanceID: "ins-test", Tool: "core_file_read"})
	l.OnExecState(ExecState{
		GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "top-1", ToolCallID: "tc-1",
		Phase: ExecPhaseAwaiting, State: "running",
		Detail: map[string]any{"mode": "manual", "timeout_s": 60, "options": []string{"detach", "cancel"}},
	})
	if err := l.Apply(subjTaskUpdated, nodePayload(map[string]any{"state": "running"})); err != nil {
		t.Fatalf("Apply(updated/running): %v", err)
	}
	rec := l.Record("tk-1")
	if rec == nil || rec.State != StateAwaiting {
		t.Fatalf("running 快照把 awaiting 降级了: %+v", rec)
	}
	if m := execJSONOf(t, rec); m["phase"] != ExecPhaseAwaiting {
		t.Fatalf("exec_json 被改写: %v", m)
	}
	if row := fake.row("tk-1"); sval(row["state"]) != StateAwaiting {
		t.Fatalf("权威行 state=%q want awaiting", sval(row["state"]))
	}
}

// TestExecJSONDeepMergeAcrossExecEvents：gateway 执行态之间的 `exec_json` **深合并**（只增不减）——
// started{mode,threshold_s,provider_key} → detached{trigger,threshold_s}（不带 provider_key）→
// awaiting{timeout_s,options}：三段的字段都在（不丢 gw_task_id / provider_key / detail 明细）。
func TestExecJSONDeepMergeAcrossExecEvents(t *testing.T) {
	l, fake := newLayer(t)
	if err := l.Apply(subjTaskStarted, nodePayload(nil)); err != nil { // tk-1 / tc-1 / top-1
		t.Fatalf("Apply(started): %v", err)
	}
	l.BindExec("tk-1", Exec{GWTaskID: "tk-0001", InstanceID: "ins-test", Tool: "core_file_read"})

	// ① started（带 provider_key + mode/threshold_s）
	l.OnExecState(ExecState{
		GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "top-1", ToolCallID: "tc-1",
		WorkDir: "E:/ws", ProviderKey: "self", Phase: ExecPhaseStarted, State: "running",
		Detail: map[string]any{"mode": "auto", "threshold_s": 30},
	})
	m := execJSONOf(t, l.Record("tk-1"))
	if m["phase"] != ExecPhaseStarted || m["provider_key"] != "self" {
		t.Fatalf("started exec_json 错: %v", m)
	}

	// ② detached（**不带** provider_key）→ 深合并，不丢 provider_key / 前段明细
	l.OnExecState(ExecState{
		GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "top-1", ToolCallID: "tc-1",
		Phase: ExecPhaseDetached, State: "running",
		Detail: map[string]any{"trigger": "auto", "threshold_s": 5},
	})
	rec := l.Record("tk-1")
	if rec == nil || rec.State != StateDetached {
		t.Fatalf("detached 未落: %+v", rec)
	}
	m = execJSONOf(t, rec)
	detail := execDetailOf(t, rec)
	if m["gw_task_id"] != "tk-0001" || m["provider_key"] != "self" || m["phase"] != ExecPhaseDetached {
		t.Fatalf("detached 更新丢了 gw_task_id/provider_key: %v", m)
	}
	if detail == nil || detail["trigger"] != "auto" || detail["threshold_s"] != float64(5) {
		t.Fatalf("detached detail 合并错: %v", m)
	}

	// ③ awaiting（options/timeout_s）→ 两段信息都在（trigger/threshold_s + options/timeout_s）
	l.OnExecState(ExecState{
		GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "top-1", ToolCallID: "tc-1",
		Phase: ExecPhaseAwaiting, State: "running",
		Detail: map[string]any{"timeout_s": 60, "options": []string{"detach", "cancel"}},
	})
	rec = l.Record("tk-1")
	if rec == nil || rec.State != StateAwaiting {
		t.Fatalf("awaiting 未落: %+v", rec)
	}
	m = execJSONOf(t, rec)
	detail = execDetailOf(t, rec)
	if m["phase"] != ExecPhaseAwaiting || m["provider_key"] != "self" || m["workdir"] != "E:/ws" {
		t.Fatalf("awaiting 更新丢字段: %v", m)
	}
	if detail == nil || detail["trigger"] != "auto" || detail["threshold_s"] != float64(5) ||
		detail["timeout_s"] != float64(60) {
		t.Fatalf("exec_json 深合并丢 detail: %v", m)
	}
	if opts, ok := detail["options"].([]any); !ok || len(opts) != 2 {
		t.Fatalf("exec_json 深合并丢 options: %v", detail)
	}
	// 权威行同步同一份合并结果；无重复行
	row := fake.row("tk-1")
	if sval(row["state"]) != StateAwaiting || !strings.Contains(sval(row["exec_json"]), `"provider_key":"self"`) ||
		!strings.Contains(sval(row["exec_json"]), `"trigger":"auto"`) {
		t.Fatalf("权威行未同步合并后的 exec_json: %+v", row)
	}
	if n := fake.rowCount(); n != 1 {
		t.Fatalf("权威行数=%d want 1（执行态不得建行）", n)
	}
}
