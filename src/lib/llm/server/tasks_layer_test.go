// 任务层单写者集成测试（21 任务层设计方案 §9.2 P2 验收）：bus 级——跑一轮真实同步工具调用，
// 断言 ①权威表 `tasktree` 出现该节点、`workdir` 非空（隔离键落库）；
// ②层与库一致（Verify 零差异）；③关闭任务 = **逻辑删除**（列表消失、库行仍在且 closed 可见）。
// 另：llm 侧不再直接写库 —— 由本文件「层是唯一写方」的间接证据 + 代码位置保证
// （`chonkpilot-llm/server/tasks.go` 已无 `data-tasktree-upsert` 调用，见 grep）。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
	tasklayer "github.com/chonkpilot/chonkpilot-task"
)

// TestTaskLayerSingleWriter：层为唯一权威（tasktree 行 + workdir + Verify + 逻辑删除）。
func TestTaskLayerSingleWriter(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-layer", "t-layer")

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-layer", "turn": "t-layer",
		"type": "text-user", "content": "sync tool please",
	}))
	if lastComplete(collectTurn(t, s.bus, "t-layer", 15*time.Second)) == nil {
		t.Fatal("轮次未完成")
	}

	// ① 权威表（默认路径 = 不传 shadow）：任务层写入的节点 + workdir 非空
	res, err := dataRequest(s.bus, "data-tasktree-list", map[string]any{
		"instance_id": "ins-test",
		"data":        map[string]any{"top_session": "s-layer"},
	})
	if err != nil {
		t.Fatalf("读权威表: %v", err)
	}
	nodes, _ := res["nodes"].([]any)
	var toolNode map[string]any
	for _, it := range nodes {
		m, _ := it.(map[string]any)
		if m != nil && str(m["tool_call_id"]) == "tc-sync" {
			toolNode = m
		}
	}
	if toolNode == nil {
		t.Fatalf("权威表缺同步工具节点: %+v", nodes)
	}
	if str(toolNode["status"]) != TaskStateDone || str(toolNode["kind"]) != TaskKindTool ||
		str(toolNode["top_session"]) != "s-layer" || str(toolNode["session_id"]) != "s-layer" ||
		str(toolNode["title"]) != "sync" {
		t.Fatalf("权威节点字段错: %+v", toolNode)
	}
	if got := str(toolNode["workdir"]); got == "" || got != testWorkDir {
		t.Fatalf("权威行 workdir 应 = instance 绑定 work_dir %q，实得 %q", testWorkDir, got)
	}

	// ② 层与库一致（Verify 零差异）
	rep, err := s.taskLayer.Verify("ins-test", "s-layer")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.Match() {
		t.Fatalf("层与库应零差异（P2 单写者）:\n%s", rep.String())
	}

	// ③ 关闭任务 = 逻辑删除：视图消失、库行仍在（closed 可见，历史可查）
	nodeID := str(toolNode["node_id"])
	if nodeID == "" {
		t.Fatalf("节点缺 node_id: %+v", toolNode)
	}
	if _, err := dataRequest(s.bus, "data-tasktree-delete", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"node_id": nodeID},
	}); err != nil {
		t.Fatalf("关闭任务: %v", err)
	}
	res, err = dataRequest(s.bus, "data-tasktree-list", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"top_session": "s-layer"},
	})
	if err != nil {
		t.Fatalf("关闭后读视图: %v", err)
	}
	if nodes, _ := res["nodes"].([]any); len(nodes) != 0 {
		t.Fatalf("关闭后视图应不再显示该节点: %+v", nodes)
	}
	res, err = dataRequest(s.bus, "data-tasktree-list", map[string]any{
		"instance_id": "ins-test",
		"data":        map[string]any{"top_session": "s-layer", "include_closed": true},
	})
	if err != nil {
		t.Fatalf("读历史（include_closed）: %v", err)
	}
	nodes, _ = res["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("历史应保留该节点（含 closed 标记）: %+v", nodes)
	}
	closedNode, _ := nodes[0].(map[string]any)
	if ok, _ := closedNode["closed"].(bool); !ok {
		t.Fatalf("库行 closed 标记缺失: %+v", closedNode)
	}
	if str(closedNode["deleted_at"]) == "" {
		t.Fatalf("库行 deleted_at 缺失: %+v", closedNode)
	}
}

// TestTaskLayerCancelAuthority：控制面以层为权威 + 层回调执行侧（P2 交付物 ⑤）：
// task-stop 经层判定可取消集合 → 层回调经**进程内 sink** 下发执行取消（层持 exec 句柄）。
func TestTaskLayerCancelAuthority(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-layer-c", "t-layer-c") // 注册 instance（层可定位 prjusr 库）
	te := watchTasks(t, s.bus)
	resetExecControl()

	node, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-layer-c", TurnID: "t-layer-c",
		InstanceID: "ins-test", TopSession: "s-layer-c", Name: "inflight",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	s.tasks.setGwTask(node.TaskID, "gw-layer-1") // 同回登记执行句柄到层
	s.tasks.setState(node.TaskID, TaskStatePending)
	te.wait(t, "started", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		return len(te.started) == 1
	})

	// 层持执行句柄（BindExec）
	if exec, ok := s.taskLayer.ExecOf(node.TaskID); !ok || exec.GWTaskID != "gw-layer-1" {
		t.Fatalf("层未持有 exec 句柄: %+v ok=%v", exec, ok)
	}

	f := s.bus.Emit(context.Background(), "task-stop", jb(map[string]any{
		"req_id": "r-layer-stop", "task_id": node.TaskID,
	}))
	v := f.Wait()
	if res, _ := v.Result.(map[string]any); res != nil {
		if ok, _ := res["cancelled"].(bool); !ok {
			t.Fatalf("task-stop ack: %+v", res)
		}
	} else {
		t.Fatalf("task-stop 无 ack: %v", v.Err())
	}
	te.wait(t, "cancelled", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		d := te.done[node.TaskID]
		return d != nil && d["state"] == TaskStateCancelled
	})
	// 层 → 执行侧回调落到进程内 sink（CancelExec）
	got := execCancelledRefs()
	if len(got) != 1 || got[0] != "gw-layer-1" {
		t.Fatalf("exec cancels=%v want [gw-layer-1]（层回调）", got)
	}
	// 终态后层判定为不可取消 → 二次 task-stop 不重复广播
	if st, ok := s.taskLayer.State(node.TaskID); !ok || st != TaskStateCancelled {
		t.Fatalf("层状态应为 cancelled: %q ok=%v", st, ok)
	}
}

// TestTaskStopDuringAwaiting：I-106 回归 —— **待裁决（awaiting）中取消** → 层权威行终态 `cancelled`
// （不残留 `awaiting`）。症状路径（41 I-106，2026-09-19）：manual 工具到超时点 → 层 `state=awaiting`
// （exec 句柄已绑）→ 前端任务区裁决条「取消」发 `task-stop{task_id}` → 层判定可取消（awaiting 非
// 不可逆终态）→ 层回调经进程内 sink `CancelExec(gw_task_id)` 真打断 + 广播 `tasks.done{cancelled}`
// → 层/库行终态 `cancelled`，`data-tasktree-tasks` 不再输出 `awaiting` 明细（裁决条消隐）。
//
// 定性依据（I-106）：本用例锁定「取消在 awaiting 下必须生效」的产品契约；L4 用例 G 的偶发失败
// 已定为**测试资产时序**（读快照与 task-stop 请求并发），非本契约失效（见 42 §2 (120)）。
func TestTaskStopDuringAwaiting(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-aw", "t-aw")
	te := watchTasks(t, s.bus)
	resetExecControl()

	node, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-aw", TurnID: "t-aw",
		InstanceID: "ins-test", TopSession: "s-aw", Name: "转后台", ToolCallID: "tc-aw",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	te.wait(t, "started", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		return len(te.started) == 1
	})

	// gateway 执行态上报（真实装配 = taskExecSink → Layer.OnExecState）：started → awaiting（manual 超时点）。
	sink := &taskExecSink{layer: s.taskLayer}
	sink.OnExecState(mcpgateway.ExecState{
		GWTaskID: "tk-aw-1", InstanceID: "ins-test", TopSession: "s-aw", Tool: "core_file_read",
		ToolCallID: "tc-aw", Phase: mcpgateway.ExecPhaseStarted, State: "running",
	})
	sink.OnExecState(mcpgateway.ExecState{
		GWTaskID: "tk-aw-1", InstanceID: "ins-test", TopSession: "s-aw", ToolCallID: "tc-aw",
		Phase: mcpgateway.ExecPhaseAwaiting, State: "running",
		Detail: map[string]any{"mode": "manual", "timeout_s": 1, "options": []string{"detach", "cancel"}},
	})

	// 前置：层权威行 = awaiting，且前端任务区（data-tasktree-tasks）输出 awaiting 明细。
	row := waitTaskRow(t, s, "s-aw", node.TaskID, func(row map[string]any) bool {
		return str(row["state"]) == tasklayer.StateAwaiting
	})
	if str(row["state"]) != tasklayer.StateAwaiting {
		t.Fatalf("前置：层行应为 awaiting: %+v", row)
	}
	if st, ok := s.taskLayer.State(node.TaskID); !ok || st != tasklayer.StateAwaiting {
		t.Fatalf("前置：层热视图应为 awaiting: %q ok=%v", st, ok)
	}
	if aw := tasksViewItem(t, s, "s-aw", node.TaskID)["awaiting"]; aw == nil {
		t.Fatalf("前置：data-tasktree-tasks 应输出 awaiting 明细: %+v",
			tasksViewItem(t, s, "s-aw", node.TaskID))
	}

	// 取消（前端裁决条「取消」同一入口）：task-stop{task_id}。
	v := s.bus.Emit(context.Background(), "task-stop", jb(map[string]any{
		"req_id": "r-aw-stop", "instance_id": "ins-test", "task_id": node.TaskID,
	})).Wait()
	res, _ := v.Result.(map[string]any)
	if v.Err() != nil || res == nil {
		t.Fatalf("task-stop(awaiting) 失败: err=%v res=%+v", v.Err(), v.Result)
	}
	if ok, _ := res["cancelled"].(bool); !ok || str(res["task_id"]) != node.TaskID {
		t.Fatalf("task-stop ack=%+v want cancelled=true task_id=%s", res, node.TaskID)
	}
	// 真打断：层 → 进程内 sink CancelExec(gw_task_id)
	if got := execCancelledRefs(); len(got) != 1 || got[0] != "tk-aw-1" {
		t.Fatalf("sink.CancelExec=%v want [tk-aw-1]", got)
	}
	// 终态收敛：层权威行 cancelled（不残留 awaiting）+ 广播 tasks.done{cancelled}
	row = waitTaskRow(t, s, "s-aw", node.TaskID, func(row map[string]any) bool {
		return str(row["state"]) == tasklayer.StateCancelled
	})
	if str(row["state"]) != tasklayer.StateCancelled {
		t.Fatalf("取消后层行应为 cancelled: %+v", row)
	}
	te.wait(t, "cancelled", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		d := te.done[node.TaskID]
		return d != nil && d["state"] == TaskStateCancelled
	})
	// 前端任务区：awaiting 明细随终态消失（裁决条不再渲染）
	if item := tasksViewItem(t, s, "s-aw", node.TaskID); item["awaiting"] != nil {
		t.Fatalf("取消后 data-tasktree-tasks 不应再输出 awaiting: %+v", item)
	}
}

// tasksViewItem 读 `data-tasktree-tasks`（前端任务区数据源）该 task_id 的条目（缺 → 空 map）。
func tasksViewItem(t *testing.T, s *Server, topSession, taskID string) map[string]any {
	t.Helper()
	res, err := dataRequest(s.bus, "data-tasktree-tasks", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"top_session": topSession},
	})
	if err != nil {
		t.Fatalf("读任务快照: %v", err)
	}
	list, _ := res["list"].([]any)
	for _, it := range list {
		m, _ := it.(map[string]any)
		if str(m["task_id"]) == taskID {
			return m
		}
	}
	return map[string]any{}
}

// TestTaskLayerRecoverOnFirstStart：启动恢复并入层（P2 交付物 ⑥）——库中遗留非终态节点在
// 首个 llm-start 时被标 interrupted（不自动重跑），且不再由 llm 侧单独实现。
func TestTaskLayerRecoverOnFirstStart(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-rec", "t-rec") // 注册 instance（首个 llm-start 触发清理）

	// 造一条遗留 running 行（模拟上次进程崩溃前执行中）
	inst := "ins-test"
	if _, err := dataRequest(s.bus, "data-tasktree-upsert", map[string]any{
		"instance_id": inst,
		"data": map[string]any{
			"task_id": "tk-stale", "top_session": "s-stale", "session_id": "s-stale",
			"kind": "tool", "title": "遗留任务", "status": TaskStateRunning,
			"created_at": "2026-09-18T00:00:00Z", "instance_id": inst,
		},
	}); err != nil {
		t.Fatalf("造遗留行: %v", err)
	}
	// 新 instance → 再触发一次清理（recoverStaleTasktree 只在首个 llm-start / 未清理过的实例执行）
	n := s.tasks.recoverStaleTasktree(inst)
	if n < 1 {
		t.Fatalf("启动恢复标注数=%d want ≥1", n)
	}
	res, err := dataRequest(s.bus, "data-tasktree-list", map[string]any{
		"instance_id": inst, "data": map[string]any{"top_session": "s-stale"},
	})
	if err != nil {
		t.Fatalf("读库: %v", err)
	}
	nodes, _ := res["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("遗留节点=%+v", nodes)
	}
	row, _ := nodes[0].(map[string]any)
	if str(row["status"]) != TaskStateInterrupted {
		t.Fatalf("遗留非终态应标 interrupted: %+v", row)
	}
	if str(row["finished_at"]) == "" {
		t.Fatalf("interrupted 应带 finished_at: %+v", row)
	}
}

// verifyCall 发 `task-verify` 方法面（I-87）并取回层宿主应答。
func verifyCall(t *testing.T, s *Server, topSession string) (*tasklayer.VerifyResult, error) {
	t.Helper()
	v := s.bus.Emit(context.Background(), "task-verify", jb(map[string]any{
		"instance_id": "ins-test", "top_session": topSession,
	})).Wait()
	if err := v.Err(); err != nil {
		return nil, err
	}
	res, ok := v.Result.(*tasklayer.VerifyResult)
	if !ok {
		t.Fatalf("task-verify 应答类型错: %T (%+v)", v.Result, v.Result)
	}
	return res, nil
}

// runSyncToolTurn 跑一轮真实同步工具调用（mock LLM「sync tool please」→ tc-sync）并等轮次完成；
// 返回权威表 `tasktree` 中该工具节点行（tool_call_id=tc-sync）。
func runSyncToolTurn(t *testing.T, s *Server, session, turn string) map[string]any {
	t.Helper()
	startTurn(t, s, session, turn)
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "turn": turn,
		"type": "text-user", "content": "sync tool please",
	}))
	if lastComplete(collectTurn(t, s.bus, turn, 15*time.Second)) == nil {
		t.Fatal("轮次未完成")
	}
	res, err := dataRequest(s.bus, "data-tasktree-list", map[string]any{
		"instance_id": "ins-test",
		"data":        map[string]any{"top_session": session},
	})
	if err != nil {
		t.Fatalf("读权威表: %v", err)
	}
	nodes, _ := res["nodes"].([]any)
	for _, it := range nodes {
		if m, _ := it.(map[string]any); m != nil && str(m["tool_call_id"]) == "tc-sync" {
			return m
		}
	}
	t.Fatalf("权威表缺同步工具节点: %+v", nodes)
	return nil
}

// TestToolCallContextCarriesTreeTopSession：I-90 —— llm 发起 `mcp-tools-call` 时随调用上下文下发
// **任务树归属**（`top_session` = 主会话；`parent` = 该调用的 llm 侧节点 task_id），gateway 据此
// 在完成回报中回传 → 任务层可在「未登记」时独立建节点。
func TestToolCallContextCarriesTreeTopSession(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	fakeGWCallCtxMu.Lock()
	fakeGWCallCtx = map[string][2]string{}
	fakeGWCallCtxMu.Unlock()

	runSyncToolTurn(t, s, "s-ctx", "t-ctx")

	fakeGWCallCtxMu.Lock()
	got, ok := fakeGWCallCtx["tc-sync"]
	fakeGWCallCtxMu.Unlock()
	if !ok {
		t.Fatal("fake gateway 未收到 tools/call（tc-sync）")
	}
	if got[0] != "s-ctx" {
		t.Fatalf("tools/call 上下文 top_session=%q want s-ctx", got[0])
	}
	if got[1] == "" || !strings.HasPrefix(got[1], "tk-") {
		t.Fatalf("tools/call 上下文 parent=%q want 该调用的 llm 侧节点 task_id", got[1])
	}
	// parent 必须指向**已登记**的本次调用节点（层内可视：tool_call_id / top_session 同源）
	rec := s.taskLayer.Record(got[1])
	if rec == nil || rec.ToolCallID != "tc-sync" || rec.TopSession != "s-ctx" {
		t.Fatalf("parent 未指向本次调用的已登记节点: %+v", rec)
	}
}

// TestTaskVerifyMethod：I-87 —— `task-verify` 方法面（层宿主注册；只读）：
// ① 零差异 → match=true 且 diffs=[]（rows/cached_rows 非零）；
// ② 构造一处不一致（权威行 status 被外部改写，不经层事件）→ match=false 且 diffs 非空；
// ③ 缺 top_session → 协议错误（v.Err() 非空，不 panic）。
func TestTaskVerifyMethod(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	node := runSyncToolTurn(t, s, "s-verify", "t-verify")

	// ① 零差异
	res, err := verifyCall(t, s, "s-verify")
	if err != nil {
		t.Fatalf("task-verify: %v", err)
	}
	if !res.Match || len(res.Diffs) != 0 || res.Rows < 1 || res.CachedRows < 1 || res.Error != "" {
		t.Fatalf("零差异应答错: %+v", res)
	}

	// ② 一处不一致：把权威行整行回写并把 status 改为 error（层视图不变 → 字段级差异）
	row := map[string]any{}
	for k, v := range node {
		row[k] = v
	}
	row["status"] = "error"
	if _, err := dataRequest(s.bus, "data-tasktree-upsert", map[string]any{
		"instance_id": "ins-test", "data": row,
	}); err != nil {
		t.Fatalf("改写权威行: %v", err)
	}
	res, err = verifyCall(t, s, "s-verify")
	if err != nil {
		t.Fatalf("task-verify（不一致）: %v", err)
	}
	if res.Match || len(res.Diffs) == 0 || res.Error != "" {
		t.Fatalf("应报告差异: %+v", res)
	}
	found := false
	for _, d := range res.Diffs {
		if d.Field == "status" && d.Layer == TaskStateDone && d.Authoritative == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺 status 字段差异: %+v", res.Diffs)
	}

	// ③ 缺 top_session → 协议错误
	v := s.bus.Emit(context.Background(), "task-verify", jb(map[string]any{"instance_id": "ins-test"})).Wait()
	if v.Err() == nil {
		t.Fatalf("缺 top_session 应回协议错误: %+v", v.Result)
	}
}

// TestTaskExecSinkExecDetailReadable：P3-①/② llm 侧接线 —— 装配注入的 `taskExecSink`
// （= gateway `Params.ExecSink` 的实现）把执行态落层权威表（exec_json 含 detached/awaiting 明细）：
//
//	① 执行态命中既有节点（tool_call_id 补登记 exec 映射）→ 状态按既有枚举映射、**无重复行**；
//	② `data-tasktree-tasks`（前端任务区）可读到 `state` / `exec_json` 明细（此前该项为空缺）；
//	③ 控制面查询 `ExecStateOf(gw_task_id)` 以层为准（P3-②）。
func TestTaskExecSinkExecDetailReadable(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-p3", "t-p3")
	te := watchTasks(t, s.bus)

	node, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-p3", TurnID: "t-p3",
		InstanceID: "ins-test", TopSession: "s-p3", Name: "inflight", ToolCallID: "tc-p3",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	te.wait(t, "started", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		return len(te.started) == 1
	})

	sink := &taskExecSink{layer: s.taskLayer} // 与装配注入 gateway 的同一适配器
	sink.OnExecState(mcpgateway.ExecState{
		GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "s-p3", Tool: "core_file_read",
		ToolCallID: "tc-p3", Phase: mcpgateway.ExecPhaseStarted, State: "running",
	})
	sink.OnExecState(mcpgateway.ExecState{
		GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "s-p3", ToolCallID: "tc-p3",
		Phase: mcpgateway.ExecPhaseAwaiting, State: "running",
		Detail: map[string]any{"mode": "manual", "timeout_s": 1, "options": []string{"detach", "cancel"}},
	})
	sink.OnExecState(mcpgateway.ExecState{
		GWTaskID: "tk-0001", InstanceID: "ins-test", TopSession: "s-p3", ToolCallID: "tc-p3",
		Phase: mcpgateway.ExecPhaseDetached, State: "running",
		Detail: map[string]any{"mode": "manual"},
	})

	// ③ 层权威状态（P3-②；awaiting → awaiting、detached → detached，均为层既有枚举）
	if st, ok := sink.ExecStateOf("tk-0001"); !ok || st != "detached" {
		t.Fatalf("ExecStateOf = %q ok=%v want detached", st, ok)
	}
	if st, ok := s.taskLayer.State(node.TaskID); !ok || st != "detached" {
		t.Fatalf("层状态应为 detached: %q ok=%v", st, ok)
	}

	// ② 前端任务区读（data-tasktree-tasks）→ 单行 + exec_json 明细
	res, err := dataRequest(s.bus, "data-tasktree-tasks", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"top_session": "s-p3"},
	})
	if err != nil {
		t.Fatalf("读任务列表: %v", err)
	}
	list, _ := res["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("任务行数=%d want 1（执行态不得新增行）: %+v", len(list), list)
	}
	item, _ := list[0].(map[string]any)
	if str(item["task_id"]) != node.TaskID || str(item["state"]) != "detached" {
		t.Fatalf("任务项字段错: %+v", item)
	}
	var exec map[string]any
	if err := json.Unmarshal([]byte(str(item["exec_json"])), &exec); err != nil {
		t.Fatalf("exec_json 非法: %v（%q）", err, str(item["exec_json"]))
	}
	if exec["phase"] != mcpgateway.ExecPhaseDetached || exec["gw_task_id"] != "tk-0001" {
		t.Fatalf("exec_json 明细错: %+v", exec)
	}
	if detail, _ := exec["detail"].(map[string]any); detail == nil || detail["mode"] != "manual" {
		t.Fatalf("exec_json.detail 错: %+v", exec)
	}
}

// TestTaskExecPhaseFrontVisibleUnchanged：P3 **相位门控**（存疑 1 收口）——detached / awaiting 两类
// 新增执行态不得改变**前端可见状态**，经前端可见路径（`data-tasktree-tasks` / `data-tasktree-list`
// 的输出）验证：
//
//	① 两个路径读到的 `status` **恒为 running**（与 P3 之前同值；前端只认 running 显示 spinner）；
//	② 真实相位只落**库内** `state` 与 `exec_json.phase`（不进前端可见字段）；
//	③ 行数恒为 1（执行态只推进、不建行）。
func TestTaskExecPhaseFrontVisibleUnchanged(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-p3v", "t-p3v")
	te := watchTasks(t, s.bus)

	node, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-p3v", TurnID: "t-p3v",
		InstanceID: "ins-test", TopSession: "s-p3v", Name: "inflight", ToolCallID: "tc-p3v",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	te.wait(t, "started", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		return len(te.started) == 1
	})
	sink := &taskExecSink{layer: s.taskLayer}

	for _, step := range []struct {
		phase, wantState string
		detail           map[string]any
	}{
		{mcpgateway.ExecPhaseAwaiting, "awaiting",
			map[string]any{"mode": "manual", "timeout_s": 1, "options": []string{"detach", "cancel"}}},
		{mcpgateway.ExecPhaseDetached, "detached", map[string]any{"trigger": "manual"}},
	} {
		sink.OnExecState(mcpgateway.ExecState{
			GWTaskID: "tk-p3v", InstanceID: "ins-test", TopSession: "s-p3v", Tool: "core_file_read",
			ToolCallID: "tc-p3v", Phase: step.phase, State: "running", Detail: step.detail,
		})

		// ① 前端任务区（data-tasktree-tasks）：status 仍 running；相位在 state / exec_json
		res, err := dataRequest(s.bus, "data-tasktree-tasks", map[string]any{
			"instance_id": "ins-test", "data": map[string]any{"top_session": "s-p3v"},
		})
		if err != nil {
			t.Fatalf("读任务列表: %v", err)
		}
		list, _ := res["list"].([]any)
		if len(list) != 1 {
			t.Fatalf("任务行数=%d want 1（执行态不得建行）: %+v", len(list), list)
		}
		item, _ := list[0].(map[string]any)
		if str(item["task_id"]) != node.TaskID || str(item["status"]) != "running" {
			t.Fatalf("前端可见 status 应为 running（与 P3 前同值；phase=%s）: %+v", step.phase, item)
		}
		if str(item["state"]) != step.wantState {
			t.Fatalf("库内 state=%q want %q（phase=%s）", str(item["state"]), step.wantState, step.phase)
		}
		var exec map[string]any
		if err := json.Unmarshal([]byte(str(item["exec_json"])), &exec); err != nil {
			t.Fatalf("exec_json 非法: %v（%q）", err, str(item["exec_json"]))
		}
		if exec["phase"] != step.phase {
			t.Fatalf("exec_json.phase=%v want %s", exec["phase"], step.phase)
		}

		// ② 前端任务树（data-tasktree-list）：同一行、同一口径
		res, err = dataRequest(s.bus, "data-tasktree-list", map[string]any{
			"instance_id": "ins-test", "data": map[string]any{"top_session": "s-p3v"},
		})
		if err != nil {
			t.Fatalf("读任务树: %v", err)
		}
		nodes, _ := res["nodes"].([]any)
		var row map[string]any
		for _, it := range nodes {
			if m, _ := it.(map[string]any); m != nil && str(m["task_id"]) == node.TaskID {
				row = m
			}
		}
		if row == nil {
			t.Fatalf("任务树缺该节点: %+v", nodes)
		}
		if str(row["status"]) != "running" || str(row["state"]) != step.wantState {
			t.Fatalf("树路径 status/state 错（phase=%s；status 须为前端可见口径）: %+v", step.phase, row)
		}
	}
}

// waitTaskRow 轮询权威表 `tasktree` 该 task_id 行直到 cond 成立（层订阅投递可能异步；超时 → 打印末次行并失败）。
func waitTaskRow(t *testing.T, s *Server, topSession, taskID string, cond func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		if row := taskRowOf(t, s, topSession, taskID); row != nil {
			last = row
			if cond(row) {
				return row
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("权威行未达预期: task_id=%s row=%+v", taskID, last)
	return nil
}

// taskRowOf 读权威表该 task_id 行（读失败 / 不存在 → nil）。
func taskRowOf(t *testing.T, s *Server, topSession, taskID string) map[string]any {
	t.Helper()
	res, err := dataRequest(s.bus, "data-tasktree-list", map[string]any{
		"instance_id": "ins-test",
		"data":        map[string]any{"top_session": topSession, "include_closed": true},
	})
	if err != nil {
		return nil
	}
	nodes, _ := res["nodes"].([]any)
	for _, it := range nodes {
		if m, _ := it.(map[string]any); m != nil && str(m["task_id"]) == taskID {
			return m
		}
	}
	return nil
}

// rowExecJSON 解出行内 exec_json（空 / 非法 → 失败）。
func rowExecJSON(t *testing.T, row map[string]any) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(str(row["exec_json"])), &m); err != nil {
		t.Fatalf("exec_json 非法（%v）: %q", err, str(row["exec_json"]))
	}
	return m
}

// TestTaskLayerExecStateSurvivesLlmSnapshots：**P3 真实链路复验**（真 bus + 真 persist + 真层 +
// 真发布路径 `tm.setState`/`tm.done`）——gateway 执行态（经装配注入的 `taskExecSink`）写入
// `detached` + `exec_json` 后，llm 侧**转后台快照**（tasks.updated state=pending）与**终态快照**
// （tasks.done）都不得抹掉执行态信息：
//
//	① pending 快照 → 库行 `state` 仍 detached（粗粒度不降级）、`exec_json` 仍含 gw_task_id /
//	   provider_key / detail.trigger（真机缺口：原先被清空并覆盖为 pending）；
//	② done 快照 → 库行 `state`=done（终态覆盖）且 `exec_json` 仍在。
func TestTaskLayerExecStateSurvivesLlmSnapshots(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-p3fix", "t-p3fix")

	node, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-p3fix", TurnID: "t-p3fix",
		InstanceID: "ins-test", TopSession: "s-p3fix", Name: "转后台任务", ToolCallID: "tc-p3fix",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	s.tasks.setGwTask(node.TaskID, "tk-p3fix") // 层 BindExec（执行态按 exec 映射命中该节点）
	waitTaskRow(t, s, "s-p3fix", node.TaskID, func(map[string]any) bool { return true })

	// gateway 执行态：转后台（auto 阈值）→ detached + exec_json
	(&taskExecSink{layer: s.taskLayer}).OnExecState(mcpgateway.ExecState{
		GWTaskID: "tk-p3fix", InstanceID: "ins-test", TopSession: "s-p3fix", Tool: "core_file_read",
		ToolCallID: "tc-p3fix", WorkDir: testWorkDir, ProviderKey: "self",
		Phase: mcpgateway.ExecPhaseDetached, State: "running",
		Detail: map[string]any{"trigger": "auto", "threshold_s": 5},
	})

	// ① llm 侧转后台快照（真实发布路径：tasks.updated{state=pending}）→ 不得降级 / 不得抹空
	s.tasks.setState(node.TaskID, TaskStatePending)
	row := waitTaskRow(t, s, "s-p3fix", node.TaskID, func(row map[string]any) bool {
		return str(row["state"]) == tasklayer.StateDetached
	})
	exec := rowExecJSON(t, row)
	if exec["gw_task_id"] != "tk-p3fix" || exec["provider_key"] != "self" ||
		exec["phase"] != mcpgateway.ExecPhaseDetached {
		t.Fatalf("pending 快照抹掉/改写了 exec_json: %+v", exec)
	}
	if detail, _ := exec["detail"].(map[string]any); detail == nil || detail["trigger"] != "auto" {
		t.Fatalf("pending 快照抹掉 exec_json.detail: %+v", exec)
	}

	// ② llm 侧终态快照（真实发布路径：tasks.done）→ 终态覆盖且 exec_json 保留
	s.tasks.done(node.TaskID, TaskStateDone, "完成", "")
	row = waitTaskRow(t, s, "s-p3fix", node.TaskID, func(row map[string]any) bool {
		return str(row["state"]) == tasklayer.StateDone
	})
	exec = rowExecJSON(t, row)
	if exec["gw_task_id"] != "tk-p3fix" || exec["provider_key"] != "self" {
		t.Fatalf("终态快照抹掉 exec_json: %+v", exec)
	}
	if detail, _ := exec["detail"].(map[string]any); detail == nil || detail["trigger"] != "auto" {
		t.Fatalf("终态快照抹掉 exec_json.detail: %+v", exec)
	}
	// 层与库仍一致（合并语义不破坏单写者一致性）
	rep, err := s.taskLayer.Verify("ins-test", "s-p3fix")
	if err != nil || !rep.Match() {
		t.Fatalf("Verify 应零差异: err=%v\n%s", err, rep.String())
	}
}

// TestTaskStopByIdAndToolCall：task-stop 全链路（2026-09-18，取代已移除的 `mcp-tasks-cancel`）：
//
//	① id 归一：前端工具行「停止」只有 tool_call_id（裁决态气泡无 server 节点 id）→ 反查任务节点
//	   → 层判定可取消集合 → 层回调经进程内 sink `CancelExec(gw_task_id)` 真打断 + 标 cancelled 广播；
//	② 已不可逆终态 → 明确报错，**不重复下发**执行侧取消；
//	③ id 缺失 / 未命中 → 明确报错；
//	④ 执行池未注入（sink nil）且层确有在飞执行 → 明确报错（不静默）。
func TestTaskStopByIdAndToolCall(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-stop", "t-stop") // 注册 instance（层可定位权威表）
	te := watchTasks(t, s.bus)
	resetExecControl()

	node, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-stop", TurnID: "t-stop",
		InstanceID: "ins-test", TopSession: "s-stop", Name: "inflight", ToolCallID: "tc-stop",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	s.tasks.setGwTask(node.TaskID, "tk-stop-gw") // 层持 exec 句柄（含 gw_task_id）
	te.wait(t, "started", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		return len(te.started) == 1
	})

	// ① tool_call_id → 反查 task_id → 层 → sink.CancelExec(gw_task_id)（真打断）+ 标终态
	v := s.bus.Emit(context.Background(), "task-stop", jb(map[string]any{
		"tool_call_id": "tc-stop",
	})).Wait()
	if v.Err() != nil {
		t.Fatalf("task-stop(tool_call_id): %v", v.Err())
	}
	res, _ := v.Result.(map[string]any)
	if ok, _ := res["cancelled"].(bool); !ok || str(res["task_id"]) != node.TaskID {
		t.Fatalf("task-stop ack=%+v want cancelled=true task_id=%s", res, node.TaskID)
	}
	if got := execCancelledRefs(); len(got) != 1 || got[0] != "tk-stop-gw" {
		t.Fatalf("sink.CancelExec=%v want [tk-stop-gw]", got)
	}
	te.wait(t, "cancelled", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		d := te.done[node.TaskID]
		return d != nil && d["state"] == TaskStateCancelled
	})
	if st, ok := s.taskLayer.State(node.TaskID); !ok || st != TaskStateCancelled {
		t.Fatalf("层状态应为 cancelled: %q ok=%v", st, ok)
	}

	// ② 已终态 → 明确报错 + 不重复下发执行侧取消
	v = s.bus.Emit(context.Background(), "task-stop", jb(map[string]any{"task_id": node.TaskID})).Wait()
	if err := v.Err(); err == nil || !strings.Contains(err.Error(), "已结束") {
		t.Fatalf("已终态应回明确错误, got %v", err)
	}
	if got := execCancelledRefs(); len(got) != 1 {
		t.Fatalf("已终态不得重复下发执行侧取消: %v", got)
	}

	// ③ id 缺失 / 未命中 → 明确报错
	if err := s.bus.Emit(context.Background(), "task-stop", jb(map[string]any{})).Wait().Err(); err == nil {
		t.Fatal("缺 task_id/tool_call_id 应报错")
	}
	if err := s.bus.Emit(context.Background(), "task-stop", jb(map[string]any{"tool_call_id": "tc-none"})).Wait().Err(); err == nil {
		t.Fatal("tool_call_id 未命中应报错")
	}

	// ④ 执行池未注入 + 确有在飞执行 → 明确报错（不静默）
	node2, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-stop", TurnID: "t-stop",
		InstanceID: "ins-test", TopSession: "s-stop", Name: "inflight2", ToolCallID: "tc-stop-2",
	})
	if err != nil {
		t.Fatalf("start2: %v", err)
	}
	s.tasks.setGwTask(node2.TaskID, "tk-stop-gw2")
	s.execSink = nil
	err = s.bus.Emit(context.Background(), "task-stop", jb(map[string]any{"task_id": node2.TaskID})).Wait().Err()
	s.execSink = testExecSink
	if err == nil || !strings.Contains(err.Error(), "执行池不可用") {
		t.Fatalf("sink 未注入应回明确错误, got %v", err)
	}
}

// TestRunToolStopExplicitTexts：tool_stop 域工具（LLM 侧）错误语义 —— 执行池未注入 / 已终态
// 均回明确文案（不静默）；真实停止经层 → sink 下发（链路由 TestTaskStopByIdAndToolCall 锁定）。
func TestRunToolStopExplicitTexts(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-ts", "t-ts")
	te := watchTasks(t, s.bus)
	resetExecControl()

	node, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-ts", TurnID: "t-ts",
		InstanceID: "ins-test", TopSession: "s-ts", Name: "x", ToolCallID: "tc-ts",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	s.tasks.setGwTask(node.TaskID, "tk-ts-gw")

	// 执行池未注入 + 层持 exec → 明确文案（不静默）
	s.execSink = nil
	if got := s.runToolStop(nil, nil, map[string]any{"task_id": node.TaskID}); !strings.Contains(got, "执行池不可用") {
		t.Fatalf("sink 未注入应回明确文案, got %q", got)
	}
	s.execSink = testExecSink

	// 正常停止 → 层 → sink 取消 + 级联停止文案
	if got := s.runToolStop(nil, nil, map[string]any{"task_id": node.TaskID}); !strings.Contains(got, "已级联停止") {
		t.Fatalf("停止文案错: %q", got)
	}
	if got := execCancelledRefs(); len(got) != 1 || got[0] != "tk-ts-gw" {
		t.Fatalf("sink.CancelExec=%v want [tk-ts-gw]", got)
	}
	te.wait(t, "cancelled", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		d := te.done[node.TaskID]
		return d != nil && d["state"] == TaskStateCancelled
	})

	// 已终态 → 明确文案（不重复取消）
	if got := s.runToolStop(nil, nil, map[string]any{"task_id": node.TaskID}); !strings.Contains(got, "已结束") {
		t.Fatalf("已终态文案错: %q", got)
	}
}
