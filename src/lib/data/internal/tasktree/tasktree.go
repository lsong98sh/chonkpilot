// Package tasktree 是 tasktree 域门面实现（`chonkpilot-data/facade` 的 TasktreeAPI）的落地包
// （阶段 4「internal 下沉」：由 `chonkpilot-data/persist` 下沉至此）。
//
// 覆盖：任务树节点列表 / 任务快照 / 节点幂等落库 / 关闭（逻辑删除）+ 影子域回滚开关。
// 存储形状（`node_id`/`node_type` 列、影子桶 `task_shadow*`、索引桶
// `tasktree_by_top/_by_session/_by_parent`（由 Table 写入口维护，见 indexes.go）、逻辑删除两列
// `closed`/`deleted_at`）与门面 DTO 的翻译收在 `facade/wire`（一份翻译，三处路径逐字一致）。
//
// 单一实现两处绑定（同一份代码，不并列两套写法）：
//   - inline 绑定：`facade/inline.New` 直接构造本服务（同进程直调，不经 MQ）；
//   - mq  绑定：persist 的 `data-tasktree-*` handler **委托本文件的同一批方法**，
//     只负责信封（reply/fail）——故两条路径行为等价（有测试）。
//
// 变更广播（订阅面；23 §7）：`TasktreeDelete` 关闭节点后广播既有 `task-deleted`
// （61 §3.4：`{instance_id, node_id}`，主题与载荷不变）——任何绑定下订阅方
// （server 内存同步删除）照旧收到。
//
// internal 门禁：本包不导出给模块外（见 internal/kernel 包注释）。
package tasktree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/chonkpilot/chonkpilot-data/internal/sessionstore"
	"github.com/lsong98sh/chonkpilot-agent/session/component"
	"github.com/lsong98sh/chonkpilot-agent/session/store"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
)

// Service 是 tasktree 域门面实现（inline 绑定与 MQ 信封层共用）。
type Service struct {
	*kernel.Base
}

// New 构造 tasktree 域服务（共享内核由装配侧注入）。
func New(base *kernel.Base) *Service { return &Service{Base: base} }

// 编译期断言：实现完整 tasktree 域门面（缺方法即编译不过）。
var _ facade.TasktreeAPI = (*Service)(nil)

// 表选择（`tasktree` / `task_shadow`）与行过滤（closed / init）实现唯一 = 会话组件
// （`component.Tasktree*`，P4-2 批次 C，2026-10-31）；本包只保留 DTO 整形与跨库聚合。

// TasktreeList 读某主会话的任务树节点（Mode="init" → 仅 DSL 作业根（kind=dsl_job，DSL-3 起；
// 原 kind=llm 已改判）/运行中存活节点）；
// 默认过滤**逻辑删除**（closed）行，IncludeClosed=true → 一并返回（任务层内部读）。
func (s *Service) TasktreeList(req facade.TasktreeListRequest) (facade.TasktreeListResponse, error) {
	if req.TopSession == "" {
		return facade.TasktreeListResponse{}, errors.New("top_session required")
	}
	if req.DialogID == "" {
		return facade.TasktreeListResponse{}, kernel.ErrDialogRequired
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.TasktreeListResponse{}, err
	}
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.TasktreeListResponse{}, err
	}
	recs, err := comp.TasktreeList(component.TasktreeListCmd{
		DialogID:      req.DialogID,
		TopSession:    req.TopSession,
		Mode:          req.Mode,
		Shadow:        req.Shadow,
		IncludeClosed: req.IncludeClosed,
	})
	if err != nil {
		return facade.TasktreeListResponse{}, err
	}
	nodes := make([]facade.TaskNode, 0, len(recs))
	for _, r := range recs {
		nodes = append(nodes, wire.TaskNodeFromWire(kernel.RecordView(data.Record(r), "node_id")))
	}
	return facade.TasktreeListResponse{Nodes: nodes}, nil
}

// TasktreeTasks 读任务快照列表（**单源 = 任务层权威表**：运行态与持久态同源）；
// 默认过滤逻辑删除（closed）行，IncludeClosed=true → 一并返回。
func (s *Service) TasktreeTasks(req facade.TasktreeTasksRequest) (facade.TasktreeTasksResponse, error) {
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.TasktreeTasksResponse{}, err
	}
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.TasktreeTasksResponse{}, err
	}
	cmd := component.TasktreeTasksCmd{
		SessionID: req.SessionID, TopSession: req.TopSession,
		Shadow: req.Shadow, IncludeClosed: req.IncludeClosed,
	}
	// 一树一库：无 dialog_id 时为**全实例快照**（任务层启动恢复）→ 跨对话库聚合（61 §0.6 例外）。
	if req.DialogID == "" {
		files, err := sessionstore.ListDialogFiles(prj.Path())
		if err != nil {
			return facade.TasktreeTasksResponse{}, err
		}
		out := make([]facade.Task, 0, 16)
		for _, f := range files {
			dialogID, ok := data.DialogIDFromFile(f)
			if !ok {
				continue
			}
			cmd.DialogID = dialogID
			recs, err := comp.TasktreeTasks(cmd)
			if err != nil {
				continue // 打不开（锁/损坏）→ 跳过，不阻断其它对话
			}
			out = append(out, tasksFrom(recs)...)
		}
		return facade.TasktreeTasksResponse{List: out}, nil
	}
	cmd.DialogID = req.DialogID
	recs, err := comp.TasktreeTasks(cmd)
	if err != nil {
		return facade.TasktreeTasksResponse{}, err
	}
	return facade.TasktreeTasksResponse{List: tasksFrom(recs)}, nil
}

// tasksFrom 把任务树原始行映射为门面任务快照（字段口径与改前逐字一致）。
func tasksFrom(recs []store.Record) []facade.Task {
	out := make([]facade.Task, 0, len(recs))
	for _, r := range recs {
		taskID := kernel.Sval(r["node_id"])
		if taskID == "" {
			taskID = kernel.Sval(r[data.KeyField])
		}
		if tid := kernel.Sval(r["task_id"]); tid != "" {
			taskID = tid
		}
		kind := kernel.Sval(r["kind"])
		item := facade.Task{
			ID:         taskID,
			Status:     kernel.Sval(r["status"]),
			Kind:       kind,
			SessionID:  kernel.Sval(r["session_id"]),
			TopSession: kernel.Sval(r["top_session"]),
			Name:       kernel.Sval(r["title"]),
			ToolCallID: kernel.Sval(r["tool_call_id"]),
			State:      kernel.Sval(r["state"]),
			ExecJSON:   kernel.Sval(r["exec_json"]),
			StartedAt:  kernel.Sval(r["created_at"]),

			LoopCurrent:  ival(r["loop_current"]),
			LoopTotal:    ival(r["loop_total"]),
			Steps:        wire.DslStepsFromWire(r["steps"]),
			Shadow:       boolVal(r["shadow"]),
			ReturnKind:   kernel.Sval(r["return_kind"]),
			ReturnInline: kernel.Sval(r["return_inline"]),
			ReturnFile:   kernel.Sval(r["return_file"]),
			ReturnSize:   ival(r["return_size"]),
		}
		// 待裁决明细（I-99）：仅当层权威 state == awaiting 且执行态携带 options 时给出。
		if item.State == "awaiting" {
			item.Awaiting = awaitingView(item.ExecJSON)
		}
		out = append(out, item)
	}
	return out
}

// TasktreeUpsert 节点幂等落库（同 id 覆盖；P2 起 = 任务层唯一写入路径）：
// 表列 `node_type` 由 Kind 推（llm → session，其余 tool）；扩展字段按非空落库。
func (s *Service) TasktreeUpsert(req facade.TasktreeUpsertRequest) (facade.TasktreeUpsertResponse, error) {
	n := req.Node
	if n.ID == "" {
		return facade.TasktreeUpsertResponse{}, errors.New("task_id required")
	}
	// INPUTVAL-1 P0-4：任务状态枚举白名单（写坏值会让前端任务树 / 恢复判定失真）。
	switch n.Status {
	case "", "pending", "running", "done", "error", "cancelled", "interrupted":
	default:
		return facade.TasktreeUpsertResponse{}, fmt.Errorf("tasktree: invalid status %q", n.Status)
	}
	nodeType := "tool"
	if n.Kind == "llm" {
		nodeType = "session"
	}
	rec := data.Record{
		"node_id":        n.ID,
		"node_type":      nodeType,
		"top_session":    n.TopSession,
		"session_id":     n.SessionID,
		"task_id":        n.ID,
		"kind":           n.Kind,
		"parent_node_id": n.ParentID,
		"title":          n.Title,
		"status":         n.Status,
		"updated_at":     time.Now().UTC().Format(kernel.RFC3339FixedNano),
	}
	// 任务层扩展字段（P2）：非空即落；旧调用方（不带这些字段）行为不变。
	// 注：`created_at` 亦纳入「未携带才保留」——恒写空串会清空旧值并使 OrderBy created_at 排序错位。
	for k, v := range map[string]string{
		"created_at":    n.CreatedAt,
		"finished_at":   n.FinishedAt,
		"tool_call_id":  n.ToolCallID,
		"instance_id":   n.InstanceID,
		"work_dir":      n.WorkDir,
		"state":         n.State,
		"args_digest":   n.ArgsDigest,
		"result_digest": n.ResultDigest,
		"exec_json":     n.ExecJSON,
		"started_at":    n.StartedAt,
		"done_at":       n.DoneAt,
		"deleted_at":    n.DeletedAt,
		"return_kind":   n.ReturnKind,
		"return_inline": n.ReturnInline,
		"return_file":   n.ReturnFile,
	} {
		if v != "" {
			rec[k] = v
		}
	}
	// DSL-3 展示字段（非零才落；缺省与既有载荷逐字节等价）。
	if n.LoopCurrent != 0 {
		rec["loop_current"] = n.LoopCurrent
	}
	if n.LoopTotal != 0 {
		rec["loop_total"] = n.LoopTotal
	}
	if n.ReturnSize != 0 {
		rec["return_size"] = n.ReturnSize
	}
	if n.Shadow {
		rec["shadow"] = true
	}
	if len(n.Steps) > 0 {
		rec["steps"] = wire.DslStepsToWire(n.Steps)
	}
	if n.Closed != nil {
		rec["closed"] = *n.Closed
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.TasktreeUpsertResponse{}, err
	}
	if req.DialogID == "" {
		return facade.TasktreeUpsertResponse{}, kernel.ErrDialogRequired
	}
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.TasktreeUpsertResponse{}, err
	}
	// 单一实现 = 组件 `UpdateIn`（本次未携带的条件写入列保留旧值；行不存在 → 新建）。
	if err := comp.TasktreeUpsert(component.TasktreeUpsertCmd{
		DialogID: req.DialogID,
		Shadow:   req.Shadow,
		ID:       n.ID,
		Rec:      store.Record(rec),
	}); err != nil {
		return facade.TasktreeUpsertResponse{}, err
	}
	return facade.TasktreeUpsertResponse{OK: true}, nil
}

// TasktreeDelete 「关闭任务」= **逻辑删除**（21 任务层设计方案 §8.1-2）：节点与其级联子树逐行
// 标记 closed=true + deleted_at（**行保留、历史可查**），不再物理删除；随后发布 task-deleted
// 同步 server 内存。Shadow=true（P1 回滚开关）→ 保持 P1 语义：物理删影子子树 + 影子索引，
// **不广播** task-deleted。
func (s *Service) TasktreeDelete(req facade.TasktreeDeleteRequest) (facade.TasktreeDeleteResponse, error) {
	if req.NodeID == "" {
		return facade.TasktreeDeleteResponse{}, errors.New("node_id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.TasktreeDeleteResponse{}, err
	}
	if req.DialogID == "" {
		return facade.TasktreeDeleteResponse{}, kernel.ErrDialogRequired
	}
	comp, err := componentBridge(prj)
	if err != nil {
		return facade.TasktreeDeleteResponse{}, err
	}
	// 级联子树实现唯一 = 组件（全表扫描建父子关系，替代 `_by_parent` 前缀索引；结果集等价）。
	if err := comp.TasktreeDelete(component.TasktreeDeleteCmd{
		DialogID: req.DialogID, Shadow: req.Shadow, NodeID: req.NodeID,
	}); err != nil {
		return facade.TasktreeDeleteResponse{}, err
	}
	if !req.Shadow { // 影子域保持 P1 语义：物理删除且**不广播**
		s.emitTaskDeleted(req.InstanceID, req.NodeID)
	}
	return facade.TasktreeDeleteResponse{OK: true}, nil
}

// emitTaskDeleted 广播 task-deleted（相对主题），载荷 = `{instance_id, node_id}`（61 §3.4；
// I-93：instance_id 取自本请求上下文，node_id 语义不变）。无总线（测试）→ 静默跳过。
func (s *Service) emitTaskDeleted(instanceID, nodeID string) {
	if s.Bus == nil {
		return
	}
	b, _ := json.Marshal(map[string]any{"instance_id": instanceID, "node_id": nodeID})
	_ = s.Bus.Emit(context.Background(), msgkeys.TopicTaskDeleted, b)
}

// ival 把数据面 JSON 数值（float64/int/json.Number）转为 int（非法 → 0）。
func ival(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0
		}
		return int(i)
	}
	return 0
}

// boolVal 把数据面值转 bool（bool 原值；字符串 "true" → true）。
func boolVal(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true"
	}
	return false
}

// awaitingView 从执行态明细解析待裁决明细（I-99）：返回 nil = 未携带 options（不输出 awaiting
// 字段）。options / timeout_s 优先取顶层、回落 detail 子对象（gateway 执行态上报格式 =
// `{..., "detail":{"timeout_s","options"}}`）。reason 固定 "timeout"（到超时点的待裁决）。
func awaitingView(execJSON string) map[string]any {
	if execJSON == "" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(execJSON), &m) != nil {
		return nil
	}
	src := m
	if d, ok := m["detail"].(map[string]any); ok {
		if _, ok := m["options"]; !ok {
			src = d
		}
	}
	raw, _ := src["options"].([]any)
	opts := make([]string, 0, len(raw))
	for _, o := range raw {
		if s, ok := o.(string); ok {
			opts = append(opts, s)
		}
	}
	if len(opts) == 0 {
		return nil // 无 options → 无裁决选项，不输出（存在即带、缺省不加）
	}
	aw := map[string]any{"reason": "timeout", "options": opts}
	if ts, ok := src["timeout_s"]; ok && ts != nil {
		aw["timeout_s"] = ts
	}
	return aw
}
