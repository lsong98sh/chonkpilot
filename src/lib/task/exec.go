// 执行态纳入（21-任务层设计方案 §9.3 P3-① · exec.go）：gateway 的执行态
// （started / detached / awaiting / error）经**层内 API 直调**（`Layer.OnExecState`，同进程同步调用、
// **不新增 MQ 主题** → 61 消息面零变更，21 §8.1-7）解析到已有节点并同步写权威表：
//   - `state` 按口径映射（21 §9.3 P3）：started→running、detached→detached、awaiting→awaiting、
//     error→error；均属**既有状态枚举**（`task.go`：awaiting/detached 为 gateway 侧执行态，
//     `resumableState` 已含二者 → 启动恢复一律标 interrupted，21 §8.1-3）；
//   - 明细（`workdir` / `provider_key` / detached 的 trigger+threshold_s / awaiting 的
//     timeout_s+options / error 的 message）写入 **`exec_json`**（前端 `data-tasktree-tasks` 可读）
//     ——此前该项为空缺（21 §9.3 P3）；
//   - 终态 done/cancelled 不经本通道（仍走既有 `mcp-tasks-report`，见 layer.applyReport）。
//
// 解析顺序（执行 id 与 llm 节点 id 不同源 → 命中既有节点，**不建重复行**）：
//
//	① exec 映射（`gw_task_id ↔ llm 节点`；`BindExec` / 本文件补登记）命中 → 该节点；
//	② 未命中但 `tool_call_id` 命中**已登记**节点 → 该节点，并**补登记 exec 映射**
//	   （doCall 的 started 上报早于 llm 的 setGwTask：此步使后续执行态与完成回报直接命中）；
//	③ 皆未命中且带 `top_session` → 建行（**started 除外**：started 早于调用层登记窗口，此时建行会与
//	   随后登记的同一调用节点成为「孤儿重复行」→ 跳过并记日志）；
//	④ 否则跳过并记日志。
//
// 非法转移（如不可逆终态 → error）→ **拒绝并记日志**（遵守 21 §7.4 单调约束）。
//
// 失败隔离：`OnExecState` 无返回值，层内失败**只告警** —— 绝不 panic、绝不影响 gateway 调用路径
// （21 §9.2 硬约束）。
package task

import "encoding/json"

// ── exec_json 只增不减（P3 修正 ①）──────────────────────────────────────────────
//
// `exec_json` 是**累加型**执行态明细（P3-① 起写）：本文件的 gateway 执行态事件（`OnExecState`）按字段
// **深合并**进既有值（先 detached 再 running 的更新不得丢掉 gw_task_id / provider_key / detail 明细）；
// llm 侧节点 / 终态快照（layer.applyNode，载荷**不含** exec 信息）则**原样保留**既有值。
// 修复缺口（真机复现）：llm 侧快照全量 upsert 曾把 gateway 写入的 exec_json 抹空/把 detached 覆盖为 pending。
// 合并正文落在**唯一落库出口** `commitAgainst`（layer.go），本文件只提供纯函数。

// mergeExecJSON 深合并 exec_json（只增不减）：incoming 空 → 旧值；旧值空 → incoming；
// 两侧均为对象 → 逐键递归合并（incoming 覆盖同名非空键、保留旧值独有键）；任一侧非法 → 取另一侧。
func mergeExecJSON(oldJSON, newJSON string) string {
	if newJSON == "" {
		return oldJSON
	}
	if oldJSON == "" {
		return newJSON
	}
	var old, inc map[string]any
	if json.Unmarshal([]byte(oldJSON), &old) != nil {
		return newJSON
	}
	if json.Unmarshal([]byte(newJSON), &inc) != nil {
		return oldJSON
	}
	b, err := json.Marshal(mergeJSONMap(old, inc))
	if err != nil {
		return newJSON
	}
	return string(b)
}

// mergeJSONMap 递归合并两个 JSON 对象（dst 为底、src 覆盖）：对象逐键递归；数组 / 标量整体覆盖；
// src 的**空值（空串 / null）不覆盖** dst（只增不减语义）。
func mergeJSONMap(dst, src map[string]any) map[string]any {
	out := make(map[string]any, len(dst)+len(src))
	for k, v := range dst {
		out[k] = v
	}
	for k, v := range src {
		if v == nil || v == "" {
			continue // 空值不覆盖（只增不减）
		}
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := out[k].(map[string]any); ok {
				out[k] = mergeJSONMap(dm, sm)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// 执行态上报点（phase；与 gateway 侧 mcpgateway.ExecState 同口径）。
const (
	ExecPhaseStarted   = "started"   // 执行启动（doCall spawn 后）
	ExecPhaseDetached  = "detached"  // 转后台（auto 阈值 / manual tools/background）
	ExecPhaseAwaiting  = "awaiting"  // 超时待用户裁决
	ExecPhaseDone      = "done"      // 终态：成功
	ExecPhaseError     = "error"     // 终态：失败
	ExecPhaseCancelled = "cancelled" // 终态：取消
)

// ExecState 是 gateway 执行态上报载荷（宿主适配 gateway 侧同名结构后直调 `Layer.OnExecState`）：
// `GWTaskID` = gateway 执行 id（`tk-0001`）；`State` = gateway 执行池状态；`WorkDir` / `ProviderKey`
// = 隔离键来源与归属 provider；`Message` = 异常摘要（error 路径，简短、不含堆栈）；`Detail` =
// 上报点明细（detached 的 trigger/threshold_s、awaiting 的 timeout_s/options）→ 落 `exec_json`。
type ExecState struct {
	GWTaskID    string         `json:"gw_task_id"`
	InstanceID  string         `json:"instance_id,omitempty"`
	TopSession  string         `json:"top_session,omitempty"`
	Tool        string         `json:"tool,omitempty"`
	ToolCallID  string         `json:"tool_call_id,omitempty"`
	WorkDir     string         `json:"work_dir,omitempty"`
	ProviderKey string         `json:"provider_key,omitempty"`
	Phase       string         `json:"phase"`
	State       string         `json:"state,omitempty"`
	Message     string         `json:"message,omitempty"`
	Detail      map[string]any `json:"detail,omitempty"`
	At          string         `json:"at,omitempty"`
}

// execPhaseState 执行态 → 层状态（**只用既有枚举**，不发明新状态；21 §9.3 P3-①）：
// started→running、detached→detached、awaiting→awaiting、error→error、done/cancelled 同名；
// 未知 phase → ""（不改状态，仅写 exec_json）。
func execPhaseState(phase string) string {
	switch phase {
	case ExecPhaseStarted:
		return StateRunning
	case ExecPhaseDetached:
		return StateDetached
	case ExecPhaseAwaiting:
		return StateAwaiting
	case ExecPhaseDone:
		return StateDone
	case ExecPhaseError:
		return StateError
	case ExecPhaseCancelled:
		return StateCancelled
	}
	return ""
}

// OnExecState 执行态纳入（P3-①；同步写权威表，无返回值 = 失败只告警）：
// 解析到既有节点 → 合并状态与 exec_json（exec_json **深合并**既有值，只增不减）；未命中 →
// 按 buildExecNodeLocked 的回落处置。
func (l *Layer) OnExecState(ev ExecState) {
	if l.bus == nil || ev.GWTaskID == "" {
		return
	}
	if ev.At == "" {
		ev.At = nowRFC3339()
	}
	detail, err := json.Marshal(ev)
	if err != nil {
		l.warn("执行态序列化失败（跳过，仅告警）: gw_task_id=%s %v", ev.GWTaskID, err)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	rec := l.execTargetLocked(&ev)
	if rec == nil {
		l.buildExecNodeLocked(&ev, string(detail))
		return
	}
	// 执行句柄补充归属 provider（P3：层持 `exec.provider_key` → 取消回调定位归属 provider）
	l.bindExecLocked(rec.TaskID, ev.GWTaskID, rec)
	if ex, ok := l.execs[rec.TaskID]; ok && ev.ProviderKey != "" && ex.ProviderKey == "" {
		ex.ProviderKey = ev.ProviderKey
		l.execs[rec.TaskID] = ex
	}
	next := *rec
	if st := execPhaseState(ev.Phase); st != "" {
		next.State = st
	}
	next.ExecJSON = mergeExecJSON(rec.ExecJSON, string(detail)) // 只增不减：与既有 exec_json 深合并
	if terminalState(next.State) && next.DoneAt == "" {
		next.DoneAt = ev.At
	}
	next.UpdatedAt = nowRFC3339()
	if err := l.commitAgainst(&next, rec); err != nil {
		l.warn("执行态入库失败（仅告警，不影响 gateway 调用路径）: task_id=%s gw_task_id=%s phase=%s %v",
			rec.TaskID, ev.GWTaskID, ev.Phase, err)
	}
}

// ExecStateOf 按 gateway 执行 id 返回该节点在层中的**权威状态**（P3-②：gateway 控制面
// tasks/status|list|cancel 以层为准）。第二个返回值 false = 层未登记该执行（调用方回落自身视图判定）。
func (l *Layer) ExecStateOf(gwTaskID string) (string, bool) {
	if gwTaskID == "" {
		return "", false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	id := l.llmNodeByExecLocked(gwTaskID)
	if id == "" {
		return "", false
	}
	if rec := l.recs[id]; rec != nil && rec.State != "" {
		return rec.State, true
	}
	return "", false
}

// execTargetLocked 解析执行态的目标节点（持锁调用）：① exec 映射 → ② tool_call_id 命中已登记节点
// （命中即补登记 exec 映射）→ ③ 空（调用方按未命中处置）。
func (l *Layer) execTargetLocked(ev *ExecState) *Record {
	if id := l.llmNodeByExecLocked(ev.GWTaskID); id != "" {
		if rec := l.recs[id]; rec != nil {
			return rec
		}
		if rec := l.load(&Record{TaskID: id, TopSession: ev.TopSession, InstanceID: ev.InstanceID}); rec != nil {
			return rec
		}
	}
	id := l.matchParentByToolCallLocked(ev.ToolCallID)
	if id == "" {
		return nil
	}
	rec := l.recs[id]
	if rec == nil {
		return nil
	}
	l.bindExecLocked(id, ev.GWTaskID, rec) // 补登记映射：后续执行态 / 完成回报直接命中
	return rec
}

// llmNodeByExecLocked 由 gateway 执行 id 反查 llm 节点 id（持锁调用；exec 映射反向查找；未登记 → 空）。
func (l *Layer) llmNodeByExecLocked(gwTaskID string) string {
	if gwTaskID == "" {
		return ""
	}
	for id, ex := range l.execs {
		if ex.GWTaskID != "" && ex.GWTaskID == gwTaskID {
			return id
		}
	}
	return ""
}

// bindExecLocked 补登记 exec 映射（持锁调用）：已有映射（调用方显式 BindExec）→ 不覆盖。
func (l *Layer) bindExecLocked(taskID, gwTaskID string, rec *Record) {
	if _, ok := l.execs[taskID]; ok {
		return
	}
	l.execs[taskID] = Exec{GWTaskID: gwTaskID, InstanceID: rec.InstanceID, Tool: rec.Tool}
}

// buildExecNodeLocked 未命中既有节点的执行态 → 建行（持锁调用；规则见文件头 ③④）：
// 无 `top_session` 无法定位树 → 跳过；`started` 早于调用层登记窗口 → 跳过（避免孤儿重复行）；
// 其余按 tool_call_id 三级回落挂父（同 buildReportedNodeLocked），失败只告警。
func (l *Layer) buildExecNodeLocked(ev *ExecState, execJSON string) {
	if ev.TopSession == "" {
		l.logf("task: 执行态未命中节点且无 top_session（无法建树，跳过）: gw_task_id=%s phase=%s",
			ev.GWTaskID, ev.Phase)
		return
	}
	if ev.Phase == ExecPhaseStarted {
		l.logf("task: 执行态 started 未命中已登记节点（跳过建行，待调用层登记）: gw_task_id=%s tool_call_id=%s",
			ev.GWTaskID, ev.ToolCallID)
		return
	}
	state := execPhaseState(ev.Phase)
	if state == "" {
		l.logf("task: 执行态 phase 不可识别（跳过建行）: gw_task_id=%s phase=%q", ev.GWTaskID, ev.Phase)
		return
	}
	rec := &Record{
		WorkDir: ev.WorkDir, TaskID: ev.GWTaskID, InstanceID: ev.InstanceID, TopSession: ev.TopSession,
		NodeType: NodeTypeTool, Tool: ev.Tool, ToolCallID: ev.ToolCallID,
		State: state, ExecJSON: execJSON,
		CreatedAt: ev.At, StartedAt: ev.At, UpdatedAt: ev.At,
	}
	if pid := l.matchParentByToolCallLocked(rec.ToolCallID); pid != "" {
		rec.ParentID = pid
		l.logf("task: 执行态建节点：按 tool_call_id 匹配到已登记节点作为父 task_id=%s tool_call_id=%s parent=%s",
			rec.TaskID, rec.ToolCallID, pid)
	} else {
		l.logf("task: 执行态建节点：parent 空且 tool_call_id 无匹配 → 作为 top_session 下的顶层节点 task_id=%s tool_call_id=%s top_session=%s",
			rec.TaskID, rec.ToolCallID, rec.TopSession)
	}
	if terminalState(rec.State) {
		rec.DoneAt = ev.At
	}
	if err := l.commitAgainst(rec, nil); err != nil {
		l.warn("执行态建节点失败（仅告警，不影响主路径）: gw_task_id=%s %v", rec.TaskID, err)
		return
	}
	// 登记 exec 映射（新建行以 gateway 执行 id 为节点 id）：使后续执行态 / 完成回报 / 控制面查询命中同一行
	l.bindExecLocked(rec.TaskID, ev.GWTaskID, rec)
	if ev.ProviderKey != "" {
		ex := l.execs[rec.TaskID]
		ex.ProviderKey = ev.ProviderKey
		l.execs[rec.TaskID] = ex
	}
}
