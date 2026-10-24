// 任务编排（宿主适配）：任务树纯逻辑（建节点 / 追加 / 状态推进 / 取消级联 / 终态收敛 /
// 进度统计 / 展示映射）已下沉到纯 lib 组件 `chonkpilot-session`（`src/lib/chonkpilot-session/tasks.go`
// 与 `task_tools.go`）。本文件只剩**宿主适配**：
//
//   - 端口实现：事件广播（tasks.started/updated/done）、任务层权威读（取消判定 / 启动恢复）、
//     执行侧取消（gateway 执行池）、work_dir 解析（instance.Manager）；
//   - 后台清理 goroutine（每 5 分钟清理终态节点）；
//   - 任务层窄接口适配（`task-verify` 方法面 / 层 → 执行侧控制回调）。
//
// 本文件不得再写一份任务树逻辑；MQ 主题与 payload（61 §3.4）零变更。
//
// **P2 单写者**：tasktree 权威行由 **chonkpilot-task 任务层**订阅同一批事件后同步落库
// （`chonkpilot-task/layer.go`）；本层内存视图为运行态视图（含 gateway task id / args 等
// 不落库字段）。控制面（取消）以层状态为权威并由层回调执行侧（21 §9.2 交付物 ⑤）。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/lsong98sh/chonkpilot-session"
	tasklayer "github.com/chonkpilot/chonkpilot-task"
	mcpgateway "github.com/lsong98sh/chonkpilot-mcp-gateway/gateway"
)

// 任务节点类型 / 状态 / DSL 展示类型（定义在组件包；别名保持调用点与载荷口径不变）。
type (
	TaskNode      = session.TaskNode
	DslStepRecord = session.DslStepRecord
)

const (
	TaskKindTool    = session.TaskKindTool
	TaskKindLLM     = session.TaskKindLLM
	TaskKindAskUser = session.TaskKindAskUser

	TaskKindDslJob      = session.TaskKindDslJob
	TaskKindDslLoop     = session.TaskKindDslLoop
	TaskKindDslParallel = session.TaskKindDslParallel
	TaskKindDslStep     = session.TaskKindDslStep

	TaskStatePending     = session.TaskStatePending
	TaskStateRunning     = session.TaskStateRunning
	TaskStateDone        = session.TaskStateDone
	TaskStateError       = session.TaskStateError
	TaskStateCancelled   = session.TaskStateCancelled
	TaskStateInterrupted = session.TaskStateInterrupted
)

// terminalTaskState 判断是否终态（done/error/cancelled；防重复广播）。
func terminalTaskState(state string) bool { return session.TerminalState(state) }

// taskKindOf 工具 → 任务节点 kind（ask_user / 普通工具）。
func taskKindOf(tool string) string { return session.TaskKindOf(tool) }

// taskDisplay 任务节点展示字段（标题取值优先级，对齐 34-任务 / I-38）。
func taskDisplay(tool string, args map[string]any) (string, string, string) {
	return session.TaskDisplay(tool, args)
}

// taskManager 是 server 任务编排的**宿主适配层**：任务树运行态视图与全部树操作在组件
// （`session.TaskTree`），本结构只持端口实现与后台清理 goroutine。
type taskManager struct {
	srv  *Server
	tree *session.TaskTree

	stopCh   chan struct{} // 停止信号（后台 cleanupLoop goroutine）
	stopOnce sync.Once     // 停止幂等守卫（B-04：Stop 可能被多处/多次调用，close 通道不可重复）
}

func newTaskManager(s *Server) *taskManager {
	tm := &taskManager{
		srv:    s,
		stopCh: make(chan struct{}),
	}
	tm.tree = session.NewTaskTree(session.TaskTreeOptions{
		Emitter: taskEventEmitter{srv: s},
		Store:   taskLayerStore{srv: s},
		Exec:    func() session.TaskExecSink { return taskExecPortOf(s) },
		WorkDir: taskWorkDirSource{srv: s},
		Logf:    taskLayerLogf,
	})
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
			tm.tree.CleanupTerminal()
		case <-tm.stopCh:
			return
		}
	}
}

// stop 停止后台清理 goroutine（B-04：sync.Once 守卫，重复调用安全）。
func (tm *taskManager) stop() {
	tm.stopOnce.Do(func() { close(tm.stopCh) })
}

// ─── 任务树操作（薄适配：实现全部在组件）────────────────────────────

// recoverStaleTasktree 启动/首个 llm-start 遗留清理（R2，与 data-session-cleanup-stale 同触发点）：
// **P2 起并入任务层**（21 §9.2 交付物 ⑥：「库中非终态 → interrupted」只在一处实现）——
// 层经既有 data-tasktree-* 面枚举本实例库中非终态节点，逐条回写 status=interrupted +
// finished_at（closed 等既有标记保留），**不自动重跑**。返回标注条数。
func (tm *taskManager) recoverStaleTasktree(instanceID string) int {
	return tm.tree.RecoverStale(instanceID)
}

// markDeleted 记录节点被前端手动关闭：**内存视图移除**（不再广播/不再参与级联）。
func (tm *taskManager) markDeleted(taskID string) { tm.tree.MarkDeleted(taskID) }

// start 建任务节点并广播 tasks.started（state=running）。
func (tm *taskManager) start(n *TaskNode) (*TaskNode, error) { return tm.tree.Start(n) }

// get 返回节点快照（不存在 → nil）。
func (tm *taskManager) get(taskID string) *TaskNode { return tm.tree.Get(taskID) }

// setState 状态变更（如 async 转后台 pending）：始终广播 tasks.updated。
func (tm *taskManager) setState(taskID, state string) { tm.tree.SetState(taskID, state) }

// update 就地更新节点字段并广播 tasks.updated。
func (tm *taskManager) update(taskID string, fn func(*TaskNode)) { tm.tree.Update(taskID, fn) }

// setGwTask 登记 gateway 异步任务 id 并**绑定到任务层的执行句柄**（21 §9.2 交付物 ⑤：
// 层持 exec → 取消时回调执行侧）。
func (tm *taskManager) setGwTask(taskID, gwTaskID string) {
	binding, ok := tm.tree.SetGWTask(taskID, gwTaskID)
	if ok && tm.srv.taskLayer != nil {
		tm.srv.taskLayer.BindExec(taskID, tasklayer.Exec{
			GWTaskID: binding.GWTaskID, InstanceID: binding.InstanceID, Tool: binding.Tool,
		})
	}
}

// nodeIn 按 task_id 取节点并校验实例归属（缺口 3）。
func (tm *taskManager) nodeIn(instanceID, taskID string) *TaskNode {
	return tm.tree.NodeIn(instanceID, taskID)
}

// findByGwTask 按 gateway 任务 id 找节点快照（异步完成回报定位；未登记 → nil）。
func (tm *taskManager) findByGwTask(instanceID, gwTaskID string) *TaskNode {
	return tm.tree.FindByGWTask(instanceID, gwTaskID)
}

// findByToolCall 按 LLM tool-call id 找任务节点（G-17 转后台门控反查工具名；未命中 → nil）。
func (tm *taskManager) findByToolCall(instanceID, toolCallID string) *TaskNode {
	return tm.tree.FindByToolCall(instanceID, toolCallID)
}

// find 按 task_id 找节点（tool-retry 直取定位；未登记 → nil）。
func (tm *taskManager) find(taskID string) *TaskNode { return tm.tree.Find(taskID) }

// latestLLMSessionNode 按（top_session, session）定位子会话对应的父任务节点。
func (tm *taskManager) latestLLMSessionNode(instanceID, topSession, session string) *TaskNode {
	return tm.tree.LatestLLMSessionNode(instanceID, topSession, session)
}

// bindSubSession 登记 DSL-3 步骤子会话 → 静态父节点 id。
func (tm *taskManager) bindSubSession(session, parentID string) {
	tm.tree.BindSubSession(session, parentID)
}

// unbindSubSessions 清理某作业的全部子会话映射（作业结束时调用；防 map 无界增长）。
func (tm *taskManager) unbindSubSessions(sessions []string) { tm.tree.UnbindSubSessions(sessions) }

// findRetryTarget 定位 tool-retry 目标节点（msg-ref §4.2；未命中 → nil）。
func (tm *taskManager) findRetryTarget(instanceID, session, turn, toolCallID string) *TaskNode {
	return tm.tree.FindRetryTarget(instanceID, session, turn, toolCallID)
}

// restoreStale tool-retry 重启恢复：把从库还原的节点登记回内存任务表（沿用原 task_id）。
func (tm *taskManager) restoreStale(n *TaskNode) { tm.tree.RestoreStale(n) }

// retryReset 把 interrupted 节点重置为 running（tool-retry 重跑；复用原任务节点）。
func (tm *taskManager) retryReset(taskID string) bool { return tm.tree.RetryReset(taskID) }

// progress 进度/输出更新：节流广播 tasks.updated（≤250ms）。
func (tm *taskManager) progress(taskID string, progress float64, output string) {
	tm.tree.Progress(taskID, progress, output)
}

// done 终态（done/error/cancelled）：广播 tasks.done（完整结构）；已终态不重复广播。
func (tm *taskManager) done(taskID, state, summary, errMsg string) {
	tm.tree.Done(taskID, state, summary, errMsg)
}

// subtree 返回该节点及全部后代（级联定位：沿 parent_id 遍历）。
func (tm *taskManager) subtree(instanceID, rootID string) []*TaskNode {
	return tm.tree.Subtree(instanceID, rootID)
}

// cancelSubtree 用户取消（task-stop）：状态判定以层为权威；返回取消节点数。
func (tm *taskManager) cancelSubtree(instanceID, rootID string) int {
	return tm.tree.CancelSubtree(instanceID, rootID)
}

// cancelByTurn 取消某轮次在飞的**全部**任务节点（I-35：llm-cancel 级联）；返回取消节点数。
func (tm *taskManager) cancelByTurn(instanceID, turnID string) int {
	return tm.tree.CancelByTurn(instanceID, turnID)
}

// ─── 端口实现（副作用全部落在宿主）────────────────────────────────

// taskEventEmitter 实现 `session.TaskEmitter`：节点快照 → tasks.* 广播（载荷 61 §3.4 不变）。
type taskEventEmitter struct{ srv *Server }

func (e taskEventEmitter) Started(node session.TaskNode) { e.srv.publish("tasks.started", node) }
func (e taskEventEmitter) Updated(node session.TaskNode) { e.srv.publish("tasks.updated", node) }
func (e taskEventEmitter) Done(node session.TaskNode)    { e.srv.publish("tasks.done", node) }

// taskLayerStore 实现 `session.TaskStore`：任务层权威读（取消判定）+ 启动恢复收敛。
// 层在 `Server.New` 中后于 taskManager 创建 → 每次调用动态取（不缓存句柄）。
type taskLayerStore struct{ srv *Server }

// AuthorizeCancel 层判定的可取消集合（nil = 层不可知）。
func (s taskLayerStore) AuthorizeCancel(instanceID, rootID, topSession string) []string {
	if s.srv == nil || s.srv.taskLayer == nil {
		return nil
	}
	return s.srv.taskLayer.CancelSubtree(instanceID, rootID, topSession)
}

// RecoverStale 启动恢复：库中非终态 → interrupted，返回标注条数。
func (s taskLayerStore) RecoverStale(instanceID string) int {
	if s.srv == nil || s.srv.taskLayer == nil {
		return 0
	}
	return s.srv.taskLayer.Recover(instanceID)
}

// taskWorkDirSource 实现 `session.InstanceWorkDir`：work_dir 随 instance 绑定解析。
type taskWorkDirSource struct{ srv *Server }

func (w taskWorkDirSource) WorkDir(instanceID string) (string, bool) {
	if w.srv == nil || w.srv.im == nil {
		return "", false
	}
	rec, ok := w.srv.im.Lookup(instanceID)
	if !ok {
		return "", false
	}
	return rec.WorkDir, true
}

// taskExecPortOf 取执行池控制面端口（执行池在 `Server.New` 中后于 taskManager 装配；
// nil = 未注入 → 执行侧不可取消）。
func taskExecPortOf(s *Server) session.TaskExecSink {
	if s == nil || s.execSink == nil {
		return nil
	}
	return taskExecPort{sink: s.execSink}
}

// taskExecPort 实现 `session.TaskExecSink`（转调 gateway 执行池）。
type taskExecPort struct{ sink mcpgateway.ExecSink }

func (p taskExecPort) CancelExec(instanceID, gwTaskID string) (bool, error) {
	return p.sink.CancelExec(instanceID, gwTaskID)
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
