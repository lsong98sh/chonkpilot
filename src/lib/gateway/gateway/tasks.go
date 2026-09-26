// execPool：异步执行生命周期（pending/running/done/error/cancelled）。
// 对齐 26-mcp-gateway：执行归属（instance_id + session/turn）、
// 完成回报回调（tasks.done 仅回报 server，不对前端广播——任务编排提升 server，gateway 纯执行层）。
//
// 命名收口（RB-1，2026-09-21）：本文件是 gateway 的**执行池**（执行控制：超时/pending/后台
// goroutine/取消），与任务层（`src/lib/task`，任务树/级联/持久化）是两层不同概念，故内部标识符
// 与任务层区分：`execPool` / `ExecTask` / `execs` / `ex-%04d`；**JSON 字段 `task_id` 保持不动**
// （跨 [61-消息一览] 消息面，零变更）。
package mcpgateway

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ExecTask 是异步执行运行态（网关执行池所有权的权威状态）。
type ExecTask struct {
	ID            string              `json:"task_id"`
	Tool          string              `json:"tool"`
	State         string              `json:"state"` // pending / running / done / error / cancelled
	OwnerInstance string              `json:"instance_id,omitempty"`
	Session       string              `json:"session,omitempty"`
	Turn          string              `json:"turn,omitempty"`
	WorkDir       string              `json:"work_dir,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
	StartedAt     time.Time           `json:"started_at,omitempty"`
	DoneAt        time.Time           `json:"done_at,omitempty"`
	Result        *mcp.CallToolResult `json:"result,omitempty"`
	Error         string              `json:"error,omitempty"`
	ToolCallID    string              `json:"tool_call_id,omitempty"` // 关联 LLM tool-call id（manual 解绑定位）
	TopSession    string              `json:"top_session,omitempty"`  // 任务树归属主会话（I-90：随 mcp-tasks-report 回报）
	Parent        string              `json:"parent,omitempty"`       // 已登记的 llm 侧节点 task_id（I-90：随回报下发）
	ProviderKey   string              `json:"-"`                      // 归属 provider（取消 → 作废/重建）
	cancel        context.CancelFunc

	// 任务化同步裁决内部状态（不序列化到任何消息）：
	asyncReport bool          // 终态是否经 onDone 回报 mcp-tasks-report（always 恒 true；任务化解绑后 true）
	detached    bool          // 是否已解绑转后台
	allowDetach bool          // 是否允许 detach（manual/auto；never = false → options 无 detach）
	allowWait   bool          // 是否允许 wait（never；manual = false → options 无 wait）
	doneCh      chan struct{} // 终态信号（任务化同步等待据此醒来）
	detachCh    chan struct{} // 解绑信号（tools/background 或 auto 阈值）
	waitCh      chan struct{} // 等待完成信号（mcp-tools-wait：撤销超时、继续等）
	doneOnce    sync.Once
	waitOnce    sync.Once

	// 「超时待用户裁决」态（manual/never 到超时点进入；I-57：供前端裁决条恢复；
	// 2026-09-18 起控制面方法面已移除 → 状态查询以任务层为准）。
	// 裁决（wait/detach/cancel）或任务终态后清空。
	awaitingTimeoutS float64
	awaitingOptions  []string
}

// ── 执行态上报（P3-①：gateway 执行态纳入任务层）────────────────────────────
//
// 执行态经**层内 API 直调**（`ExecSink.OnExecState`，同进程同步调用）落库 —— **不新增 MQ 主题**
// （61 消息面零变更，见 chonkpilot-task/exec.go · 21 §9.3 P3）。未注入 sink（库内 / 单测 /
// gateway 单体 exe）→ 全部 no-op，行为与引入前逐字节等价。

// 执行态上报点（phase）。
const (
	ExecPhaseStarted   = "started"   // doCall spawn 后（执行启动）
	ExecPhaseDetached  = "detached"  // 转后台（auto 阈值命中 / manual tools/background）
	ExecPhaseAwaiting  = "awaiting"  // 超时待用户裁决
	ExecPhaseDone      = "done"      // 终态：成功（不经本通道；保留常量供层侧同名映射）
	ExecPhaseError     = "error"     // 异常：上游调用失败 / 取消后 respawn 失败（带简短 message）
	ExecPhaseCancelled = "cancelled" // 终态：取消（不经本通道；保留常量供层侧同名映射）
)

// ExecState 是 gateway → 任务层的**执行态**载荷（层内直调，不走 MQ）：
// `gw_task_id` = gateway 执行 id；`state` = gateway 执行池状态；`workdir` / `provider_key` = 隔离键
// 来源与归属 provider；`message` = 异常摘要（error 路径，简短、不含堆栈）；`detail` = 上报点明细
// （detached 的 trigger/threshold_s、awaiting 的 timeout_s/options）→ 层写入 `exec_json`。
type ExecState struct {
	GWTaskID    string         `json:"gw_task_id"`
	InstanceID  string         `json:"instance_id,omitempty"`
	TopSession  string         `json:"top_session,omitempty"`
	Tool        string         `json:"tool,omitempty"`
	ToolCallID  string         `json:"tool_call_id,omitempty"`
	WorkDir     string         `json:"workdir,omitempty"`
	ProviderKey string         `json:"provider_key,omitempty"`
	Phase       string         `json:"phase"`
	State       string         `json:"state,omitempty"`
	Message     string         `json:"message,omitempty"`
	Detail      map[string]any `json:"detail,omitempty"`
	At          string         `json:"at,omitempty"`
}

// ExecSink 是 gateway 持有的**任务层窄接口**（在 gateway 包内声明，避免反向依赖；由装配方
// （chonkpilot-llm/server）注入层或其适配器；未注入 → 全部 no-op）：
//   - OnExecState：执行态上报（P3-①）；层失败只告警（无返回值，绝不影响 gateway 调用路径）。
//   - ExecStateOf：按 gateway 执行 id 查层**权威状态**（P3-②；第二个返回值 false = 层未登记该执行
//     → 调用方回落自身视图判定，行为等价）。
//   - CancelExec / DetachExec：**层 → 执行侧**控制回调（2026-09-18：`mcp-tasks-cancel` /
//     `mcp-tools-background` 方法面移除后，取消/转后台改由层 + 进程内 sink 提供）。
//     语义 = 让 gateway 执行池执行取消 / 转后台（gateway 侧实现转调公开方法
//     `(*Gateway).CancelExec` / `(*Gateway).DetachExec`，复用既有 `ep.cancel` / `ep.detach`）；
//     **未注入 / 装配方未就绪（nil）→ 调用方返回明确错误、不动作**（gateway 可独立运行）。
//     **2026-09-19（实例隔离第二批，缺口 2）**：两个回调**带 instance 归属**（层侧取
//     `tasklayer.Exec.InstanceID` / 方法面载荷 `instance_id`）→ 执行池只在本 instance 桶内定位，
//     跨实例任务互不可见、互不可取消。
type ExecSink interface {
	OnExecState(ev ExecState)
	ExecStateOf(gwTaskID string) (state string, ok bool)
	CancelExec(instanceID, gwTaskID string) (ok bool, err error)
	DetachExec(instanceID, idOrCall string) (taskID string, err error)
}

// markDone 关闭终态信号（幂等；任务化同步等待据此醒来）。
func (t *ExecTask) markDone() { t.doneOnce.Do(func() { close(t.doneCh) }) }

// markWait 关闭“等待完成”信号（幂等；撤销超时继续等待）。
func (t *ExecTask) markWait() { t.waitOnce.Do(func() { close(t.waitCh) }) }

// clearAwaiting 清除「超时待裁决」标记（须在 ep.mu 内调用）：裁决（wait/detach/cancel）或
// 任务终态后调用。
func clearAwaiting(t *ExecTask) {
	t.awaitingTimeoutS = 0
	t.awaitingOptions = nil
}

// execPool 管理全部异步执行。
// 定位（2026-08-30 §9.9）：gateway 为纯执行层——只负责执行控制
// （超时三层判定 / pending / 后台 goroutine / 取消），完成回报回调仅服务 server：
// onDone 发出的 tasks.done 只回报 server（订阅方收敛为 server），
// 不对前端广播任务事件（任务树/级联/状态事件由 server 编排，21-llm-server）。
//
// 2026-09-19（实例隔离第二批，缺口 2）：执行池**按 instance 分桶**（`execs[instance_id][exec_id]`），
// `max` 成为「**每 instance** 上限」；所有读写路径（spawn / locate / detach / wait / awaiting /
// cancel / status / 执行态上报）一律带 instance 归属，仅在本 instance 桶内定位。
// **兼容**：instance 为空 → 空键桶，等价于引入 instance 维度前的「单池」（desktop 单体
// 1 进程 1 instance 行为不变；gw 执行 id 仍**全局唯一**，`ex-%04d` 不受分桶影响）。
type execPool struct {
	mu       sync.Mutex
	execs    map[string]map[string]*ExecTask // instance_id → (exec_id → ExecTask)；instance 空 = 旧单池
	max      int
	seq      int
	onDone   func(*ExecTask) // tasks.done 完成回报（仅 server 订阅）
	onCancel func(*ExecTask) // 取消后置钩子（gateway：作废/重建归属 provider）
	onExec   func(ExecState) // 执行态上报（P3-①；nil = 未注入 = no-op）
}

func newExecPool(max int, onDone, onCancel func(*ExecTask), onExec func(ExecState)) *execPool {
	if max <= 0 {
		max = 8
	}
	return &execPool{execs: map[string]map[string]*ExecTask{}, max: max, onDone: onDone, onCancel: onCancel, onExec: onExec}
}

// taskOf 在指定 instance 桶内按 exec id 直取（须在 ep.mu 内调用；instance 空 = 旧单池键）。
func (ep *execPool) taskOf(instance, id string) (*ExecTask, bool) {
	b := ep.execs[instance]
	if b == nil {
		return nil, false
	}
	t, ok := b[id]
	return t, ok
}

// execStateOf 构造执行态上报载荷（须在 ep.mu 内调用：快照执行字段，避免与执行 goroutine 写竞争）。
// error 路径附**简短摘要**（`message` = 执行错误，不含堆栈）。
func execStateOf(t *ExecTask, phase string, detail map[string]any) ExecState {
	ev := ExecState{
		GWTaskID: t.ID, InstanceID: t.OwnerInstance, TopSession: t.TopSession,
		Tool: t.Tool, ToolCallID: t.ToolCallID, WorkDir: t.WorkDir, ProviderKey: t.ProviderKey,
		Phase: phase, State: t.State, Detail: detail, At: time.Now().UTC().Format(time.RFC3339),
	}
	if phase == ExecPhaseError {
		ev.Message = t.Error
	}
	return ev
}

// notifyExec 上报执行态（锁外调用；未注入 sink → no-op）。
func (ep *execPool) notifyExec(ev ExecState) {
	if ep.onExec != nil {
		ep.onExec(ev)
	}
}

// emitExec 按 (instance, exec id) 上报执行态（锁内取快照 → 锁外上报；未注入 / 未命中 → no-op）。
func (ep *execPool) emitExec(instance, id, phase string, detail map[string]any) {
	if ep.onExec == nil {
		return
	}
	ep.mu.Lock()
	t, ok := ep.taskOf(instance, id)
	var ev ExecState
	if ok {
		ev = execStateOf(t, phase, detail)
	}
	ep.mu.Unlock()
	if ok {
		ep.notifyExec(ev)
	}
}

// emitExecError 按 (instance, exec id) 上报 error 执行态（message 覆盖池内摘要，如取消后
// respawn 失败的具体原因）；锁内取快照 → 锁外上报；未注入 / 未命中 → no-op。
func (ep *execPool) emitExecError(instance, id, message string, detail map[string]any) {
	if ep.onExec == nil {
		return
	}
	ep.mu.Lock()
	t, ok := ep.taskOf(instance, id)
	var ev ExecState
	if ok {
		ev = execStateOf(t, ExecPhaseError, detail)
	}
	ep.mu.Unlock()
	if !ok {
		return
	}
	if message != "" {
		ev.Message = message
	}
	ep.notifyExec(ev)
}

// taskSpec 是任务化启动参数（统一异步模型：always/manual/never/auto 复用同一路径）。
type taskSpec struct {
	tool        string
	instanceID  string
	session     string
	turn        string
	workDir     string
	toolCallID  string
	topSession  string // 任务树归属主会话（I-90：随 mcp-tasks-report 回报；空 = 回报不带）
	parent      string // 已登记的 llm 侧节点 task_id（I-90；空 = 回报不带）
	providerKey string // 归属 provider（取消 → Invalidate 作废/重建）
	asyncReport bool   // 终态是否经 onDone 回报 mcp-tasks-report
	allowDetach bool   // 允许 detach（manual/auto）
	allowWait   bool   // 允许 wait（never）
}

// start 创建后台执行并立即回报终态（always 路径）。
func (ep *execPool) start(ctx context.Context, spec taskSpec, run func(ctx context.Context) (*mcp.CallToolResult, error)) (*ExecTask, error) {
	spec.asyncReport = true
	return ep.spawn(ctx, spec, run)
}

// spawn 创建执行并后台运行 run；asyncReport 决定终态是否经 onDone 回报
// （always 恒 true；manual/never/auto 初始 false，detach 后置 true）。
// 上限 `max` 为**每 instance 上限**（缺口 2）：只统计本 instance 桶内的活跃（pending/running）执行。
func (ep *execPool) spawn(ctx context.Context, spec taskSpec, run func(ctx context.Context) (*mcp.CallToolResult, error)) (*ExecTask, error) {
	ep.mu.Lock()
	bucket := ep.execs[spec.instanceID] // 只统计本 instance 桶（instance 空 = 旧单池键）
	active := 0
	for _, t := range bucket {
		if t.State == "pending" || t.State == "running" {
			active++
		}
	}
	if active >= ep.max {
		ep.mu.Unlock()
		return nil, fmt.Errorf("并发任务数已达上限（%d），请稍后再试", ep.max)
	}
	ep.seq++
	id := fmt.Sprintf("ex-%04d", ep.seq) // gw 执行 id 全局唯一（跨 instance 不冲突）
	ctx2, cancel := context.WithCancel(ctx)
	t := &ExecTask{
		ID: id, Tool: spec.tool, State: "pending",
		OwnerInstance: spec.instanceID, Session: spec.session, Turn: spec.turn, WorkDir: spec.workDir,
		ToolCallID: spec.toolCallID, TopSession: spec.topSession, Parent: spec.parent,
		ProviderKey: spec.providerKey, CreatedAt: time.Now(), cancel: cancel,
		asyncReport: spec.asyncReport, allowDetach: spec.allowDetach, allowWait: spec.allowWait,
		doneCh:   make(chan struct{}),
		detachCh: make(chan struct{}),
		waitCh:   make(chan struct{}),
	}
	if bucket == nil {
		bucket = map[string]*ExecTask{}
		ep.execs[spec.instanceID] = bucket
	}
	bucket[id] = t
	ep.mu.Unlock()

	go func() {
		ep.setState(t, "running")
		res, err := run(ctx2)
		ep.mu.Lock()
		if ctx2.Err() == context.Canceled && t.State != "done" {
			t.State = "cancelled"
			t.Error = "cancelled"
		} else if err != nil {
			t.State = "error"
			t.Error = err.Error()
		} else {
			t.State = "done"
			t.Result = res
		}
		t.DoneAt = time.Now()
		clearAwaiting(t) // 终态：清除待裁决标记
		// 执行态上报（P3-① 上报点 ⑤-异常）：仅**失败**路径走层内上报（带简短 message → 层记
		// exec.error）；done / cancelled 终态**不经本通道**（仍走既有 mcp-tasks-report → 层
		// applyReport），避免造出第二条终态通道。
		reportError := t.State == "error"
		var execEv ExecState
		if reportError {
			execEv = execStateOf(t, ExecPhaseError, nil)
		}
		report := t.asyncReport
		done := ep.onDone
		t.markDone()
		ep.mu.Unlock()
		if report && done != nil {
			done(t)
		}
		if reportError {
			ep.notifyExec(execEv)
		}
	}()
	return t, nil
}

// locate 在**指定 instance 桶内**按 exec_id 或 tool_call_id 定位执行（须在锁内调用）。
// 只扫本 instance 桶 → 跨实例执行互不可见（缺口 2）；instance 空 = 旧单池键（旧语义等价）。
func (ep *execPool) locate(instance, idOrCall string) *ExecTask {
	if idOrCall == "" {
		return nil
	}
	b := ep.execs[instance]
	if b == nil {
		return nil
	}
	if t, ok := b[idOrCall]; ok {
		return t
	}
	for _, cand := range b {
		if cand.ToolCallID != "" && cand.ToolCallID == idOrCall {
			return cand
		}
	}
	return nil
}

// detach 把在飞的 manual/auto 执行解绑转后台（`Gateway.DetachExec`；原 tools/background）：
// 命中非终态执行 → 置解绑标记（终态回报 + 结果异步交付）并唤醒等待方。
// never（options 无 detach）→ 拒绝。返回执行 id；未命中 / 已终态 / 已解绑 → error。
// instance 归属：只在本 instance 桶内定位（跨实例不可见）。
func (ep *execPool) detach(instance, idOrCall string) (string, error) {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	t := ep.locate(instance, idOrCall)
	if t == nil {
		return "", fmt.Errorf("task for %s not found", idOrCall)
	}
	switch t.State {
	case "done", "error", "cancelled":
		return "", fmt.Errorf("task %s is not running (state: %s)", t.ID, t.State)
	}
	if !t.allowDetach {
		return "", fmt.Errorf("task %s does not allow detach", t.ID)
	}
	if t.detached {
		return "", fmt.Errorf("task %s already in background", t.ID)
	}
	t.detached = true
	t.asyncReport = true
	clearAwaiting(t) // 用户已裁决（detach）：清除待裁决标记
	close(t.detachCh)
	return t.ID, nil
}

// autoDetach 把到达 async-threshold 的在飞 auto 执行标记为已转后台（**不重跑**）：
// 仅置标记（终态回报 + 结果异步交付），调用方（doCall）自行返回 pending。
func (ep *execPool) autoDetach(t *ExecTask) error {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	switch t.State {
	case "done", "error", "cancelled":
		return fmt.Errorf("task %s already terminal (%s)", t.ID, t.State)
	}
	if t.detached {
		return fmt.Errorf("task %s already in background", t.ID)
	}
	t.detached = true
	t.asyncReport = true
	return nil
}

// wait 撤销超时、继续等待原调用返回并交付结果（mcp-tools-wait {tool_call_id|task_id}）。
// 仅 allowWait（never）执行可等待；非终态才可等待。instance 归属：只在本 instance 桶内定位。
func (ep *execPool) wait(instance, idOrCall string) (string, error) {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	t := ep.locate(instance, idOrCall)
	if t == nil {
		return "", fmt.Errorf("task for %s not found", idOrCall)
	}
	switch t.State {
	case "done", "error", "cancelled":
		return "", fmt.Errorf("task %s already terminal (%s)", t.ID, t.State)
	}
	if !t.allowWait {
		return "", fmt.Errorf("task %s does not allow wait", t.ID)
	}
	clearAwaiting(t) // 用户已裁决（wait）：清除待裁决标记
	t.markWait()
	return t.ID, nil
}

// enterAwaiting 标记执行进入「超时待用户裁决」态（manual/never 到超时点；I-57）：
// 记录 options（与 mcp-tools-timeout 事件一致，供前端裁决条恢复）。
// 仅非终态才置位（竞态下执行可能已在超时瞬间完成）；裁决（wait/detach）或终态清除。
// instance 归属：只在本 instance 桶内定位。
func (ep *execPool) enterAwaiting(instance, id string, timeoutSec float64, options []string) {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	t, ok := ep.taskOf(instance, id)
	if !ok {
		return
	}
	switch t.State {
	case "done", "error", "cancelled":
		return
	}
	t.awaitingTimeoutS = timeoutSec
	t.awaitingOptions = append([]string(nil), options...)
}

// manualResult 取执行终态结果快照（锁内构建）——任务化同步调用交付用；不持锁读会与执行
// goroutine 的终态写入竞争（cancel 路径的 doneCh 关闭方可能非执行 goroutine）。
func (ep *execPool) manualResult(t *ExecTask) map[string]any {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	return manualResultMap(t)
}

// statusMap 返回执行状态快照（锁内构建，避免与执行 goroutine 写字段竞争）。
// instance 归属：只在本 instance 桶内定位（跨实例不可见）。
func (ep *execPool) statusMap(instance, id string) (map[string]any, bool) {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	t, ok := ep.taskOf(instance, id)
	if !ok {
		return nil, false
	}
	return taskStatusMap(t), true
}

// cancel 取消执行（非终态才可取消）——**含已 detached 的后台执行**（用户硬约束）：
// 置 cancelled 终态 + 取消执行 ctx；随后（锁外）触发 onCancel 钩子作废/重建归属 provider
// （kill stdio 子进程 + respawn），使取消真实打断在飞调用。
//
// 终态回报（幂等分治）：detached 执行由执行 goroutine 终态经 onDone 回报（asyncReport=true）；
// **非 detached 执行**（manual/never 在飞、asyncReport=false）的执行 goroutine 不回报——原
// 同步调用方可能已离开（turn 结束/进程重启），故此处补发一次 mcp-tasks-report{state:cancelled}
// （携带 tool_call_id 供 server 定位未登记 gwTaskID 的节点）。两路径互斥（以 asyncReport 分流），
// 不会重复回报。
// instance 归属：只在本 instance 桶内定位（跨实例不可见）。
func (ep *execPool) cancel(instance, id string) error {
	ep.mu.Lock()
	t, ok := ep.taskOf(instance, id)
	if !ok {
		ep.mu.Unlock()
		return fmt.Errorf("task %s not found", id)
	}
	switch t.State {
	case "done", "error", "cancelled":
		ep.mu.Unlock()
		return fmt.Errorf("cannot cancel task in terminal state: %s", t.State)
	}
	if t.cancel != nil {
		t.cancel()
	}
	t.State = "cancelled"
	if t.Error == "" {
		t.Error = "cancelled" // 终态摘要（未 detach 回报的 result_summary 取此值）
	}
	t.DoneAt = time.Now()
	clearAwaiting(t) // 用户已裁决（cancel）：清除待裁决标记
	// 终态 cancelled **不经执行态上报通道**（取消路径的终态回报见上：detached 由执行 goroutine 经
	// onDone 回报，非 detached 由此处补发 mcp-tasks-report）→ 层由既有回报推进为 cancelled。
	report := !t.asyncReport
	done := ep.onDone
	t.markDone()
	onCancel := ep.onCancel
	ep.mu.Unlock()
	if report && done != nil {
		done(t)
	}
	if onCancel != nil {
		onCancel(t)
	}
	return nil
}

// cancelRef 在**指定 instance 桶内**按 exec_id **或** LLM tool-call id 取消（前者直取；后者
// locate 归一到 exec id）：`Gateway.CancelExec` 的双引用口径（与 detach 一致）。
// instance 归属：只在本 instance 桶内定位 → 跨实例执行互不可取消。
func (ep *execPool) cancelRef(instance, idOrCall string) error {
	ep.mu.Lock()
	id := ""
	if t := ep.locate(instance, idOrCall); t != nil {
		id = t.ID
	}
	ep.mu.Unlock()
	if id == "" {
		return fmt.Errorf("task %s not found", idOrCall)
	}
	return ep.cancel(instance, id)
}

func (ep *execPool) setState(t *ExecTask, s string) {
	ep.mu.Lock()
	t.State = s
	if s == "running" && t.StartedAt.IsZero() {
		t.StartedAt = time.Now()
	}
	ep.mu.Unlock()
}
