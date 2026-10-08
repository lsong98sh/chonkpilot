// 任务编排（TaskManager，对齐 21-llm-server / 21-任务层设计方案 §9.2 P2）：
// 任务树/级联/批量/状态事件由 server 编排——LLM 回合工具请求 → 建任务节点
// （kind=工具/llm/ask_user；parent_id 挂父节点，top_session 归属）→ 广播 tasks.started；
// 状态/进度变更 → tasks.updated（节流 ≤250ms）；终态（done/error/cancelled）→ tasks.done
// （完整 task 结构）。gateway 是纯执行层，不对前端广播任务事件。
// 同步工具也发短生命周期事件（started → done）。前端只订阅 server 一个面。
//
// **P2 单写者**：tasktree 权威行由 **chonkpilot-task 任务层**订阅同一批事件后同步落库
// （`chonkpilot-task/layer.go`）；本 manager **不再自写库**，内存 `nodes` 降级为运行态视图
// （含 gateway task id / args 等不落库字段）。控制面（取消）以层状态为权威并由层回调执行侧
// （21 §9.2 交付物 ⑤）。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
	tasklayer "github.com/chonkpilot/chonkpilot-task"
)

// TaskKind 任务节点类型（21-llm-server：kind=工具/llm/ask_user）。
const (
	TaskKindTool    = "tool"     // 普通工具（同步/异步，经 gateway tools/call 执行）
	TaskKindLLM     = "llm"      // llm 型工具（llm_run DSL）→ server 开子会话执行
	TaskKindAskUser = "ask_user" // ask_user 工具 → server ask 通道（广播 ask-user → 等 reply → 回填）
)

// DSL 展示节点 kind（DSL-3，[42 §2 (251)/(252)]；61 §3.4 增补取值）：
//   - `dsl_job`      = llm_run 作业根节点（原 kind=llm 的根，DSL 展示入口）；
//   - `dsl_loop`     = LOOP 折叠容器节点（静态语句，**不随迭代增长**）；
//   - `dsl_parallel` = PARALLEL 折叠容器节点（同上）；
//   - `dsl_step`     = 单次执行记录 kind（**保留取值**：本实现选择「记录入 `dsl_job.steps[]`」
//     方案，不建树节点，故当前不产出；见 jobdsl.go buildStaticTree 注释）。
const (
	TaskKindDslJob      = "dsl_job"
	TaskKindDslLoop     = "dsl_loop"
	TaskKindDslParallel = "dsl_parallel"
	TaskKindDslStep     = "dsl_step"
)

// DslStepRecord 是 DSL 作业的一次 LLM 步骤执行记录（DSL-3：`dsl_job.steps[]` 元素，扁平、
// 不随迭代进树；`No` 跨迭代累计、1 起）。前端据 `session_id` 做「查看/新窗口」。
type DslStepRecord struct {
	No          int    `json:"no"`                     // 跨迭代累计序号（1 起）
	Status      string `json:"status"`                 // running / done / error / cancelled
	Purpose     string `json:"purpose"`                // 该步运行目的（展示名）
	ElapsedMs   int64  `json:"elapsed_ms"`             // 耗时（毫秒）
	CreatedAt   string `json:"created_at"`             // 开始时刻（ISO/RFC3339）
	SessionID   string `json:"session_id"`             // 该次执行的子会话 id（jobSession-N）
	StatementID string `json:"statement_id,omitempty"` // 所属静态语句节点 id（当前实现未产出，保留）
}

// TaskState 任务节点状态（终态 done/error/cancelled/interrupted，对齐 21-llm-server）。
const (
	TaskStatePending     = "pending"     // 转后台（gateway 返回 pending{task_id}）
	TaskStateRunning     = "running"     // 执行中（tasks.started 初始态）
	TaskStateDone        = "done"        // 完成
	TaskStateError       = "error"       // 失败
	TaskStateCancelled   = "cancelled"   // 用户主动取消（task-stop 级联）
	TaskStateInterrupted = "interrupted" // 进程重启遗留（启动恢复标注；可重试，同 cancelled 语义）
)

// TaskNode 是任务树节点（完整 task 结构，对齐 21-llm-server tasks.* 载荷；
// tool_pair 气泡/任务树全链路直取）。
type TaskNode struct {
	TaskID        string  `json:"task_id"`
	Tool          string  `json:"tool"`                  // 原始工具名（core_file_read / llm_run / ask_user…）
	ToolCallID    string  `json:"tool_call_id"`          // LLM tool-call id（前端 tool_pair 定位）
	ParentID      string  `json:"parent_id,omitempty"`   // 父任务节点 id；空 = 顶层
	TopSession    string  `json:"top_session,omitempty"` // 聚合根（主会话），树隔离
	Kind          string  `json:"kind"`                  // tool / llm / ask_user
	SessionID     string  `json:"session_id"`
	TurnID        string  `json:"turn_id"`
	InstanceID    string  `json:"instance_id"`
	WorkDir       string  `json:"work_dir,omitempty"`   // 隔离键（21 §1.2 D6：任务层管理单位 = work_dir；**载荷增补字段**，任务层落库用）
	Name          string  `json:"name"`                 // 展示名（LLM 填的 tool_call_display_name 优先，缺省工具名；llm 用 title）
	Purpose       string  `json:"purpose,omitempty"`    // 同展示名（LLM 必填的调用理由；转后台工具展示用）
	Simplified    string  `json:"simplified,omitempty"` // 简短摘要（气泡直取）
	State         string  `json:"state"`
	StartedAt     string  `json:"started_at"`
	FinishedAt    string  `json:"finished_at,omitempty"`
	Elapsed       int64   `json:"elapsed"`            // 秒（完整 task 结构固定字段，0 也下发）
	Progress      float64 `json:"progress,omitempty"` // 0~1（批量节点完成进度）
	Output        string  `json:"output,omitempty"`
	ResultSummary string  `json:"result_summary,omitempty"` // 终态摘要（done）
	Error         string  `json:"error,omitempty"`          // 终态错误（error/cancelled 的 summary 归此）
	Success       bool    `json:"success"`

	// Args 展示用工具参数（随 tasks.* 广播，供前端任务详情渲染）—— **仅 ask_user 节点填充**
	// （追问内容/选项等；I-50）。其余节点的调用参数保留在内部 args（tool-retry 用）不广播，
	// 避免每次任务事件的载荷膨胀。
	Args map[string]any `json:"args,omitempty"`

	// ── DSL 展示字段（DSL-3，[42 §2 (251)/(252)]；61 §3.4 增补，缺省为空 → 与既有载荷逐字节等价）──
	//
	// LoopCurrent / LoopTotal 仅容器节点（kind=dsl_loop/dsl_parallel）：当前轮次（1 起）/ 总轮数
	// （未知可缺省 0）。Steps 仅作业根节点（kind=dsl_job）：扁平步骤执行记录（不随迭代进树）。
	// Shadow = true 表示「执行记录隐藏节点」（本实现选择记录入 steps[]，不产出 shadow 节点）。
	// ReturnKind/Inline/File/Size = `$RETURN` 两态（inline 全文 / file 文件名 + 字节数）。
	LoopCurrent  int             `json:"loop_current,omitempty"`
	LoopTotal    int             `json:"loop_total,omitempty"`
	Steps        []DslStepRecord `json:"steps,omitempty"`
	Shadow       bool            `json:"shadow,omitempty"`
	ReturnKind   string          `json:"return_kind,omitempty"`
	ReturnInline string          `json:"return_inline,omitempty"`
	ReturnFile   string          `json:"return_file,omitempty"`
	ReturnSize   int             `json:"return_size,omitempty"`

	// 内部字段（不广播）
	started  time.Time      // 开始时刻（elapsed 计算）
	gwTaskID string         // gateway 异步任务 id（pending{task_id}；执行侧取消/转后台与完成回报定位）
	args     map[string]any // 工具调用参数（tool-retry 重跑用，不广播）
}

// clone 返回节点快照（广播用；elapsed 实时刷新）。
func (n *TaskNode) clone() *TaskNode {
	c := *n
	if !c.started.IsZero() {
		c.Elapsed = int64(time.Since(c.started).Seconds())
	}
	return &c
}

// terminalTaskState 判断是否终态（done/error/cancelled；防重复广播）。
func terminalTaskState(state string) bool {
	return state == TaskStateDone || state == TaskStateError || state == TaskStateCancelled
}

// taskManager 是 server 任务编排（TaskManager 提升，对齐 21-llm-server）：
// 任务树权威在层（chonkpilot-task，P2 单写者）；本 manager 持运行态视图——节点/级联/批量/
// 状态事件全由 server 维护发布；gateway 只收 tools/call 回报执行结果，不感知任务树/tool_pair/会话。
type taskManager struct {
	srv *Server

	mu       sync.Mutex
	nodes    map[string]*TaskNode // task_id → 节点（树由 ParentID 表达）
	order    []string             // 插入序（终态节点 FIFO 淘汰，maxTaskNodes 保护）
	lastEmit map[string]time.Time // task_id → 上次 tasks.updated 时间（节流 ≤250ms）

	// subParents 是 DSL-3 步骤子会话 → 静态父节点 id 映射（session_id → 容器/作业根 task_id）：
	// 执行记录不建树节点（DSL-3），子会话内产生的工具节点据此挂到该步所属**静态语句节点**
	// （容器 dsl_loop/dsl_parallel，或作业根 dsl_job）。仅内存（与作业同生命周期）。
	subParents map[string]string

	stopCh chan struct{} // 停止信号（后台 cleanupLoop goroutine）
}

// maxTaskNodes 保留任务节点上限（防无限增长；对齐主仓库 TaskManager maxTasks=200）。
const maxTaskNodes = 200

// maxActiveTasks 活跃任务上限（running/pending 非终态，超出时 start 返回错误）。
const maxActiveTasks = 50

// matchInstance 判定任务节点的实例归属是否命中（2026-09-19 缺口 3）：
// instanceID 为空 = 不校验（旧调用方 / 旧语义，行为与引入前一致）；非空 → 节点须同 instance。
// 任务节点一律携带 InstanceID（turn.go / jobdsl.go / domainmcp.go 建节点时从 turn 继承）。
func matchInstance(instanceID, nodeInstance string) bool {
	return instanceID == "" || nodeInstance == instanceID
}

func newTaskManager(s *Server) *taskManager {
	tm := &taskManager{
		srv:        s,
		nodes:      make(map[string]*TaskNode),
		lastEmit:   make(map[string]time.Time),
		subParents: make(map[string]string),
		stopCh:     make(chan struct{}),
	}
	// 后台定期清理终态节点（每 5 分钟）
	go tm.cleanupLoop()
	return tm
}

// cleanupLoop 定期清理终态节点（超过保留时长 5 分钟）。
func (tm *taskManager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			tm.cleanupTerminal()
		case <-tm.stopCh:
			return
		}
	}
}

// stop 停止后台清理 goroutine。
func (tm *taskManager) stop() {
	close(tm.stopCh)
}

// cleanupTerminal 清理已终态超过 5 分钟的节点。
func (tm *taskManager) cleanupTerminal() {
	cutoff := time.Now().Add(-5 * time.Minute)
	tm.mu.Lock()
	defer tm.mu.Unlock()
	var keep []string
	for _, id := range tm.order {
		n, ok := tm.nodes[id]
		if !ok {
			continue
		}
		if terminalTaskState(n.State) {
			if n.FinishedAt != "" {
				ft, err := time.Parse(time.RFC3339, n.FinishedAt)
				if err == nil && ft.Before(cutoff) {
					delete(tm.nodes, id)
					delete(tm.lastEmit, id)
					continue
				}
			}
		}
		keep = append(keep, id)
	}
	tm.order = keep
}

// recoverStaleTasktree 启动/首个 llm-start 遗留清理（R2，与 data-session-cleanup-stale 同触发点）：
// **P2 起并入任务层**（21 §9.2 交付物 ⑥：「库中非终态 → interrupted」只在一处实现，消除重复）——
// 层经既有 data-tasktree-* 面枚举本实例库中非终态（pending/running/awaiting/detached）节点，
// 逐条回写 status=interrupted + finished_at（closed 等既有标记保留），**不自动重跑**。
// 层失败只告警（不阻塞编排；重启恢复尽力而为）。返回标注条数。
func (tm *taskManager) recoverStaleTasktree(instanceID string) int {
	if instanceID == "" || tm.srv.taskLayer == nil {
		return 0
	}
	return tm.srv.taskLayer.Recover(instanceID)
}

// onTaskVerify 处理 **`task-verify`** 方法面（I-87，2026-09-18 用户确认；61 已登记）：载荷
// `{instance_id, top_session, include_closed?}` → 转发到任务层 `VerifyPayload`（复用层
// `Verify(top_session)`：层内运行态热视图 vs 权威表 `tasktree` 行）→ 写回 v.Result
// （`*tasklayer.VerifyResult`：{match, diffs, rows, cached_rows, ...}）。
//
// **只读**、无副作用；失败（载荷非法 / 读取失败）回**协议错误**（mq 收集进 v.Errors），**不 panic**。
// 命名理由：校验需要层内热视图，data 模块单独无法完成 → 不落 `data-*` 域，由**层宿主**注册
// （层在本进程内；主题名 = `task-verify`，与 `task-stop` / `task-background` 同族）。
func (s *Server) onTaskVerify(_ context.Context, _ string, v *mq.Value) error {
	var req tasklayer.VerifyRequest
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		return methodErr("task-verify: 载荷解析失败: " + err.Error())
	}
	res := s.taskLayer.VerifyPayload(&req)
	if res.Error != "" {
		return methodErr("task-verify: " + res.Error)
	}
	v.Result = res
	return nil
}

// markDeleted 记录节点被前端手动关闭：**内存视图移除**（不再广播/不再参与级联）。
// P2：库侧由 persist `data-tasktree-delete` 逻辑删除（closed 标记）+ 层视图同步标记，
// 关闭后的节点不因后续事件复活（层 commit 保留 closed）。
func (tm *taskManager) markDeleted(taskID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	delete(tm.nodes, taskID)
	for i, id := range tm.order {
		if id == taskID {
			tm.order = append(tm.order[:i], tm.order[i+1:]...)
			break
		}
	}
	delete(tm.lastEmit, taskID)
}

// start 建任务节点并广播 tasks.started（state=running；启动后不可变字段由调用方先填）。
// 当**同 instance** 活跃任务数 >= maxActiveTasks 时返回错误（调用方应视为工具执行失败）——
// 2026-09-19 缺口 3：活跃数按 instance 归属统计，一个 instance 打满不再阻塞其他 instance。
// P2：`workdir` 由 instance 绑定解析后随事件载荷增补（21 §7.4）——任务层据此落权威行隔离键；
// 落库由任务层订阅 tasks.started 完成（本方法不自写库）。
func (tm *taskManager) start(n *TaskNode) (*TaskNode, error) {
	n.TaskID = "tk-" + newID()
	n.State = TaskStateRunning
	n.StartedAt = time.Now().UTC().Format(time.RFC3339)
	n.started = time.Now()
	if n.Simplified == "" {
		n.Simplified = n.Name
	}
	if n.WorkDir == "" {
		if rec, ok := tm.srv.im.Lookup(n.InstanceID); ok {
			n.WorkDir = rec.WorkDir // work_dir 随 instance 绑定（对齐 onLLMStart）
		}
	}
	tm.mu.Lock()
	// 活跃任务数检查（running/pending 非终态；限本 instance 归属，缺口 3）
	active := 0
	for _, on := range tm.nodes {
		if !terminalTaskState(on.State) && matchInstance(n.InstanceID, on.InstanceID) {
			active++
		}
	}
	if active >= maxActiveTasks {
		tm.mu.Unlock()
		return nil, fmt.Errorf("活跃任务数已达上限（%d），请等待当前任务完成", maxActiveTasks)
	}
	tm.nodes[n.TaskID] = n
	tm.order = append(tm.order, n.TaskID)
	// 终态节点超出上限 → FIFO 淘汰（运行中不淘汰，放回队尾）
	for len(tm.order) > maxTaskNodes {
		old := tm.order[0]
		tm.order = tm.order[1:]
		on, ok := tm.nodes[old]
		if !ok {
			continue
		}
		if !terminalTaskState(on.State) {
			tm.order = append(tm.order, old)
			break
		}
		delete(tm.nodes, old)
		delete(tm.lastEmit, old)
	}
	tm.mu.Unlock()
	tm.srv.publish("tasks.started", n.clone())
	return n, nil
}

// get 返回节点快照（不存在 → nil）。
func (tm *taskManager) get(taskID string) *TaskNode {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	n, ok := tm.nodes[taskID]
	if !ok {
		return nil
	}
	return n.clone()
}

// setState 状态变更（如 async 转后台 pending）：始终广播 tasks.updated。
func (tm *taskManager) setState(taskID, state string) {
	tm.mu.Lock()
	n, ok := tm.nodes[taskID]
	if !ok {
		tm.mu.Unlock()
		return
	}
	n.State = state
	tm.lastEmit[taskID] = time.Now()
	snap := n.clone()
	tm.mu.Unlock()
	tm.srv.publish("tasks.updated", snap)
}

// update 就地更新节点字段并广播 tasks.updated（派生后才确定的归属字段，
// 如 llm 节点的 session_id/turn_id = 子会话；始终广播，非进度节流路径）。
func (tm *taskManager) update(taskID string, fn func(*TaskNode)) {
	tm.mu.Lock()
	n, ok := tm.nodes[taskID]
	if !ok {
		tm.mu.Unlock()
		return
	}
	fn(n)
	tm.lastEmit[taskID] = time.Now()
	snap := n.clone()
	tm.mu.Unlock()
	tm.srv.publish("tasks.updated", snap)
}

// setGwTask 登记 gateway 异步任务 id（gateway 返回 pending 后；执行侧取消/转后台与完成回报定位）
// 并**绑定到任务层的执行句柄**（21 §9.2 交付物 ⑤：层持 exec → 取消时回调执行侧）。
func (tm *taskManager) setGwTask(taskID, gwTaskID string) {
	tm.mu.Lock()
	n, ok := tm.nodes[taskID]
	if ok {
		n.gwTaskID = gwTaskID
	}
	var exec tasklayer.Exec
	if ok {
		exec = tasklayer.Exec{GWTaskID: gwTaskID, InstanceID: n.InstanceID, Tool: n.Tool}
	}
	tm.mu.Unlock()
	if ok && tm.srv.taskLayer != nil {
		tm.srv.taskLayer.BindExec(taskID, exec)
	}
}

// nodeIn 按 task_id 取节点并校验实例归属（缺口 3）：instanceID 空 = 不校验（旧语义）；
// 节点不存在或归属不符 → nil。用于「已知调用方实例」的直取路径（不跨 instance 命中）。
func (tm *taskManager) nodeIn(instanceID, taskID string) *TaskNode {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	n, ok := tm.nodes[taskID]
	if !ok || !matchInstance(instanceID, n.InstanceID) {
		return nil
	}
	return n.clone()
}

// findByGwTask 按 gateway 任务 id 找节点快照（异步完成回报定位；未登记 → nil）。
// instanceID 非空 → 只命中**该 instance 归属**的节点（缺口 3：回报按实例隔离）。
func (tm *taskManager) findByGwTask(instanceID, gwTaskID string) *TaskNode {
	if gwTaskID == "" {
		return nil
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for _, n := range tm.nodes {
		if n.gwTaskID != gwTaskID || !matchInstance(instanceID, n.InstanceID) {
			continue
		}
		return n.clone()
	}
	return nil
}

// findByToolCall 按 LLM tool-call id 找任务节点（G-17 转后台门控反查工具名；未命中 → nil）。
// instanceID 非空 → 只命中该 instance 归属的节点（缺口 3）。
func (tm *taskManager) findByToolCall(instanceID, toolCallID string) *TaskNode {
	if toolCallID == "" {
		return nil
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for i := len(tm.order) - 1; i >= 0; i-- {
		n, ok := tm.nodes[tm.order[i]]
		if ok && n.ToolCallID == toolCallID && matchInstance(instanceID, n.InstanceID) {
			return n.clone()
		}
	}
	return nil
}

// find 按 task_id 找节点（tool-retry 直取定位；未登记 → nil）。
func (tm *taskManager) find(taskID string) *TaskNode {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	n, ok := tm.nodes[taskID]
	if !ok {
		return nil
	}
	return n.clone()
}

// latestLLMSessionNode 按（top_session, session）定位子会话对应的父任务节点：
//   - **DSL-3 优先**：`subParents[session]`（由 jobdsl.llmAction 登记）→ 该步所属**静态语句节点**
//     （容器 dsl_loop/dsl_parallel 或作业根 dsl_job）。DSL-3 起执行记录不建树节点，子会话内产生的
//     工具/任务节点据此挂到静态语句节点之下（任务树拓扑：语句节点 → 该步工具）。
//   - 回落（既有语义）：kind=llm 且 SessionID=子会话 的节点（jobdsl 历史 / 单测手工构造）。
//
// 取最近创建（order 插入序逆序首个）；未命中 → nil（退化为顶层，异常窗口见 turn.taskNodeBase 注释）。
// instanceID 非空 → 只命中该 instance 归属的节点（缺口 3：不跨 instance 挂错父节点）。
func (tm *taskManager) latestLLMSessionNode(instanceID, topSession, session string) *TaskNode {
	if session == "" {
		return nil
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if pid := tm.subParents[session]; pid != "" {
		if n, ok := tm.nodes[pid]; ok && matchInstance(instanceID, n.InstanceID) &&
			(topSession == "" || n.TopSession == topSession) {
			return n.clone()
		}
	}
	for i := len(tm.order) - 1; i >= 0; i-- {
		n, ok := tm.nodes[tm.order[i]]
		if !ok || (n.Kind != TaskKindLLM && n.Kind != TaskKindDslStep) || n.SessionID != session {
			continue
		}
		if !matchInstance(instanceID, n.InstanceID) {
			continue
		}
		if topSession != "" && n.TopSession != topSession {
			continue
		}
		return n.clone()
	}
	return nil
}

// bindSubSession 登记 DSL-3 步骤子会话 → 静态父节点 id（jobdsl.llmAction 在建子会话前调用）：
// 子会话内产生的工具节点据此挂到该步所属静态语句节点（容器或作业根）。
func (tm *taskManager) bindSubSession(session, parentID string) {
	if session == "" || parentID == "" {
		return
	}
	tm.mu.Lock()
	tm.subParents[session] = parentID
	tm.mu.Unlock()
}

// unbindSubSessions 清理某作业的全部子会话映射（作业结束时调用；防 map 无界增长）。
func (tm *taskManager) unbindSubSessions(sessions []string) {
	if len(sessions) == 0 {
		return
	}
	tm.mu.Lock()
	for _, s := range sessions {
		delete(tm.subParents, s)
	}
	tm.mu.Unlock()
}

// findRetryTarget 定位 tool-retry 目标节点（msg-ref §4.2：消息区「重试」最后一条 interrupted
// 工具 pair）：候选须属该 session（turn/toolCallID 给定则进一步精确匹配）且状态为 interrupted
// （可重试；done/error/cancelled = 已有结果/主动取消不重跑，running/pending = 仍在执行不重试）；
// 多候选取最近启动（order 逆序首个，同前端"最后一条 interrupted"语义）。
// 返回节点快照（含内部 args，重跑用）；未命中 → nil。
// instanceID 非空 → 只命中该 instance 归属的节点（缺口 3：不跨 instance 重跑他人任务）。
func (tm *taskManager) findRetryTarget(instanceID, session, turn, toolCallID string) *TaskNode {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for i := len(tm.order) - 1; i >= 0; i-- {
		n, ok := tm.nodes[tm.order[i]]
		if !ok || n.SessionID != session || n.State != TaskStateInterrupted {
			continue
		}
		if !matchInstance(instanceID, n.InstanceID) {
			continue
		}
		if turn != "" && n.TurnID != turn {
			continue
		}
		if toolCallID != "" && n.ToolCallID != toolCallID {
			continue
		}
		return n.clone()
	}
	return nil
}

// restoreStale tool-retry 重启恢复：把从库还原的节点登记回内存任务表（沿用原 task_id，
// 保证重跑终态广播/落库落在原节点）。不广播——随后 retryReset 会广播 running。
// 节点已在内存（幂等）→ 跳过。
func (tm *taskManager) restoreStale(n *TaskNode) {
	if n == nil || n.TaskID == "" {
		return
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if _, ok := tm.nodes[n.TaskID]; ok {
		return
	}
	n.State = TaskStateInterrupted // 遗留可重试语义（retryReset 置 running）
	n.started = time.Now()
	if n.StartedAt == "" {
		n.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	tm.nodes[n.TaskID] = n
	tm.order = append(tm.order, n.TaskID)
}

// retryReset 把 interrupted 节点重置为 running（tool-retry 重跑：复用原任务节点，不新建，
// 不动 args/树结构，重跑终态仍走原节点广播）；非 interrupted → false（不可重试）。
// 广播 tasks.updated（前端任务树/气泡可见重新运行）；层侧状态单调校验允许 interrupted → running
// （21 §9.2：interrupted 为可恢复终态）。
func (tm *taskManager) retryReset(taskID string) bool {
	tm.mu.Lock()
	n, ok := tm.nodes[taskID]
	if !ok || n.State != TaskStateInterrupted {
		tm.mu.Unlock()
		return false
	}
	n.State = TaskStateRunning
	n.FinishedAt = ""
	n.Error = ""
	n.ResultSummary = ""
	n.Success = false
	n.started = time.Now()
	snap := n.clone()
	tm.mu.Unlock()
	tm.srv.publish("tasks.updated", snap)
	return true
}

// progress 进度/输出更新：节流广播 tasks.updated（≤250ms，避免刷爆前端通道）。
func (tm *taskManager) progress(taskID string, progress float64, output string) {
	tm.mu.Lock()
	n, ok := tm.nodes[taskID]
	if !ok {
		tm.mu.Unlock()
		return
	}
	n.Progress = progress
	if output != "" {
		n.Output = output
	}
	now := time.Now()
	if now.Sub(tm.lastEmit[taskID]) < 250*time.Millisecond {
		tm.mu.Unlock()
		return
	}
	tm.lastEmit[taskID] = now
	snap := n.clone()
	tm.mu.Unlock()
	tm.srv.publish("tasks.updated", snap)
}

// done 终态（done/error/cancelled）：写 finished_at/elapsed/success → 广播 tasks.done（完整结构）。
// 幂等：已终态不重复广播。落库由任务层订阅 tasks.done 完成（本方法不自写库）。
func (tm *taskManager) done(taskID, state, summary, errMsg string) {
	tm.mu.Lock()
	n, ok := tm.nodes[taskID]
	if !ok {
		tm.mu.Unlock()
		return
	}
	if terminalTaskState(n.State) {
		tm.mu.Unlock()
		return
	}
	n.State = state
	n.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	if !n.started.IsZero() {
		n.Elapsed = int64(time.Since(n.started).Seconds())
	}
	n.Success = state == TaskStateDone
	if errMsg != "" {
		n.Error = errMsg
	}
	if summary != "" {
		if state == TaskStateDone {
			n.ResultSummary = summary
		} else {
			n.Error = summary // error/cancelled：摘要归入 error 字段（§3.3 result_summary/error）
		}
	}
	snap := n.clone()
	tm.mu.Unlock()
	tm.srv.publish("tasks.done", snap)
}

// subtree 返回该节点及全部后代（级联定位：沿 parent_id 遍历）。
// instanceID 非空 → 根与后代都须同 instance（缺口 3：不跨 instance 级联到他人节点）。
func (tm *taskManager) subtree(instanceID, rootID string) []*TaskNode {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	var out []*TaskNode
	if n, ok := tm.nodes[rootID]; ok && matchInstance(instanceID, n.InstanceID) {
		out = append(out, n.clone())
	}
	var walk func(id string)
	walk = func(id string) {
		for _, n := range tm.nodes {
			if n.ParentID == id && matchInstance(instanceID, n.InstanceID) {
				out = append(out, n.clone())
				walk(n.TaskID)
			}
		}
	}
	walk(rootID)
	return out
}

// cancelSubtree 用户取消（task-stop）：**状态判定以层为权威**（21 §9.2 交付物 ⑤）——
// 层判定该子树中「非不可逆终态」的节点集合（`CancelSubtree`），并**由层回调执行侧**
// （`Options.OnCancelExec` → 进程内 sink `CancelExec` → gateway `tm.cancel` → onTaskCancel →
// provider.Invalidate；层持 exec 句柄，21 §8.1-6）；本方法只把层认可的节点标 cancelled 并逐个
// 广播 tasks.done（事件名 / 载荷不变，cascade 沿 parent_id 下发）。返回取消节点数。
//
// 层对该根节点一无所知（跨进程未登记 / 无数据面）时**退化为内存视图判定**（既有行为等价），
// 并直接下发执行侧取消（不经层回调）。
// instanceID 非空 → 只取消该 instance 归属的子树节点（缺口 3：不跨 instance 取消他人任务）。
func (tm *taskManager) cancelSubtree(instanceID, rootID string) int {
	nodes := tm.subtree(instanceID, rootID)
	if len(nodes) == 0 {
		return 0
	}
	topSession := ""
	for _, n := range nodes {
		if n.TaskID == rootID {
			topSession = n.TopSession
			break
		}
	}
	var allowed map[string]bool
	if tm.srv.taskLayer != nil {
		if ids := tm.srv.taskLayer.CancelSubtree(instanceID, rootID, topSession); ids != nil {
			allowed = make(map[string]bool, len(ids))
			for _, id := range ids {
				allowed[id] = true
			}
		}
	}
	cancelled := 0
	for _, n := range nodes {
		if allowed != nil {
			if !allowed[n.TaskID] {
				continue // 层为准：层判定不可取消（不可逆终态）→ 不重复广播
			}
		} else if terminalTaskState(n.State) {
			continue // 层不可知 → 内存视图判定（既有行为）
		}
		if allowed == nil && n.gwTaskID != "" {
			// 层不可知兜底：由本层直接下发执行侧取消（既有路径；2026-09-18 起走进程内 sink）
			if sink := tm.srv.execSink; sink != nil {
				if ok, err := sink.CancelExec(n.InstanceID, n.gwTaskID); err != nil || !ok {
					taskLayerLogf("执行侧取消失败（task=%s gw_task=%s）: ok=%v err=%v", n.TaskID, n.gwTaskID, ok, err)
				}
			}
		}
		tm.done(n.TaskID, TaskStateCancelled, "", "")
		cancelled++
	}
	return cancelled
}

// cancelByTurn 取消某轮次在飞的**全部**任务节点（I-35：llm-cancel 级联，不再只终止轮次）。
// 按 (instance, TurnID) 归属定位非终态节点 → 逐个 cancelSubtree（含其子树；done 幂等，重叠子树
// 不重复广播）。instanceID 非空 → 只命中该 instance（缺口 3：同名 turn 跨 instance 不互相取消）。
// 返回取消节点数（执行侧取消经层回调下发，不再逐个回传 gateway task id）。
func (tm *taskManager) cancelByTurn(instanceID, turnID string) int {
	if turnID == "" {
		return 0
	}
	tm.mu.Lock()
	var targets []string
	for id, n := range tm.nodes {
		if n.TurnID == turnID && !terminalTaskState(n.State) && matchInstance(instanceID, n.InstanceID) {
			targets = append(targets, id)
		}
	}
	tm.mu.Unlock()
	var total int
	for _, id := range targets {
		total += tm.cancelSubtree(instanceID, id)
	}
	return total
}

// taskKindOf 工具 → 任务节点 kind（ask_user / DSL 作业 / 普通工具）。
// llm 型（llm_run）根节点 kind = **dsl_job**（DSL-3 展示：作业根，前端 isDslJob 判定入口）。
func taskKindOf(tool string) string {
	switch {
	case isAskTool(tool):
		return TaskKindAskUser
	case isLLMTool(tool):
		return TaskKindDslJob
	default:
		return TaskKindTool
	}
}

// taskDisplay 任务节点展示字段（标题取值优先级，对齐 34-任务 / I-38）：
//
//	① 调用参数注入的展示名 tool_call_display_name（gateway 注入、LLM 必填）——最优先（name/purpose）；
//	② llm 型（llm_run）显式 title 参数（契约可选、语义比通用展示名更明确 → 覆盖 ①）；
//	③ 回退工具契约名（调用方须传 **契约名**：网关暴露名 self_<契约名> 已剥前缀——turn.go 与
//	   domainNode 同源，保证无注入时标题不出现 self_ 前缀）。
//
// simplified=简短摘要（气泡直取）。返回 (name, purpose, simplified)。
// 展示名取自 gateway 注入的 tool_call_display_name 参数（2026-09-11 由 purpose 更名，避免与
// 工具自有 purpose——如 mcp_find 的任务目标——冲突）。
func taskDisplay(tool string, args map[string]any) (string, string, string) {
	purpose, _ := args["tool_call_display_name"].(string) // ① 注入展示名
	name := purpose
	if name == "" {
		name = tool // ③ 回退工具契约名
	}
	var simplified string
	switch {
	case isAskTool(tool):
		simplified = askQuestion(args)
	case isLLMTool(tool):
		// ② llm 型显式 title 更明确，覆盖注入展示名
		if title, ok := args["title"].(string); ok && title != "" {
			name = title
		}
		simplified = name
	default:
		simplified = purpose
	}
	if simplified == "" {
		simplified = name
	}
	return name, purpose, simplified
}

// ── 任务层窄接口适配（P3：gateway 执行态纳入层 + 层 → 执行侧控制回调）────────────
//
// gateway 包内声明窄接口（避免 gateway → task 反向依赖），由本处（装配方）把**任务层**与
// **执行池（gateway）**适配后注入 `mcpgateway.Params.ExecSink`（见 server.go New）。
// **不走 MQ** → 61 消息面零变更（21 §8.1-7）；层 / 执行池未就绪（DisableMCP / 库内 /
// gateway 单体）→ 对应方法返回明确错误或不动作，行为与引入前等价。
//
// 2026-09-18（用户决定）：`mcp-tasks-cancel` / `mcp-tools-background` 方法面移除 →
// **层 → 执行侧**的取消/转后台改由本适配器进程内直调 gateway 公开方法
// （`CancelExec` / `DetachExec`，复用其 `tm.cancel` / `tm.detach`）。

// taskExecSink 把执行态上报 / 状态查询转交任务层（同进程直调，天然同步），
// 并把取消/转后台转交 gateway 执行池。
type taskExecSink struct {
	layer *tasklayer.Layer
	gw    execController // 执行池句柄（RB-5 L4：窄接口；New 中 gateway 建好后回填；nil = 无执行池）
}

// OnExecState gateway 执行态 → 任务层（P3-①：字段同口径直接转交；层内失败只告警、无返回值）。
func (s *taskExecSink) OnExecState(ev mcpgateway.ExecState) {
	if s.layer == nil {
		return
	}
	s.layer.OnExecState(tasklayer.ExecState{
		GWTaskID: ev.GWTaskID, InstanceID: ev.InstanceID, TopSession: ev.TopSession,
		Tool: ev.Tool, ToolCallID: ev.ToolCallID,
		WorkDir: ev.WorkDir, ProviderKey: ev.ProviderKey,
		Phase: ev.Phase, State: ev.State, Message: ev.Message,
		Detail: ev.Detail, At: ev.At,
	})
}

// ExecStateOf 按 gateway 执行 id 查层**权威状态**（P3-②；未登记 → false）。
func (s *taskExecSink) ExecStateOf(gwTaskID string) (string, bool) {
	if s.layer == nil {
		return "", false
	}
	return s.layer.ExecStateOf(gwTaskID)
}

// CancelExec 层 → 执行侧取消（交付 2）：转调 gateway 执行池取消（含已 detached 的后台任务）。
// **带 instance 归属**（缺口 2：执行池按 instance 分桶 → 只在本 instance 桶内定位）。
// 执行池未就绪 → 明确错误、不动作。
func (s *taskExecSink) CancelExec(instanceID, gwTaskID string) (bool, error) {
	if s.gw == nil {
		return false, fmt.Errorf("执行池不可用（未注入 gateway），无法取消 %s", gwTaskID)
	}
	return s.gw.CancelExec(instanceID, gwTaskID)
}

// DetachExec 层 → 执行侧转后台（交付 2）：转调 gateway 执行池解绑（idOrCall = gw task id 或
// tool_call_id）。**带 instance 归属**（缺口 2：只在本 instance 桶内定位）。
// 执行池未就绪 → 明确错误、不动作。
func (s *taskExecSink) DetachExec(instanceID, idOrCall string) (string, error) {
	if s.gw == nil {
		return "", fmt.Errorf("执行池不可用（未注入 gateway），无法转后台 %s", idOrCall)
	}
	return s.gw.DetachExec(instanceID, idOrCall)
}
