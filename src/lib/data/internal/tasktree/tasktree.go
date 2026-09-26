// Package tasktree 是 tasktree 域门面实现（`chonkpilot-data/facade` 的 TasktreeAPI）的落地包
// （阶段 4「internal 下沉」：由 `chonkpilot-data/persist` 下沉至此）。
//
// 覆盖：任务树节点列表 / 任务快照 / 节点幂等落库 / 关闭（逻辑删除）+ 影子域回滚开关。
// 存储形状（`node_id`/`node_type` 列、影子桶 `task_shadow*`、索引桶
// `tasktree_by_parent/_by_top`、逻辑删除两列 `closed`/`deleted_at`）与门面 DTO 的翻译
// 收在 `facade/wire`（一份翻译，三处路径逐字一致）。
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
		"workdir":       n.WorkDir,
		"state":         n.State,
		"args_digest":   n.ArgsDigest,
		"result_digest": n.ResultDigest,
		"exec_json":     n.ExecJSON,
		"started_at":    n.StartedAt,
		"done_at":       n.DoneAt,
		"deleted_at":    n.DeletedAt,
	} {
		if v != "" {
			rec[k] = v
		}
	}
	if n.Closed != nil {
		rec["closed"] = *n.Closed
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.TasktreeUpsertResponse{}, err
	}
	if err := prj.Table(tasktreeTableFor(req.Shadow)).Upsert(n.ID, rec); err != nil {
		return facade.TasktreeUpsertResponse{}, err
	}
	if req.Shadow {
		shadowIndexPut(prj, rec)
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
	recs, _, err := t.Query(data.Query{})
	if err != nil {
		return facade.TasktreeDeleteResponse{}, err
	}
	childrenOf := map[string][]string{}
	for _, r := range recs {
		id := kernel.Sval(r["node_id"])
		if id == "" {
			id = kernel.Sval(r[data.KeyField])
		}
		if pid := kernel.Sval(r["parent_node_id"]); pid != "" {
			childrenOf[pid] = append(childrenOf[pid], id)
		}
	}
	toDelete := []string{req.NodeID}
	for i := 0; i < len(toDelete); i++ {
		toDelete = append(toDelete, childrenOf[toDelete[i]]...)
	}
	if req.Shadow {
		for _, id := range toDelete {
			_ = t.Delete(id)
		}
		shadowIndexDelete(prj, toDelete)
		return facade.TasktreeDeleteResponse{OK: true}, nil
	}
	// 逻辑删除：节点 + 级联子树同样标记（行保留；幂等）
	now := time.Now().UTC().Format(time.RFC3339)
	for _, id := range toDelete {
		var rec data.Record
		if ok, _ := t.Get(id, &rec); !ok {
			continue
		}
		rec["closed"] = true
		rec["deleted_at"] = now
		rec["updated_at"] = now
		_ = t.Upsert(id, rec)
	}
	// 既有索引桶清理（保留原行为；索引无读方 → 不改变任何可观察行为）
	indexDelete(prj, toDelete)
	s.emitTaskDeleted(req.InstanceID, req.NodeID)
	return facade.TasktreeDeleteResponse{OK: true}, nil
}

// emitTaskDeleted 广播 task-deleted（相对主题），载荷 = `{instance_id, node_id}`（61 §3.4；
// I-93：instance_id 取自本请求上下文，node_id 语义不变）。无总线（测试）→ 静默跳过。
func (s *Service) emitTaskDeleted(instanceID, nodeID string) {
	if s.Bus == nil {
		return
	}
	b, _ := json.Marshal(map[string]any{"instance_id": instanceID, "node_id": nodeID})
	_ = s.Bus.Emit(context.Background(), "task-deleted", b)
}

// 影子域路由（21 §9.1 P1 遗留，**P2 仅作回滚开关**）：载荷 data.shadow = true → 读写**影子桶**
// task_shadow（+ task_shadow_by_top / _by_parent / _by_instance 索引桶，见 migrate.go v5）。
// **不新增 MQ 主题**（复用既有 data-tasktree-* 四个方法）；**默认不传 shadow → 权威表 tasktree**
// （P2 单写者路径）。影子路径保留 P1 语义（含物理删除），供回滚时不改代码直接切换。
const shadowTable = "task_shadow"

// shadowIndexTables 影子索引桶（照 tasktree_by_parent / tasktree_by_top 既有写法）。
var shadowIndexTables = []string{"task_shadow_by_top", "task_shadow_by_parent", "task_shadow_by_instance"}

// closedRow 判定行是否已逻辑删除（closed=true 或 deleted_at 非空）。
func closedRow(r data.Record) bool {
	if v, ok := r["closed"].(bool); ok && v {
		return true
	}
	return kernel.Sval(r["deleted_at"]) != ""
}

// shadowIndexPut 维护影子索引桶（key = "<域值>\x00<task_id>"，与 tasktreeDelete 的 suffix
// 匹配清理写法同源）；索引写失败不影响行写入（尽力而为）。
func shadowIndexPut(prj *data.DB, rec data.Record) {
	id := kernel.Sval(rec["task_id"])
	if id == "" {
		id = kernel.Sval(rec["node_id"])
	}
	if id == "" {
		return
	}
	for _, e := range []struct {
		table, field, val string
	}{
		{"task_shadow_by_top", "top_session", kernel.Sval(rec["top_session"])},
		{"task_shadow_by_parent", "parent_node_id", kernel.Sval(rec["parent_node_id"])},
		{"task_shadow_by_instance", "instance_id", kernel.Sval(rec["instance_id"])},
	} {
		if e.val == "" {
			continue
		}
		_ = prj.Table(e.table).Upsert(e.val+"\x00"+id, data.Record{
			e.field: e.val, "task_id": id, "node_id": id,
		})
	}
}

// shadowIndexDelete 清理影子索引桶（suffix 匹配被删 task_id；对齐 tasktreeDelete 既有清理）。
func shadowIndexDelete(prj *data.DB, ids []string) {
	for _, bucket := range shadowIndexTables {
		keys, err := prj.Table(bucket).ListKeys()
		if err != nil {
			continue
		}
		for _, k := range keys {
			for _, id := range ids {
				if strings.HasSuffix(k, "\x00"+id) {
					_ = prj.Table(bucket).Delete(k)
					break
				}
			}
		}
	}
}

// indexDelete 清理 tasktree 既有索引桶（by_parent / by_top；suffix 匹配）。索引无读方
// （查询全走表扫描），清理保留为既有行为，不改变任何可观察结果。
func indexDelete(prj *data.DB, ids []string) {
	for _, bucket := range []string{"tasktree_by_parent", "tasktree_by_top"} {
		keys, err := prj.Table(bucket).ListKeys()
		if err != nil {
			continue
		}
		for _, k := range keys {
			for _, id := range ids {
				if strings.HasSuffix(k, "\x00"+id) {
					_ = prj.Table(bucket).Delete(k)
					break
				}
			}
		}
	}
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
