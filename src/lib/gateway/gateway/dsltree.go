// dsltree.go — `dsl_run` 作业的**展示供数**（DSL-3）：把执行器上行 tree/step/result 协议行转成
// 既有 **task-* 事件**（task-started / task-updated / task-done），由 **任务层**（`src/lib/task`，
// tasktree 唯一写者）订阅后落库 → 前端据 `kind`/`loop_current`/`steps[]`/`return_*` 渲染 DSL 面板。
//
// 设计取舍（**零新增 MQ 主题、单写者不变**）：gateway **不直接**写 `data-tasktree-upsert`
// （该主题唯一发送方 = 任务层，见 61 §3.4）；改为复用 llm 侧同一批既有事件主题
// （llm/server/tasks.go 的 tasks.started/updated/done），任务层 `nodeEvent` 已支持 DSL 字段
// （loop_current/loop_total/steps/return_*）→ 行为与 llm_run（llm/server/jobdsl.go）同源。
//
// 节点映射：
//   - 作业根 = 新建 `dsl_job` 节点（id = "dsljob-"+job；parent = 调用上下文 parent，无 = 顶层），
//     承载 `steps[]`（跨迭代累计）与 `$RETURN` 两态（return_kind/inline/file/size）；
//   - 容器 = `dsl_loop`/`dsl_parallel` 节点（id = 作业根 id + 执行器静态节点 id），只更新
//     `loop_current`（同 id → 不新增节点）。
package mcpgateway

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// DSL 展示节点 kind（与 61 §3.4 / llm server TaskKindDsl* 一致）。
const (
	dslJobKind      = "dsl_job"
	dslLoopKind     = "dsl_loop"
	dslParallelKind = "dsl_parallel"
)

// dslStepsInlineLimit 是**进度中**单条 task-updated 携带的 steps[] 上限（C-20）：每步全量重发
// 会让长作业 O(N²)。截断取**最近** N 步，既不新增/修改 61 的 payload 字段语义，又限制单条消息
// 规模；作业终态 task-done 仍发**全量** steps[]（完整回放）。
const dslStepsInlineLimit = 200

// dslTreeNode / dslTreeMsg / dslStepMsg 是执行器上行协议行（与 dslexec/protocol.go 逐字一致）。
type dslTreeNode struct {
	ID          string `json:"id"`
	Parent      string `json:"parent"`
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	LoopCurrent int    `json:"loop_current"`
	LoopTotal   int    `json:"loop_total"`
}

type dslTreeMsg struct {
	T     string        `json:"t"`
	Job   string        `json:"job"`
	Nodes []dslTreeNode `json:"nodes"`
}

type dslStepMsg struct {
	T         string `json:"t"`
	Job       string `json:"job"`
	No        int    `json:"no"`
	Status    string `json:"status"`
	Purpose   string `json:"purpose"`
	ElapsedMs int64  `json:"elapsed_ms"`
	Session   string `json:"session"`
	Statement string `json:"statement"`
}

// dslTreeReporter 接收执行器上行 tree/step/result（→ tasktree 变更）。可注入以便单测。
type dslTreeReporter interface {
	OnTree(nodes []dslTreeNode)
	OnStep(st dslStepMsg)
	OnResult(res dslResultMsg)
}

// dslEmit 是一条待发事件（subject = 既有 task-* 相对主题；payload = 节点快照）。
type dslEmit struct {
	subject string
	payload map[string]any
}

// dslJobReporter 是 dslTreeReporter 的默认实现：把作业静态树/步骤/结果投影为作业根 + 容器节点，
// 经既有 task-* 事件发往任务层（单写者落库）。
type dslJobReporter struct {
	emit func(subject string, payload map[string]any)

	job, instance, topSession, parent, session, turn, workDir, toolCallID string
	rootID                                                                string
	createdAt                                                             string

	mu       sync.Mutex
	emitted  map[string]bool        // 节点 id → 已发 task-started
	lastLoop map[string]int         // 容器 id → 已发 loop_current（去抖：仅变化才更新）
	steps    map[int]map[string]any // no → 步骤行
	order    []int                  // 步骤序号（稳定顺序）
	finished bool                   // 终态已发（防重复）
}

// newDSLJobReporter 构造作业展示上报器；emit 为 nil → 无副作用（未接线 / 单测）。
func newDSLJobReporter(job, instance, topSession, parent, session, turn, workDir, toolCallID string, emit func(string, map[string]any)) *dslJobReporter {
	if emit == nil {
		emit = func(string, map[string]any) {}
	}
	return &dslJobReporter{
		emit: emit, job: job, instance: instance, topSession: topSession, parent: parent,
		session: session, turn: turn, workDir: workDir, toolCallID: toolCallID,
		rootID: "dsljob-" + job, createdAt: nowRFC3339(),
		emitted: map[string]bool{}, lastLoop: map[string]int{}, steps: map[int]map[string]any{},
	}
}

// nodeID 映射执行器静态节点 id → 任务节点 id（前缀作业根 id，保证跨作业唯一）。
func (r *dslJobReporter) nodeID(execID string) string { return r.rootID + "-" + execID }

// OnTree 处理静态容器树快照：建作业根（首次）+ 各容器节点；容器 loop_current 变化 → 更新。
func (r *dslJobReporter) OnTree(nodes []dslTreeNode) {
	r.mu.Lock()
	var out []dslEmit
	out = append(out, r.ensureRootLocked()...)
	for _, n := range nodes {
		id := r.nodeID(n.ID)
		pid := r.rootID
		if n.Parent != "" {
			pid = r.nodeID(n.Parent)
		}
		p := r.baseNodeLocked(id, n.Kind, n.Label, pid)
		p["loop_current"] = n.LoopCurrent
		if n.LoopTotal != 0 {
			p["loop_total"] = n.LoopTotal
		}
		if !r.emitted[id] {
			r.emitted[id] = true
			r.lastLoop[id] = n.LoopCurrent
			out = append(out, dslEmit{msgkeys.TopicTaskStarted, p})
		} else if r.lastLoop[id] != n.LoopCurrent {
			r.lastLoop[id] = n.LoopCurrent
			out = append(out, dslEmit{msgkeys.TopicTaskUpdated, p})
		}
	}
	r.mu.Unlock()
	r.flush(out)
}

// OnStep 处理一次 LLM 步骤进度：更新作业根 `steps[]` 并发 task-updated。
func (r *dslJobReporter) OnStep(st dslStepMsg) {
	r.mu.Lock()
	out := r.ensureRootLocked()
	row := r.steps[st.No]
	if row == nil {
		row = map[string]any{"no": st.No, "created_at": nowRFC3339()}
		r.steps[st.No] = row
		r.order = append(r.order, st.No)
	}
	row["status"] = st.Status
	row["purpose"] = st.Purpose
	row["session_id"] = st.Session
	row["statement_id"] = st.Statement
	row["elapsed_ms"] = st.ElapsedMs
	root := r.baseNodeLocked(r.rootID, dslJobKind, "DSL 作业", r.parent)
	root["steps"] = r.recentStepsLocked(dslStepsInlineLimit)
	out = append(out, dslEmit{msgkeys.TopicTaskUpdated, root})
	r.mu.Unlock()
	r.flush(out)
}

// OnResult 处理作业终态：写终态 + `$RETURN` 两态（仅脚本使用 `$RETURN` 时写 return_*）。
func (r *dslJobReporter) OnResult(res dslResultMsg) {
	state := "done"
	if !res.OK {
		state = "error"
	}
	r.finish(state, res)
}

// finish 发作业根终态（幂等）。state ∈ done/error/cancelled。
func (r *dslJobReporter) finish(state string, res dslResultMsg) {
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return
	}
	r.finished = true
	out := r.ensureRootLocked()
	root := r.baseNodeLocked(r.rootID, dslJobKind, "DSL 作业", r.parent)
	root["state"] = state
	root["status"] = state
	root["finished_at"] = nowRFC3339()
	if state == "error" && res.Error != "" {
		root["error"] = res.Error
		root["result_summary"] = res.Error
	}
	if len(r.order) > 0 {
		root["steps"] = r.stepsLocked()
	}
	// `$RETURN` 两态（脚本未使用 → 不写 return_*；与 llm_run 的 markReturn 同口径）。
	if res.ReturnUsed {
		if res.Overflow || strings.TrimSpace(res.File) != "" {
			root["return_kind"] = "file"
			root["return_file"] = filepath.Base(res.File)
			root["return_size"] = res.Size
		} else {
			root["return_kind"] = "inline"
			root["return_inline"] = res.Text
		}
	}
	out = append(out, dslEmit{msgkeys.TopicTaskDone, root})
	r.mu.Unlock()
	r.flush(out)
}

// ensureRootLocked 首次建作业根（task-started）；返回待发事件（持锁调用）。
func (r *dslJobReporter) ensureRootLocked() []dslEmit {
	if r.emitted[r.rootID] {
		return nil
	}
	r.emitted[r.rootID] = true
	return []dslEmit{{msgkeys.TopicTaskStarted, r.baseNodeLocked(r.rootID, dslJobKind, "DSL 作业", r.parent)}}
}

// baseNodeLocked 构造节点快照（字段名与 llm/server TaskNode / task 层 nodeEvent 逐字一致；持锁调用）。
func (r *dslJobReporter) baseNodeLocked(id, kind, title, parentID string) map[string]any {
	m := map[string]any{
		"task_id":     id,
		"tool":        dslRunToolName,
		"kind":        kind,
		"top_session": r.topSession,
		"session_id":  r.session,
		"instance_id": r.instance,
		"work_dir":    r.workDir,
		"name":        title,
		"simplified":  title,
		"state":       "running",
		"status":      "running",
		"started_at":  r.createdAt,
	}
	if parentID != "" {
		m["parent_id"] = parentID
	}
	if r.toolCallID != "" {
		m["tool_call_id"] = r.toolCallID
	}
	if r.turn != "" {
		m["turn_id"] = r.turn
	}
	return m
}

// stepsLocked 取步骤数组快照（顺序 = 首次出现序；持锁调用）。
func (r *dslJobReporter) stepsLocked() []any {
	out := make([]any, 0, len(r.order))
	for _, no := range r.order {
		out = append(out, r.steps[no])
	}
	return out
}

// recentStepsLocked 取最近 limit 步快照（limit<=0 或不足 → 全量）；持锁调用（C-20 进度截断用）。
func (r *dslJobReporter) recentStepsLocked(limit int) []any {
	if limit <= 0 || len(r.order) <= limit {
		return r.stepsLocked()
	}
	start := len(r.order) - limit
	out := make([]any, 0, limit)
	for _, no := range r.order[start:] {
		out = append(out, r.steps[no])
	}
	return out
}

// flush 发送事件（锁外）。
func (r *dslJobReporter) flush(out []dslEmit) {
	for _, e := range out {
		r.emit(e.subject, e.payload)
	}
}

// nowRFC3339 当前时刻（UTC RFC3339；与任务层时间戳同格式）。
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
