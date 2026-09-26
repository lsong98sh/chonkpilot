// 层骨架（21 §9.2 P2 · layer.go）：订阅**既有事件** → Apply（同步 upsert 权威表 tasktree）。
//
// 订阅清单（全部为既有事件，**零新增 MQ 主题**）：
//   - llm `tasks.started` / `tasks.updated` / `tasks.done`（广播相对主题 task-started /
//     task-updated / task-done；发布点 chonkpilot-llm/server/tasks.go）
//   - gateway `mcp-tasks-report`（chonkpilot-mcp-gateway/gateway/subjects.go）——载荷含
//     `top_session` / `parent`（I-90 增补）→ 未登记节点可由回报**独立建节点**
//   - `task-deleted`（persist data-tasktree-delete 后同步事件；载荷 `{instance_id, node_id}`）
//
// P2 单写者：层写**原表 `tasktree`**（不带 shadow）；llm 不再自写库。
// 层内 `recs` 仅为写路径热视图 + 控制面状态查询（DB 仍为权威）；`execs` 为执行句柄（P2 交付物 ⑤，
// 与 gateway 执行池同生命周期 → 仅内存）。
//
// 失败隔离：层内任何失败**只告警**，订阅回调恒返回 nil —— 绝不向上传播 error 到
// llm / gateway 的业务路径（21 §9.1 交付物 3 / §9.2 硬约束）。
package task

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// 订阅的事件主题（相对主题；总线注入前缀）。
const (
	subjTaskStarted = "task-started" // llm tasks.started
	subjTaskUpdated = "task-updated" // llm tasks.updated
	subjTaskDone    = "task-done"    // llm tasks.done
	subjTaskReport  = "mcp-tasks-report"
	subjTaskDeleted = "task-deleted"
)

// eventSubjects 是层订阅的既有事件全集（不新增主题）。
var eventSubjects = []string{subjTaskStarted, subjTaskUpdated, subjTaskDone, subjTaskReport, subjTaskDeleted}

// dataTimeout 数据面等待上限（同进程同步派发 = 微秒级；给分离形态留裕量）。
const dataTimeout = 3 * time.Second

// Options 是层构造参数。
type Options struct {
	// Logf 告警输出（空 = 丢弃）。层失败只告警，绝不影响主路径。
	Logf func(format string, args ...any)
	// Timeout 数据面等待上限（空 = 3s）。
	Timeout time.Duration
	// OnCancelExec 是**层 → 执行侧**取消回调（21 §8.1-6 / P2 交付物 ⑤）：接受「层判定为可取消
	// 且持有 exec 句柄」的节点 id 与句柄，由调用方经既有执行控制面（gateway tasks/cancel →
	// onTaskCancel → provider.Invalidate）落地取消。空 = 不回调（层只做判定）。
	// 回调在**层锁外**调用（执行侧可能同步回灌事件 → 避免自锁）。
	OnCancelExec func(taskID string, exec Exec)
}

// Layer 是任务层实例：DB 为权威，层内 recs 仅作写路径热视图 + 控制面查询。
type Layer struct {
	bus          mq.Bus
	store        *store
	logf         func(format string, args ...any)
	onCancelExec func(taskID string, exec Exec)

	// mu 串行化 Apply（含数据面往返）——保证同步 ack（D3）与幂等 / 单调校验原子性。
	// 单次往返为同进程内存消息（微秒级），无高频场景（D5），不构成瓶颈。
	mu      sync.Mutex
	recs    map[string]*Record // task_id → 记录（层内热视图；DB 仍为权威）
	execs   map[string]Exec    // task_id → 执行句柄（层持有；取消时回调执行侧）
	subs    []mq.Sub
	started bool
}

// New 构造层（不订阅；需 Start）。bus 为空 → 层退化为空实现（不 panic）。
func New(bus mq.Bus, opts Options) *Layer {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = dataTimeout
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Layer{
		bus:          bus,
		store:        &store{bus: bus, timeout: timeout},
		logf:         logf,
		onCancelExec: opts.OnCancelExec,
		recs:         make(map[string]*Record),
		execs:        make(map[string]Exec),
	}
}

// Start 订阅既有事件（幂等；重复调用无副作用）。
func (l *Layer) Start() error {
	if l.bus == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started {
		return nil
	}
	for _, subject := range eventSubjects {
		sh, err := l.bus.On(subject, 0, func(_ context.Context, subj string, v *mq.Value) error {
			if err := l.Apply(subj, v.Payload); err != nil {
				l.warn("处理 %s 失败（仅告警，不影响主路径）: %v", subj, err)
			}
			return nil // 恒 nil：层内失败不进入 mq 的 v.Errors
		})
		if err != nil {
			l.unsubscribeAll()
			return fmt.Errorf("task: 订阅 %s: %w", subject, err)
		}
		l.subs = append(l.subs, sh)
	}
	l.started = true
	return nil
}

// Stop 退订全部（幂等）。
func (l *Layer) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.unsubscribeAll()
	l.started = false
}

func (l *Layer) unsubscribeAll() {
	for _, sub := range l.subs {
		_ = sub.Unsubscribe()
	}
	l.subs = nil
}

// Apply 处理一条既有事件 → 同步 upsert 权威表（返回 error 供测试 / 自检；
// 生产订阅路径只把 error 记入告警日志）。
func (l *Layer) Apply(subject string, payload []byte) error {
	if l.bus == nil {
		return nil
	}
	switch subject {
	case subjTaskStarted, subjTaskUpdated, subjTaskDone:
		return l.applyNode(payload)
	case subjTaskReport:
		return l.applyReport(payload)
	case subjTaskDeleted:
		return l.applyDeleted(payload)
	default:
		return fmt.Errorf("task: 未知事件主题 %s", subject)
	}
}

// applyNode 处理 llm 节点快照（tasks.started / updated / done → 快照 upsert）：
// 载荷**不携带** exec 信息 → 既有 `exec_json` 原样保留；llm 粗粒度状态（pending/running）**不降级**
// 既有执行态（detached/awaiting），终态（done/error/cancelled）照常覆盖并保留 exec_json
// （P3 修正，见 commitAgainst ②③）。
func (l *Layer) applyNode(payload []byte) error {
	var ev nodeEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return fmt.Errorf("task: 解析节点事件: %w", err)
	}
	rec, err := recordFromNode(&ev)
	if err != nil {
		return err
	}
	rec.UpdatedAt = nowRFC3339()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.commit(rec)
}

// applyReport 处理 gateway 完成回报（mcp-tasks-report），匹配顺序（P3 前置修正）：
//
//	① **按 `task_id` 命中**已有节点（层内热视图 / 库回读）→ **只推进状态**（撞号 / 单调校验同 commit）；
//	② **按 `tool_call_id` 命中已登记节点**（llm 先登记 → 唯一且稳定）→ **推进该节点**，并把 gateway
//	   执行 id 记入其 `exec.gw_task_id`（**不新建节点**）—— 回报的 `task_id` 是 gateway 执行 id
//	   （如 `ex-0001`），与 llm 侧登记节点 id（`tk-<hex>`）不同源，直接当未登记处理会为同一逻辑
//	   任务多建一行子节点（「重复行」缺陷修复）；
//	③ 仍未命中但存在既有 exec 映射（`BindExec` / 执行态补登记）→ 同样只推进、不建行；
//	④ 仍未命中 → 走既有「有 `top_session` 则建节点」的三级回落（见 buildReportedNodeLocked）：
//	   回报自带 top_session 即已足以独立建行，不再依赖 llm 的 task-* 事件先登记；无 `top_session`
//	   （旧发布方 / 缺失）→ 维持既有口径：跳过并记日志。
//
// 建节点失败**只告警**（见 buildReportedNodeLocked），不影响回报主路径。
func (l *Layer) applyReport(payload []byte) error {
	var ev reportEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return fmt.Errorf("task: 解析完成回报: %w", err)
	}
	rep, err := recordFromReport(&ev)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// ① 按 task_id 命中
	if old := l.knownLocked(rep.TaskID, rep.TopSession, rep.InstanceID); old != nil {
		return l.commitAgainst(mergeReport(old, rep), old)
	}
	// ② 按 tool_call_id 命中已登记节点 → 推进该节点 + 记 gateway 执行 id（不建新行）
	//
	// 优先级（预期行为，勿调换）：回报**同时**带 `task_id`=gateway 执行 id 与 `parent` 时，先按
	// `tool_call_id` 推进**已登记**的本次调用节点；`parent` 只在① ② ③ 皆不命中、走 ④ 建节点回落时
	// 才作为挂父依据（buildReportedNodeLocked ①）。理由是 `tool_call_id` 唯一且稳定地指向「本次调用
	// 的那一行」，而按 `parent` 建行会为同一 tool-call 多建一行子节点（重复行）——即「命中即推进、
	// 建行才用 parent」。
	if id := l.matchRegisteredByToolCallLocked(rep.ToolCallID, rep.TopSession, rep.InstanceID); id != "" {
		if base := l.knownLocked(id, rep.TopSession, rep.InstanceID); base != nil {
			l.bindExecLocked(id, rep.TaskID, base)
			l.logf("task: 完成回报按 tool_call_id 命中已登记节点（只推进状态 + 记 gateway 执行 id，不建新行）: gw_task_id=%s tool_call_id=%s task_id=%s",
				rep.TaskID, rep.ToolCallID, id)
			return l.commitAgainst(mergeReport(base, rep), base)
		}
	}
	// ③ 既有 exec 映射（调用方 BindExec / 执行态补登记）
	if id := l.llmNodeByExecLocked(rep.TaskID); id != "" {
		if base := l.knownLocked(id, rep.TopSession, rep.InstanceID); base != nil {
			l.logf("task: 完成回报按 exec 映射命中已登记节点（只推进状态，不建新行）: gw_task_id=%s task_id=%s",
				rep.TaskID, id)
			return l.commitAgainst(mergeReport(base, rep), base)
		}
	}
	// ④ 未登记回落：有 top_session 则建节点
	return l.buildReportedNodeLocked(rep)
}

// knownLocked 取层内热视图记录（持锁调用；缺失 → 按 top_session 回读库补齐，DB 权威）。
// top_session 未知 → 无法定位，返回 nil。
func (l *Layer) knownLocked(taskID, topSession, instanceID string) *Record {
	if r := l.recs[taskID]; r != nil {
		return r
	}
	return l.load(&Record{TaskID: taskID, TopSession: topSession, InstanceID: instanceID})
}

// matchRegisteredByToolCallLocked 按 `tool_call_id` 命中「已登记节点」（持锁调用）：先查层内热视图；
// 未命中且 `top_session` 已知 → 回读库补齐热视图（仅补缺失键，热视图优先）后重试。
// 未命中 / tool_call_id 空 → 空串（调用方继续回落）。
func (l *Layer) matchRegisteredByToolCallLocked(toolCallID, topSession, instanceID string) string {
	if toolCallID == "" {
		return ""
	}
	if id := l.matchParentByToolCallLocked(toolCallID); id != "" {
		return id
	}
	if topSession == "" {
		return ""
	}
	rows, err := l.store.list(instanceID, topSession, true)
	if err != nil {
		l.warn("按 tool_call_id 回读权威行失败（按未登记处理）: tool_call_id=%s %v", toolCallID, err)
		return ""
	}
	for _, row := range rows {
		r := rowToRecord(row)
		if r == nil || l.recs[r.TaskID] != nil {
			continue // 热视图优先（可能比库行更新）
		}
		l.recs[r.TaskID] = r
	}
	return l.matchParentByToolCallLocked(toolCallID)
}

// mergeReport 把完成回报推进既有节点（层记录副本）：状态 / 结果摘要 / 工具与 tool_call_id 补全 /
// 终态时刻；**不覆盖**树归属（parent / top_session 以既有节点为准，避免与登记事件撞号）。
func mergeReport(old *Record, rep *Record) *Record {
	rec := *old
	if rep.State != "" {
		rec.State = rep.State
	}
	if rep.ResultDigest != "" {
		rec.ResultDigest = rep.ResultDigest
	}
	if rec.Tool == "" {
		rec.Tool = rep.Tool
	}
	if rec.ToolCallID == "" {
		rec.ToolCallID = rep.ToolCallID
	}
	if terminalState(rec.State) && rec.DoneAt == "" {
		rec.DoneAt = nowRFC3339()
	}
	rec.UpdatedAt = nowRFC3339()
	return &rec
}

// buildReportedNodeLocked 处理**未登记**的 gateway 完成回报 → **独立建节点**（I-90；持锁调用）。
// 树归属回落顺序（每步记日志，便于诊断）：
//
//	① 回报 `parent` 非空 → 挂该父（parent 由调用层给出，层不猜不重排 —— 21 §4-2）；
//	② `parent` 空但 `tool_call_id` 能匹配到**已登记**的 llm 侧节点 → 挂其下；
//	③ 两者皆无 → 作为该 `top_session` 下的**顶层节点**（parent 空，21 §4-1）。
//
// 缺 `top_session`（无法定位树）/ 状态不可识别 → 跳过并记日志（维持既有「不建树」口径）。
// 建节点失败**只告警并返回 nil**：层内失败绝不进入回报主路径（21 §9.1 交付物 3）。
func (l *Layer) buildReportedNodeLocked(rep *Record) error {
	if rep.TopSession == "" {
		l.logf("task: 完成回报对应的节点未登记且无 top_session（无法建树，跳过）: task_id=%s", rep.TaskID)
		return nil
	}
	if rep.State == "" {
		l.logf("task: 完成回报未登记且状态不可识别（跳过建节点）: task_id=%s state=%q", rep.TaskID, rep.State)
		return nil
	}
	rec := *rep
	rec.NodeType = NodeTypeTool
	now := nowRFC3339()
	if rec.CreatedAt == "" {
		rec.CreatedAt = now // 回报不带创建时刻 → 以建行时刻为准（权威行 created_at / [started_at] 同源）
		rec.StartedAt = rec.CreatedAt
	}
	rec.UpdatedAt = now
	if terminalState(rec.State) {
		rec.DoneAt = now
	}
	if rec.ParentID != "" {
		l.logf("task: 完成回报建节点：按回报 parent 挂父 task_id=%s parent=%s", rec.TaskID, rec.ParentID)
	} else if pid := l.matchParentByToolCallLocked(rec.ToolCallID); pid != "" {
		rec.ParentID = pid
		l.logf("task: 完成回报建节点：parent 空 → 按 tool_call_id 匹配到已登记节点作为父 task_id=%s tool_call_id=%s parent=%s",
			rec.TaskID, rec.ToolCallID, pid)
	} else {
		l.logf("task: 完成回报建节点：parent 空且 tool_call_id 无匹配 → 作为 top_session 下的顶层节点 task_id=%s tool_call_id=%s top_session=%s",
			rec.TaskID, rec.ToolCallID, rec.TopSession)
	}
	if err := l.commitAgainst(&rec, nil); err != nil {
		l.warn("完成回报建节点失败（仅告警，不影响主路径）: task_id=%s %v", rec.TaskID, err)
		return nil
	}
	return nil
}

// matchParentByToolCallLocked 在层视图内按 `tool_call_id` 找「已登记的 llm 侧节点」（持锁调用）：
// 候选 = 层内 tool_call_id 相同的节点，取**最早登记者**（D3 顺序依赖：调用层先登记节点、gateway
// 完成回报后才到 → 同 tool_call_id 下 llm 侧节点必然更早；据此不会把「回报自建节点」选成父而互链）。
// 未命中 / tool_call_id 空 → 空串（回落顶层）。
func (l *Layer) matchParentByToolCallLocked(toolCallID string) string {
	if toolCallID == "" {
		return ""
	}
	var best *Record
	for _, r := range l.recs {
		if r.ToolCallID != toolCallID {
			continue
		}
		if best == nil || earlierRegistered(r, best) {
			best = r
		}
	}
	if best == nil {
		return ""
	}
	return best.TaskID
}

// earlierRegistered 判定 a 是否比 b「更早登记」（持锁调用；同 tool_call_id 择父用）：
// created_at 小者优先（空 = 未知 → 让位）；并列 → kind=llm（turn 节点）优先；再并列 → task_id 小者优先。
func earlierRegistered(a, b *Record) bool {
	if a.CreatedAt != b.CreatedAt {
		if a.CreatedAt == "" {
			return false
		}
		if b.CreatedAt == "" {
			return true
		}
		return a.CreatedAt < b.CreatedAt
	}
	if (a.NodeType == NodeTypeLLM) != (b.NodeType == NodeTypeLLM) {
		return a.NodeType == NodeTypeLLM
	}
	return a.TaskID < b.TaskID
}

// applyDeleted 处理 task-deleted（「关闭任务」= **逻辑删除**，P2 交付物 ④）：层视图把该节点及
// 其**在层视图内的后代**标记 `closed`/`deleted_at`（库侧的级联标记由 persist delete 处理器写入；
// 层不重复写库）。**不物理删除、不广播**（事件载荷与语义不变，仅由 persist 单方发出）。
func (l *Layer) applyDeleted(payload []byte) error {
	var ev deletedEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return fmt.Errorf("task: 解析 task-deleted: %w", err)
	}
	if ev.NodeID == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.markClosedLocked(ev.NodeID)
	return nil
}

// markClosedLocked 逻辑删除标记（持锁调用）：节点本身 + 其在层视图内的后代。
func (l *Layer) markClosedLocked(rootID string) {
	now := nowRFC3339()
	for _, id := range l.descendantsLocked(rootID) {
		rec := l.recs[id]
		if rec == nil {
			continue
		}
		cp := *rec
		cp.Closed = true
		if cp.DeletedAt == "" {
			cp.DeletedAt = now
		}
		cp.UpdatedAt = now
		l.recs[id] = &cp
	}
}

// descendantsLocked 返回节点自身 + 层视图内的全部后代（沿 ParentID；持锁调用）。
func (l *Layer) descendantsLocked(rootID string) []string {
	childrenOf := map[string][]string{}
	for _, r := range l.recs {
		if r.ParentID != "" {
			childrenOf[r.ParentID] = append(childrenOf[r.ParentID], r.TaskID)
		}
	}
	out := []string{rootID}
	for i := 0; i < len(out); i++ {
		out = append(out, childrenOf[out[i]]...)
	}
	return out
}

// commit 提交一条记录（持锁调用）：先解析既有记录（回读库）→ 判定主体 commitAgainst。
func (l *Layer) commit(rec *Record) error {
	if rec.TaskID == "" {
		return fmt.Errorf("task: 记录缺 task_id")
	}
	old := l.recs[rec.TaskID]
	if old == nil {
		old = l.load(rec) // 跨进程 / 热视图缺失：回读库（DB 权威）
	}
	return l.commitAgainst(rec, old)
}

// commitAgainst 是 commit 的判定主体（old = 已解析的既有记录 / nil = 无；持锁调用）：
// 撞号 / **exec_json 只增不减深合并** / 状态优先级 / 状态单调校验 → 逻辑删除标记保留 → 幂等短路 → 同步落库。
//
//	① 撞号（同 (workdir, task_id) 语义冲突）→ 拒绝并记错误日志，层不覆盖；
//	② `exec_json` **只增不减**（P3 修正）：incoming 未携带 exec 信息（llm 节点 / 终态快照）→ 保留既有值；
//	   incoming 携带（gateway 执行态）→ 与既有值深合并（不丢 gw_task_id / provider_key / detail 明细）；
//	③ 状态优先级（P3 修正）：终态 > 执行态(detached/awaiting) > 粗粒度(pending/running)——incoming
//	   pending/running 不得把既有 detached/awaiting 降级（保留既有状态，其余字段照常合并）；终态必须覆盖；
//	④ 状态回退 / 非法转移（done/error/cancelled 不可逆、pending→running 单向）→ 拒绝并记日志，不 panic；
//	⑤ 已逻辑删除的节点 → 保留 closed/deleted_at（关闭后不因后续事件复活）；
//	⑥ 同 (workdir, task_id) 重复事件且内容一致 → 幂等短路，不重复落库；
//	⑦ 其余 → upsert 权威表（同步 ack）。
//
// 拆出独立方法是让「已回读过库」的调用方（applyReport 未登记分支）不重复一次 data 面往返。
func (l *Layer) commitAgainst(rec, old *Record) error {
	if rec.TaskID == "" {
		return fmt.Errorf("task: 记录缺 task_id")
	}
	if old != nil {
		if msg := conflictOf(old, rec); msg != "" {
			l.warn("撞号拒绝（同 task_id 语义冲突，层不覆盖）: task_id=%s %s", rec.TaskID, msg)
			return fmt.Errorf("task: 撞号拒绝 task_id=%s: %s", rec.TaskID, msg)
		}
		// ② exec_json 只增不减（P3 修正）：见 exec.go 的 mergeExecJSON。
		rec.ExecJSON = mergeExecJSON(old.ExecJSON, rec.ExecJSON)
		// ③ 状态优先级（P3 修正）：llm 粗粒度状态不得把既有执行态降级（保留既有状态；终态不受限）。
		if execStateDowngrade(old.State, rec.State) {
			l.logf("状态优先级：incoming %s 不覆盖既有执行态 %s（保留执行态，其余字段照常合并）: task_id=%s",
				rec.State, old.State, rec.TaskID)
			rec.State = old.State
		}
		if !legalTransition(old.State, rec.State) {
			l.warn("状态回退/非法转移拒绝: task_id=%s %s → %s（终态不可逆 / pending→running 单向）",
				rec.TaskID, old.State, rec.State)
			return fmt.Errorf("task: 状态转移非法 task_id=%s: %s → %s", rec.TaskID, old.State, rec.State)
		}
		if old.Closed { // ⑤ 逻辑删除后的事件只补字段，不复活
			rec.Closed = true
			if rec.DeletedAt == "" {
				rec.DeletedAt = old.DeletedAt
			}
		}
		if sameRecord(old, rec) {
			return nil // ⑥ 幂等：重复事件不产生重复落库
		}
	}
	if rec.InstanceID == "" {
		// 与既有 llm 落库口径一致（instance 不可定位 → 不落库）：避免多实例下 data 面
		// 「唯一实例回退」把行写进别的库。层内热视图照常记录（控制面判定可用）。
		l.warn("节点缺 instance_id（不落库，仅层内记录）: task_id=%s", rec.TaskID)
		l.recs[rec.TaskID] = rec
		return nil
	}
	if err := l.store.upsert(rec); err != nil {
		l.warn("落库失败（仅告警，不影响主路径）: task_id=%s %v", rec.TaskID, err)
		return err
	}
	l.recs[rec.TaskID] = rec
	return nil
}

// load 回读库中的既有记录（幂等 / 撞号判定用；top_session 未知 → 无法定位，返回 nil）。
func (l *Layer) load(rec *Record) *Record {
	if rec.TopSession == "" {
		return nil
	}
	rows, err := l.store.list(rec.InstanceID, rec.TopSession, true)
	if err != nil {
		l.warn("回读权威行失败（按新记录处理）: task_id=%s %v", rec.TaskID, err)
		return nil
	}
	for _, r := range rows {
		if got := rowToRecord(r); got != nil && got.TaskID == rec.TaskID {
			l.recs[got.TaskID] = got
			return got
		}
	}
	return nil
}

// Record 返回层内热视图中的记录副本（不存在 → nil）。
func (l *Layer) Record(taskID string) *Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, ok := l.recs[taskID]; ok {
		c := *r
		return &c
	}
	return nil
}

// State 返回层内记录的**权威状态**（21 §9.2 交付物 ⑤：控制面状态判定以层为准）。
// 第二个返回值为 false = 层对该节点一无所知（调用方按自身视图判定，保持行为等价）。
func (l *Layer) State(taskID string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, ok := l.recs[taskID]; ok && r.State != "" {
		return r.State, true
	}
	return "", false
}

// BindExec 登记节点执行句柄（层持有；调用方在 gateway 异步任务登记后调用）。
// 仅内存（与 gateway 执行池同生命周期；进程重启后执行池同样不存在）。
func (l *Layer) BindExec(taskID string, exec Exec) {
	if l.bus == nil || taskID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if exec == (Exec{}) {
		delete(l.execs, taskID)
		return
	}
	l.execs[taskID] = exec
}

// ExecOf 取节点执行句柄（未登记 → 第二个返回 false）。
func (l *Layer) ExecOf(taskID string) (Exec, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	exec, ok := l.execs[taskID]
	return exec, ok
}

// CancelSubtree 以层为权威判定「某节点子树的可取消集合」（P2 交付物 ⑤）：
//   - instanceID = 该节点归属实例（由调用方**显式透传**：turn / 方法面载荷的 instance_id；可空，
//     空则回落「从层内事件记录解析该主会话的来源实例」）；
//   - rootID = 任务节点 id；topSession = 该节点所属主会话（层据此回读权威行定位，可空）；
//   - 判定规则与既有编排一致：**done / error / cancelled（不可逆终态）不重复取消**，其余（含
//     interrupted / pending / running）可取消；
//   - 对持有 exec 句柄的节点，**在层锁外**回调 OnCancelExec（层 → 执行侧）；
//   - 返回可取消节点 id 列表；**nil = 层对该节点一无所知**（调用方按自身视图判定，行为等价）。
//
// 层不广播任何事件、不改变 MQ 载荷（回调由调用方落到既有执行控制面）。
func (l *Layer) CancelSubtree(instanceID, rootID, topSession string) []string {
	if l.bus == nil || rootID == "" {
		return nil
	}
	// ① 判定（持锁）：层内热视图 +（需求时）库回读合成子树视图
	l.mu.Lock()
	view := make(map[string]*Record, len(l.recs))
	for _, r := range l.recs {
		if topSession == "" || r.TopSession == topSession {
			view[r.TaskID] = r
		}
	}
	if _, ok := view[rootID]; !ok && topSession != "" {
		// 库回读须用**真实 instance**（显式优先；缺省从层内事件记录解析该主会话的来源实例）。
		// 解析不到 → **不**以空串走数据面「唯一实例回退」（多实例下 ErrInstanceIDRequired /
		// 跨实例串库）：记告警并按「层不可知」处理（下方 view[rootID] 仍未知 → 返回 nil，
		// 交调用方按自身视图判定，行为等价）。
		inst := instanceID
		if inst == "" {
			inst, _ = l.instanceOfLocked(topSession)
		}
		if inst == "" {
			l.warn("取消判定无法解析 instance_id（不从唯一实例回退，按层不可知处理）: root=%s top_session=%s", rootID, topSession)
		} else if rows, err := l.store.list(inst, topSession, true); err == nil {
			for _, row := range rows {
				if r := rowToRecord(row); r != nil {
					view[r.TaskID] = r
				}
			}
		} else {
			l.warn("取消判定回读权威行失败: root=%s %v", rootID, err)
		}
	}
	childrenOf := map[string][]string{}
	for _, r := range view {
		if r.ParentID != "" {
			childrenOf[r.ParentID] = append(childrenOf[r.ParentID], r.TaskID)
		}
	}
	subtree := []string{rootID}
	for i := 0; i < len(subtree); i++ {
		subtree = append(subtree, childrenOf[subtree[i]]...)
	}
	ids := make([]string, 0, len(subtree))
	type pending struct {
		taskID string
		exec   Exec
	}
	var callbacks []pending
	for _, id := range subtree {
		rec, known := view[id]
		if !known {
			continue // 层未知该节点（跨实例 / 未登记）→ 不纳入（调用方按自身视图兜底）
		}
		if immutableState(rec.State) {
			continue // 不可逆终态：不重复取消
		}
		ids = append(ids, id)
		if exec, ok := l.execs[id]; ok && exec.GWTaskID != "" {
			callbacks = append(callbacks, pending{taskID: id, exec: exec})
		}
	}
	if _, known := view[rootID]; !known {
		ids = nil // 根节点层不可知 → 交调用方判定（保持既有行为等价）
	}
	l.mu.Unlock()

	// ② 层 → 执行侧回调（锁外：执行侧可能同步回灌任务事件 → 避免自锁）
	for _, cb := range callbacks {
		if l.onCancelExec == nil {
			break
		}
		l.onCancelExec(cb.taskID, cb.exec)
	}
	sort.Strings(ids)
	return ids
}

// Recover 启动恢复（21 §8.1-3 / P2 交付物 ⑥，取代既有 llm cleanupStaleTasktree）：
// 把库中**非终态**（pending / running / awaiting / detached）节点一律标 `interrupted`
// （可重试；**不自动重跑**），closed 等既有标记原样保留。返回标注节点数。
// 失败只告警（返回 0）；不新增消息主题（复用 data-tasktree-tasks / -list / -upsert）。
func (l *Layer) Recover(instanceID string) int {
	if l.bus == nil || instanceID == "" {
		return 0
	}
	all, err := l.store.listTasks(instanceID, true)
	if err != nil {
		l.warn("启动恢复枚举任务失败（仅告警）: instance=%s %v", instanceID, err)
		return 0
	}
	// 快照为精简形态（无 parent/title 等）→ 按 top_session 回读全字段行后整条回写。
	tops := make([]string, 0, len(all))
	seen := map[string]bool{}
	for _, row := range all {
		if !resumableState(rowState(row)) {
			continue
		}
		if ts := sval(row["top_session"]); ts != "" && !seen[ts] {
			seen[ts] = true
			tops = append(tops, ts)
		}
	}
	now := nowRFC3339()
	n := 0
	for _, ts := range tops {
		rows, err := l.store.list(instanceID, ts, true)
		if err != nil {
			l.warn("启动恢复读取主会话树失败（跳过）: top_session=%s %v", ts, err)
			continue
		}
		for _, row := range rows {
			if !resumableState(rowState(row)) {
				continue
			}
			row["status"] = StateInterrupted
			row["state"] = StateInterrupted
			row["finished_at"] = now
			row["done_at"] = now
			if err := l.store.upsertRow(instanceID, row); err != nil {
				l.warn("启动恢复标注失败（跳过）: node=%s %v", sval(row["node_id"]), err)
				continue
			}
			l.markRecovered(row, now)
			n++
		}
	}
	if n > 0 {
		l.logf("task: 启动恢复标注 interrupted %d 个遗留节点（instance=%s）", n, instanceID)
	}
	return n
}

// markRecovered 同步层内热视图（启动恢复后控制面判定与库一致；自持锁调用）。
func (l *Layer) markRecovered(row map[string]any, now string) {
	id := sval(row["node_id"])
	if id == "" {
		id = sval(row["task_id"])
	}
	if id == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec, ok := l.recs[id]; ok {
		cp := *rec
		cp.State = StateInterrupted
		cp.DoneAt = now
		cp.UpdatedAt = now
		l.recs[id] = &cp
		return
	}
	if r := rowToRecord(row); r != nil {
		r.State = StateInterrupted
		l.recs[r.TaskID] = r
	}
}

// instanceOf 取某主会话对应的来源实例（校验 / 数据面路由用：由**事件记录显式透传**真实
// instance_id）。解析不到（层内无该主会话的实例记录）→ ("", false)：调用方须显式报错或
// 不动作，**不得**以空串走数据面「唯一实例回退」——多实例下会报 ErrInstanceIDRequired 或跨实例串库。
func (l *Layer) instanceOf(topSession string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.instanceOfLocked(topSession)
}

// instanceOfLocked 同 instanceOf（持锁调用）。
func (l *Layer) instanceOfLocked(topSession string) (string, bool) {
	for _, r := range l.recs {
		if r.TopSession == topSession && r.InstanceID != "" {
			return r.InstanceID, true
		}
	}
	return "", false
}

// rowState 取数据面行的状态（state 优先、回落 status）。
func rowState(row map[string]any) string {
	state := sval(row["state"])
	if state == "" {
		state = sval(row["status"])
	}
	return state
}

// warn 告警输出（层内失败一律只告警，不 panic、不向上传播）。
func (l *Layer) warn(format string, args ...any) { l.logf(format, args...) }

// nowRFC3339 当前时刻（UTC RFC3339；与现有落库时间戳同格式）。
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
