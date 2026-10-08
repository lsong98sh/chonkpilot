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
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// Service 是 tasktree 域门面实现（inline 绑定与 MQ 信封层共用）。
type Service struct {
	*kernel.Base
}

// New 构造 tasktree 域服务（共享内核由装配侧注入）。
func New(base *kernel.Base) *Service { return &Service{Base: base} }

// 编译期断言：实现完整 tasktree 域门面（缺方法即编译不过）。
var _ facade.TasktreeAPI = (*Service)(nil)

// tasktreeTableFor 选择读写表：影子域（P1 回滚开关）→ task_shadow；默认 → tasktree（P2 权威表）。
func tasktreeTableFor(shadow bool) string {
	if shadow {
		return shadowTable
	}
	return "tasktree"
}

// TasktreeList 读某主会话的任务树节点（Mode="init" → 仅 llm/运行中存活节点）；
// 默认过滤**逻辑删除**（closed）行，IncludeClosed=true → 一并返回（任务层内部读）。
func (s *Service) TasktreeList(req facade.TasktreeListRequest) (facade.TasktreeListResponse, error) {
	if req.TopSession == "" {
		return facade.TasktreeListResponse{}, errors.New("top_session required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.TasktreeListResponse{}, err
	}
	recs, _, err := prj.Table(tasktreeTableFor(req.Shadow)).Query(data.Query{
		Where:   data.Record{"top_session": req.TopSession},
		OrderBy: "created_at",
	})
	if err != nil {
		return facade.TasktreeListResponse{}, fmt.Errorf("list tasktree: %v", err)
	}
	nodes := make([]facade.TaskNode, 0, len(recs))
	for _, r := range recs {
		if !req.IncludeClosed && closedRow(r) {
			continue // 逻辑删除行不出视图（历史仍可经 IncludeClosed / 直查库）
		}
		if req.Mode == "init" && kernel.Sval(r["kind"]) != "llm" && kernel.Sval(r["status"]) != "running" {
			continue
		}
		nodes = append(nodes, wire.TaskNodeFromWire(kernel.RecordView(r, "node_id")))
	}
	return facade.TasktreeListResponse{Nodes: nodes}, nil
}

// TasktreeTasks 读任务快照列表（**单源 = 任务层权威表**：运行态与持久态同源）；
// 默认过滤逻辑删除（closed）行，IncludeClosed=true → 一并返回。
func (s *Service) TasktreeTasks(req facade.TasktreeTasksRequest) (facade.TasktreeTasksResponse, error) {
	where := data.Record{}
	if req.SessionID != "" {
		where["session_id"] = req.SessionID
	}
	if req.TopSession != "" {
		where["top_session"] = req.TopSession
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.TasktreeTasksResponse{}, err
	}
	recs, _, err := prj.Table(tasktreeTableFor(req.Shadow)).Query(data.Query{Where: where, OrderBy: "created_at"})
	if err != nil {
		return facade.TasktreeTasksResponse{}, fmt.Errorf("task list: %v", err)
	}
	out := make([]facade.Task, 0, len(recs))
	for _, r := range recs {
		if !req.IncludeClosed && closedRow(r) {
			continue // 逻辑删除行不出视图
		}
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
		// 待裁决明细（I-99）：仅当层权威 state == awaiting 且执行态携带 options 时给出
		// （存在即带、缺省不加——缺省与既有载荷逐字节等价）。
		if item.State == "awaiting" {
			item.Awaiting = awaitingView(item.ExecJSON)
		}
		out = append(out, item)
	}
	return facade.TasktreeTasksResponse{List: out}, nil
}

// TasktreeUpsert 节点幂等落库（同 id 覆盖；P2 起 = 任务层唯一写入路径）：
// 表列 `node_type` 由 Kind 推（llm → session，其余 tool）；扩展字段按非空落库。
func (s *Service) TasktreeUpsert(req facade.TasktreeUpsertRequest) (facade.TasktreeUpsertResponse, error) {
	n := req.Node
	if n.ID == "" {
		return facade.TasktreeUpsertResponse{}, errors.New("task_id required")
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
		"created_at":     n.CreatedAt,
		"updated_at":     time.Now().UTC().Format(time.RFC3339),
	}
	// 任务层扩展字段（P2）：非空即落；旧调用方（不带这些字段）行为不变。
	for k, v := range map[string]string{
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
	tb := prj.Table(tasktreeTableFor(req.Shadow))
	// 状态列合并（A-03）+ 单事务 RMW（A-18）：Upsert 是**全量覆盖写**，未携带的状态列会随旧值
	// 一起丢失，导致同 id 重放（如 llm 再上报 running）把「已逻辑删除」的节点复活（closed/
	// deleted_at 被清）。Get→改→Upsert 跨两事务在并发下仍会互覆，故收进 UpdateIn：合并逻辑
	// 在 fn 内基于同事务读到的旧行执行（请求携带则以其为准；行不存在 → 空记录无旧值可合并，
	// 直接新建，口径不变）。
	if err := tb.UpdateIn(n.ID, func(cur data.Record) data.Record {
		for _, k := range []string{"closed", "deleted_at", "finished_at"} {
			if _, has := rec[k]; !has {
				if v, hasOld := cur[k]; hasOld {
					rec[k] = v
				}
			}
		}
		return rec
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
	t := prj.Table(tasktreeTableFor(req.Shadow))
	// 级联子树改走二级索引 <table>_by_parent（键 = 父 id + "\x00" + 子主键，无排序段）：
	// 对每个待删节点按 ListPrefix("<id>\x00") 取直接子节点，BFS 下钻 → O(子树) 而非 O(全库)。
	// 子节点集合口径与旧全表扫逐点一致：不排除逻辑删除行、不区分 top_session（索引对空值/删除行
	// 均入/保留键）；seen 去重 + 环/自环防护（口径同 task/layer.go descendantsLocked）。
	parentIdx := prj.Table(t.Name() + "_by_parent")
	seen := map[string]bool{req.NodeID: true}
	toDelete := []string{req.NodeID}
	for i := 0; i < len(toDelete); i++ {
		kids, err := childIDs(parentIdx, t, toDelete[i])
		if err != nil {
			return facade.TasktreeDeleteResponse{}, err
		}
		for _, id := range kids {
			if seen[id] {
				continue
			}
			seen[id] = true
			toDelete = append(toDelete, id)
		}
	}
	if req.Shadow {
		// 逐行物理删（含索引键，由 Table 写入口同事务清理）；错误聚合上报（A-02）：
		// 原 `_ = t.Delete(id)` 吞错 → 失败静默残留且恒返回 OK。不存在视为已删（幂等）。
		var errs []error
		for _, id := range toDelete {
			if err := t.Delete(id); err != nil && !errors.Is(err, data.ErrNotFound) {
				errs = append(errs, fmt.Errorf("shadow delete %s: %w", id, err))
			}
		}
		if err := errors.Join(errs...); err != nil {
			return facade.TasktreeDeleteResponse{}, err
		}
		return facade.TasktreeDeleteResponse{OK: true}, nil
	}
	// 逻辑删除：节点 + 级联子树同样标记（行保留；幂等）。错误聚合上报（A-02）：逐行为独立
	// 事务，单行失败不回滚 → 必须上报，否则静默残留未关闭节点且恒返回 OK。
	// 标记收进 UpdateIn 单事务 RMW（A-18）：Get→改→Upsert 跨两事务会把并发窗口内新上报的
	// 状态用旧 rec 覆盖；行已消失 → fn 返回 nil 跳过（保持「不存在即跳过」幂等口径，不建空行）。
	now := time.Now().UTC().Format(time.RFC3339)
	var errs []error
	for _, id := range toDelete {
		if err := t.UpdateIn(id, func(rec data.Record) data.Record {
			if len(rec) == 0 {
				return nil
			}
			rec["closed"] = true
			rec["deleted_at"] = now
			rec["updated_at"] = now
			return rec
		}); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", id, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return facade.TasktreeDeleteResponse{}, err
	}
	s.emitTaskDeleted(req.InstanceID, req.NodeID)
	return facade.TasktreeDeleteResponse{OK: true}, nil
}

// childIDs 返回 parentID 的**直接子节点 id 列表**（cascade 删除用）：对索引桶
// <table>_by_parent（键 = 父 id + "\x00" + 子主键，无排序段）做前缀 Seek，只扫本父节点区段
// → O(直接子节点数) 而非 O(全库)。子节点 id 口径与旧全表扫逐点一致：`node_id` 优先、缺失
// 回落主键；脏索引（索引键在而主行缺失）跳过（旧扫主表本就不会产出该行）。
// 索引对空值/逻辑删除行均入 / 保留键，故「含已 closed 行、跨 top_session」与旧实现一致。
// parentIdx = 索引桶（前缀读子主键）、t = 主表（按主键回表取 node_id）。
func childIDs(parentIdx, t *data.Table, parentID string) ([]string, error) {
	prefix := parentID + "\x00" // 与 data.indexes.go 的索引段分隔符一致（节点 id 不含 \x00）
	keys, err := parentIdx.ListPrefix(prefix)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		pk := strings.TrimPrefix(k, prefix)
		var rec data.Record
		if ok, _ := t.Get(pk, &rec); !ok {
			continue // 脏索引（主行缺失）→ 跳过，与全表扫描口径一致
		}
		id := kernel.Sval(rec["node_id"])
		if id == "" {
			id = pk
		}
		ids = append(ids, id)
	}
	return ids, nil
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

// 影子域路由（21 §9.1 P1 遗留，**P2 仅作回滚开关**）：载荷 data.shadow = true → 读写**影子桶**
// task_shadow（+ task_shadow_by_top / _by_session / _by_parent 索引桶，由 Table 写入口维护，见
// indexes.go / migrate.go）。**不新增 MQ 主题**（复用既有 data-tasktree-* 四个方法）；
// **默认不传 shadow → 权威表 tasktree**（P2 单写者路径）。影子路径保留 P1 语义（含物理删除），
// 供回滚时不改代码直接切换。
const shadowTable = "task_shadow"

// closedRow 判定行是否已逻辑删除（closed=true 或 deleted_at 非空）。
func closedRow(r data.Record) bool {
	if v, ok := r["closed"].(bool); ok && v {
		return true
	}
	return kernel.Sval(r["deleted_at"]) != ""
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
