// Package task 是任务层（Task 层独立化，对齐 21-任务层设计方案 §1 D1）：与 LLM / Data /
// Filesys **平级**的独立一层——不是 data 的一个域、不是 gateway 的子模块；DB 为权威、由事件驱动。
//
// P2 阶段（21 §9.2「层为唯一权威」）**单写者切换**：
//   - 层写**权威表 `tasktree`**（原表；`data-tasktree-upsert` **不带** shadow → 默认路径），
//     取代 P1 的影子域；persist 侧影子分支保留但默认不再使用（仅作回滚开关）；
//   - llm 侧**不再自写库**（`chonkpilot-llm/server/tasks.go` 的 `data-tasktree-upsert` 调用已移除），
//     内存 `nodes` 降级为运行态视图；
//   - 只**订阅既有事件**（llm tasks.started/updated/done、gateway mcp-tasks-report、
//     task-deleted），**零新增 MQ 主题**（21 §8.1-7）；「关闭任务」= 逻辑删除（`closed` 标记），
//     列表/树查询过滤 closed（层内部读带 `include_closed`）。
//
// 一致性与唯一性（21 §1.2 D2/D3/D6 + §8.1 附带定稿）：task_id 由**调用层分配**，层只做
// upsert / 唯一性校验；唯一域 = (workdir, task_id)（层内以 prjusr 库为隔离单位，见 store.go
// 注释）；**撞号 → 拒绝并记错误日志，层不覆盖**；写路径**全同步**（D3）；**状态单调**
// （done/error/cancelled 不可逆、pending→running 单向；interrupted 为**可恢复终态**——tool-retry
// 重跑允许 interrupted→running），回退 / 非法转移 → 拒绝并记日志，不 panic。
package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 任务节点状态（与 21-llm-server §4.3 同口径）；终态 = done/error/cancelled/interrupted。
// awaiting / detached 为 gateway 侧执行态（P3 纳入）；启动恢复对它们与 pending/running 一视同仁
// （一律标 interrupted，21 §8.1-3）。
const (
	StatePending     = "pending"     // 已提交未执行（gateway 返回 pending）
	StateRunning     = "running"     // 执行中
	StateAwaiting    = "awaiting"    // 超时待用户裁决（gateway 执行态，P3 落库）
	StateDetached    = "detached"    // 已转后台（gateway 执行态，P3 落库）
	StateDone        = "done"        // 成功（终态）
	StateError       = "error"       // 失败（终态）
	StateCancelled   = "cancelled"   // 主动取消（终态）
	StateInterrupted = "interrupted" // 进程重启遗留（**可恢复终态**：tool-retry 可重跑；不自动重跑）
)

// 节点类型（事件载荷 kind 原值：tool / llm / ask_user；落库时由 data 面按 kind 派生
// node_type = llm → session，其余 → tool，与现有 tasktree 行同源）。
const (
	NodeTypeTool = "tool"
	NodeTypeLLM  = "llm"
	NodeTypeAsk  = "ask_user"
)

// Record 是任务层记录（对齐 21 §5.1 α 单表自引用；json tag = snake_case）。
//
// 与 21 §9.1 交付物 1 字段表一致；唯一**新增**字段 = Title（§9.1 未列，但权威行须与
// tasktree.title 同源才能做一致性校验——以真实代码为准，见 Verify）与 DeletedAt（P2 逻辑删除）。
type Record struct {
	WorkDir      string `json:"work_dir"`      // 隔离键来源（D6）；权威行由 llm 事件载荷增补字段携带
	TaskID       string `json:"task_id"`       // 调用层分配（D2）；唯一域 = (work_dir, task_id)
	InstanceID   string `json:"instance_id"`   // 仅来源标注（D6：不作隔离键、不作过滤键）
	ParentID     string `json:"parent_id"`     // 父节点（调用层给，层不猜不重排）
	TopSession   string `json:"top_session"`   // 组织 / 过滤维度（D7：树按主会话切分）
	SessionID    string `json:"session_id"`    // 会话身份（llm 节点 = 子会话）
	NodeType     string `json:"node_type"`     // tool / llm / ask_user（事件 kind 原值）
	Tool         string `json:"tool"`          // 工具名
	ToolCallID   string `json:"tool_call_id"`  // LLM tool-call id
	State        string `json:"state"`         // pending/running/done/error/cancelled/interrupted
	Title        string `json:"title"`         // 展示名（simplified 优先，回落 name）；与 tasktree.title 同源
	ArgsDigest   string `json:"args_digest"`   // 参数摘要（sha256 前 16 hex；空 = 无）
	ResultDigest string `json:"result_digest"` // 结果 / 错误摘要（sha256 前 16 hex）
	ExecJSON     string `json:"exec_json"`     // 执行态扩展（P3-① 起写：phase/state/detail（detached 的 mode/threshold_s、awaiting 的 timeout_s/options）/at）
	CreatedAt    string `json:"created_at"`    // 创建时刻（= 事件 started_at，与 tasktree.created_at 同源）
	StartedAt    string `json:"started_at"`    // 开始时刻
	UpdatedAt    string `json:"updated_at"`    // 层内最近写入时刻
	DoneAt       string `json:"done_at"`       // 终态时刻
	Closed       bool   `json:"closed"`        // 逻辑删除（§8.1-2：关闭任务 = 标记，历史保留）
	DeletedAt    string `json:"deleted_at"`    // 逻辑删除时刻（closed 为 true 时非空）

	// ── DSL 展示字段（DSL-3 / DSL-2；61 §3.4 增补，缺省为空 → 与既有载荷逐字节等价）──
	LoopCurrent  int    `json:"loop_current"`  // 容器当前轮次（1 起；kind=dsl_loop/dsl_parallel）
	LoopTotal    int    `json:"loop_total"`    // 容器总轮数（未知 = 0）
	StepsJSON    string `json:"steps_json"`    // 步骤执行记录（JSON 数组文本；仅 kind=dsl_job 非空）
	Shadow       bool   `json:"shadow"`        // 执行记录隐藏节点标记（true = 不进树）
	ReturnKind   string `json:"return_kind"`   // `$RETURN` 两态（inline / file）
	ReturnInline string `json:"return_inline"` // inline 全文（≤64K）
	ReturnFile   string `json:"return_file"`   // file 文件名（>64K）
	ReturnSize   int    `json:"return_size"`   // file 字节数
}

// Exec 是任务节点的**执行句柄**（P2 交付物 ⑤：层持有 → 取消时回调执行侧）。
// 由调用层（llm）在登记 gateway 异步任务时经 BindExec 注入；未登记 = 无执行侧可回调。
type Exec struct {
	GWTaskID    string `json:"gw_task_id,omitempty"`   // gateway 异步任务 id（tasks/cancel 定位）
	InstanceID  string `json:"instance_id,omitempty"`  // 归属实例（回调 ctx 注入）
	Tool        string `json:"tool,omitempty"`         // 工具名（诊断用）
	ProviderKey string `json:"provider_key,omitempty"` // 归属 provider（P3 由 gateway 回填；P2 恒空）
}

// frontVisibleStatus 是**前端可见状态**口径（P3 相位门控）：权威行 `status` 字段 = 前端渲染用
// 状态（前端只读它：`SessionTreeNode.vue` 仅对 running 显示 spinner、`SessionTree.vue` /
// `TaskDetailView.vue` / `useTaskView.js` 亦按 running 判定在飞）；`state` = 层内**权威状态**（真实相位）。
// P3 新增的两类执行态 detached / awaiting 在前端**没有对应取值** → 直接写入 status 会让节点由「转圈」
// 变为「无状态图标」（前端可见行为变化，违反 P3「前端零改动」）→ 一律呈现为在飞 running（与 P3 之前
// 同值：此前这两类相位不会进入层）。真实相位只落 `state` 与 `exec_json.phase/detail`。
func frontVisibleStatus(state string) string {
	switch state {
	case StateDetached, StateAwaiting:
		return StateRunning
	}
	return state
}

// terminalState 判断是否为终态（含可恢复的 interrupted；控制面「不重复广播」判据）。
func terminalState(state string) bool {
	switch state {
	case StateDone, StateError, StateCancelled, StateInterrupted:
		return true
	}
	return false
}

// immutableState 判断是否为**不可逆**终态（done/error/cancelled）。
// interrupted 不在其列：它是**可恢复终态**（tool-retry 重跑走 interrupted → running）。
func immutableState(state string) bool {
	switch state {
	case StateDone, StateError, StateCancelled:
		return true
	}
	return false
}

// resumableState 判断状态是否属于「重启遗留、需标 interrupted」（21 §8.1-3 的非终态集合）。
func resumableState(state string) bool {
	switch state {
	case StatePending, StateRunning, StateAwaiting, StateDetached:
		return true
	}
	return false
}

// knownState 判断状态是否是层认可的状态（未知状态一律拒绝，避免脏值入库）。
func knownState(state string) bool {
	switch state {
	case StatePending, StateRunning, StateAwaiting, StateDetached,
		StateDone, StateError, StateCancelled, StateInterrupted:
		return true
	}
	return false
}

// legalTransition 状态单调校验（21 §7.4：终态不可逆、pending→running 单向）：
//   - 旧态 = 不可逆终态（done/error/cancelled）→ 仅允许「同态」（幂等补齐字段），任何改态 = 回退 → 拒绝；
//   - 旧态 = interrupted（可恢复终态）→ 允许 interrupted → running（tool-retry 重跑）与终态收敛；
//   - 旧态 = pending → 允许 pending/终态，**pending → running = 回退 → 拒绝**（21 §7.4 单向口径）；
//   - 旧态 = running → 允许 running/终态 + **running → pending**（既有真实事件流：异步/转后台
//     `setState(pending)`，见 chonkpilot-llm/server/turn.go；拒绝会让单写者丢掉该状态变更）；
//   - 未知新态 → 拒绝。
func legalTransition(from, to string) bool {
	if !knownState(to) {
		return false
	}
	if immutableState(from) {
		return from == to
	}
	if from == StatePending && to == StateRunning {
		return false
	}
	return true
}

// execStateDowngrade 判定一次写入是否**把既有执行态降级为粗粒度状态**（P3 修正 ②；
// I-100 收严：interrupted 纳入受保护集合）。
//
// 状态优先级格：**终态(done/error/cancelled/interrupted) > 执行态(detached/awaiting) > 粗粒度(pending/running)**。
// incoming pending/running 落在既有 detached/awaiting 上即为降级 → 调用方**保留既有状态**
// （其余字段照常合并/落库；不返回错误，避免丢掉同载荷的字段补全）。终态**不在本规则之列**：
// 终态必须覆盖执行态（覆盖时 exec_json 由 commitAgainst 的合并语义保留）。
//
// I-100：**interrupted（可恢复终态）一旦写入，incoming `pending` 不得回退**（重启遗留不得被
// 「未执行」粗粒度快照覆盖为 pending）；唯一例外 = `interrupted → running`（tool-retry 显式重跑，
// 见 legalTransition 与 layer_test.TestInterruptedResumable），故对 interrupted 仅拦 `pending`。
func execStateDowngrade(oldState, newState string) bool {
	switch oldState {
	case StateDetached, StateAwaiting:
		return newState == StatePending || newState == StateRunning
	case StateInterrupted:
		return newState == StatePending
	}
	return false
}

// conflictOf 撞号判定（21 §8.1：同 (workdir, task_id) 语义冲突 → 拒绝并不覆盖）：
// 仅当**两侧同字段均非空且不同**时判冲突（空 = 尚未确定，后续事件补全，如 llm 节点
// session_id 由 update 后置填充，属正常补全而非冲突）。返回空串 = 无冲突。
func conflictOf(old, rec *Record) string {
	for _, f := range []struct {
		name string
		a, b string
	}{
		{"work_dir", old.WorkDir, rec.WorkDir},
		{"top_session", old.TopSession, rec.TopSession},
		{"node_type", old.NodeType, rec.NodeType},
		{"tool_call_id", old.ToolCallID, rec.ToolCallID},
		{"parent_id", old.ParentID, rec.ParentID},
	} {
		if f.a != "" && f.b != "" && f.a != f.b {
			return fmt.Sprintf("%s 冲突（已有 %q ≠ 新 %q）", f.name, f.a, f.b)
		}
	}
	return ""
}

// sameRecord 除 UpdatedAt 外全字段相同（幂等判定：相同事件重复到达 → 不重复落库）。
func sameRecord(a, b *Record) bool {
	ac, bc := *a, *b
	ac.UpdatedAt, bc.UpdatedAt = "", ""
	return ac == bc
}

// digest 生成摘要（sha256 前 8 字节 = 16 hex；空串 → 空 = 无）。
func digest(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// nodeEvent 是 llm tasks.started / tasks.updated / tasks.done 的载荷（完整 TaskNode 广播
// 快照子集，见 chonkpilot-llm/server/tasks.go TaskNode json tag）。
type nodeEvent struct {
	TaskID        string `json:"task_id"`
	Tool          string `json:"tool"`
	ToolCallID    string `json:"tool_call_id"`
	ParentID      string `json:"parent_id"`
	TopSession    string `json:"top_session"`
	Kind          string `json:"kind"`
	SessionID     string `json:"session_id"`
	TurnID        string `json:"turn_id"`
	InstanceID    string `json:"instance_id"`
	Name          string `json:"name"`
	Simplified    string `json:"simplified"`
	State         string `json:"state"`
	StartedAt     string `json:"started_at"`
	FinishedAt    string `json:"finished_at"`
	ResultSummary string `json:"result_summary"`
	Error         string `json:"error"`
	Args          any    `json:"args"`
	// 21 §7.4 建议的 payload 增补字段（由 llm 事件发布点随 instance 绑定增补）。
	WorkDir string `json:"work_dir"`
	// DSL 展示字段（DSL-3 / DSL-2；llm 事件载荷增补，缺省为空）。
	LoopCurrent  int             `json:"loop_current"`
	LoopTotal    int             `json:"loop_total"`
	Steps        json.RawMessage `json:"steps"`
	Shadow       bool            `json:"shadow"`
	ReturnKind   string          `json:"return_kind"`
	ReturnInline string          `json:"return_inline"`
	ReturnFile   string          `json:"return_file"`
	ReturnSize   int             `json:"return_size"`
}

// recordFromNode 把 llm 节点快照映射为层记录（21 §9.1 交付物 1「payload → 记录映射」）。
// created_at 取 started_at：现有 llm 落库即 `created_at = TaskNode.StartedAt`
// （chonkpilot-llm/server/tasks.go:258），保持一致才能零差异校验。
func recordFromNode(ev *nodeEvent) (*Record, error) {
	if ev.TaskID == "" {
		return nil, errors.New("task: 节点事件缺 task_id")
	}
	wd := ev.WorkDir
	kind := ev.Kind
	if kind == "" {
		kind = NodeTypeTool
	}
	title := ev.Simplified
	if title == "" {
		title = ev.Name
	}
	state := ev.State
	if state == "" {
		state = StatePending
	}
	summary := ev.ResultSummary
	if summary == "" {
		summary = ev.Error
	}
	return &Record{
		WorkDir:      wd,
		TaskID:       ev.TaskID,
		InstanceID:   ev.InstanceID,
		ParentID:     ev.ParentID,
		TopSession:   ev.TopSession,
		SessionID:    ev.SessionID,
		NodeType:     kind,
		Tool:         ev.Tool,
		ToolCallID:   ev.ToolCallID,
		State:        state,
		Title:        title,
		ArgsDigest:   digestAny(ev.Args),
		ResultDigest: digest(summary),
		CreatedAt:    ev.StartedAt,
		StartedAt:    ev.StartedAt,
		DoneAt:       ev.FinishedAt,

		LoopCurrent:  ev.LoopCurrent,
		LoopTotal:    ev.LoopTotal,
		StepsJSON:    rawSteps(ev.Steps),
		Shadow:       ev.Shadow,
		ReturnKind:   ev.ReturnKind,
		ReturnInline: ev.ReturnInline,
		ReturnFile:   ev.ReturnFile,
		ReturnSize:   ev.ReturnSize,
	}, nil
}

// rawSteps 归一事件 `steps` 原始 JSON（nil / null → 空串；其余原样为 JSON 数组文本）。
func rawSteps(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	return s
}

// reportEvent 是 gateway mcp-tasks-report 的载荷（chonkpilot-mcp-gateway/gateway/subjects.go:29）。
// I-90（2026-09-18，用户确认）增补两个字段：`top_session` / `parent`（缺失时为空）——
// 使完成回报**自带树归属**，层可在「未登记」时独立建节点（见 layer.applyReport）。
type reportEvent struct {
	TaskID        string `json:"task_id"`
	ToolCallID    string `json:"tool_call_id"`
	Tool          string `json:"tool"`
	State         string `json:"state"`
	ResultSummary string `json:"result_summary"`
	Session       string `json:"session"`
	Turn          string `json:"turn"`
	InstanceID    string `json:"instance_id"`
	TopSession    string `json:"top_session"` // 树归属：该调用所属主会话（llm 经 tools/call context 下发）
	Parent        string `json:"parent"`      // 已登记的 llm 侧节点 task_id（调用层给；无则空）
}

// recordFromReport 把 gateway 完成回报映射为层记录：
//   - 已知节点（按 task_id）→ 仅推进状态（见 layer.applyReport ①）；
//   - 未登记且有 `top_session` → 建节点（parent 已随载荷给出，见 ②）。
func recordFromReport(ev *reportEvent) (*Record, error) {
	if ev.TaskID == "" {
		return nil, errors.New("task: 回报缺 task_id")
	}
	state := ev.State
	if !knownState(state) {
		state = ""
	}
	return &Record{
		TaskID:       ev.TaskID,
		InstanceID:   ev.InstanceID,
		ParentID:     ev.Parent,
		TopSession:   ev.TopSession,
		SessionID:    ev.Session,
		NodeType:     NodeTypeTool,
		Tool:         ev.Tool,
		ToolCallID:   ev.ToolCallID,
		State:        state,
		ResultDigest: digest(ev.ResultSummary),
	}, nil
}

// deletedEvent 是 task-deleted 的载荷（persist `data-tasktree-delete` 逻辑删除后广播；
// P2 起该事件表示「关闭 = 标记 closed」，层据此同步视图，**不物理删除**）。
type deletedEvent struct {
	NodeID string `json:"node_id"`
}

// digestAny 生成任意 JSON 值的摘要（nil / 空对象 → 空）。
func digestAny(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 || string(b) == "null" {
		return ""
	}
	return digest(string(b))
}
