// data-tasktree-* 信封层：任务树域（节点列表 / 任务快照 / 幂等落库 / 关闭）。
//
// 逻辑在 `internal/tasktree`（阴影域回滚开关与逻辑删除语义不变）。
package persist

import (
	"encoding/json"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
)

// handleTasktree 分发 data-tasktree-<action>；未知 action（含 -refresh 误入）忽略不回复。
func (s *Service) handleTasktree(action string, payload []byte) {
	method := "data-tasktree-" + action

	switch action {
	case "list":
		var p TasktreeListPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			s.failDecode(method, payload, err)
			return
		}
		m := p.merged()
		top := m.TopSession
		if top == "" {
			top = tasktreeTargetID(m.ID, m.Key)
		}
		resp, err := s.TasktreeList(facade.TasktreeListRequest{
			InstanceID:    p.InstanceID,
			DialogID:      m.DialogID,
			TopSession:    top,
			Mode:          m.Mode,
			IncludeClosed: bool(m.IncludeClosed),
			Shadow:        bool(m.Shadow),
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.TasktreeListResult(resp.Nodes))
	case "tasks":
		var p TasktreeTasksPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			s.failDecode(method, payload, err)
			return
		}
		q := p.fields()
		resp, err := s.TasktreeTasks(facade.TasktreeTasksRequest{
			InstanceID:    p.InstanceID,
			DialogID:      q.DialogID,
			SessionID:     q.SessionID,
			TopSession:    q.TopSession,
			IncludeClosed: bool(q.IncludeClosed),
			Shadow:        bool(q.Shadow),
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.TasktreeTasksResult(resp.List))
	case "upsert": // A3 运行时扩展（P2 起 = 任务层唯一写入路径）
		var p TasktreeUpsertPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			s.failDecode(method, payload, err)
			return
		}
		node, dialogID := p.merged()
		if node.InstanceID == "" { // 节点行自带的 instance_id 缺省 = 信封实例（旧 flatReqMap 口径）
			node.InstanceID = p.InstanceID
		}
		if _, err := s.TasktreeUpsert(facade.TasktreeUpsertRequest{
			InstanceID: p.InstanceID,
			DialogID:   dialogID,
			Node:       taskNodeOf(node),
			Shadow:     bool(node.Shadow),
		}); err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.OKResult())
	case "delete":
		var p TasktreeDeletePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			s.failDecode(method, payload, err)
			return
		}
		f := p.merged()
		id := f.NodeID
		if id == "" {
			id = tasktreeTargetID(f.ID, f.Key)
		}
		if id == "" {
			id = f.Filter.NodeID
		}
		if _, err := s.TasktreeDelete(facade.TasktreeDeleteRequest{
			InstanceID: p.InstanceID,
			DialogID:   f.DialogID,
			NodeID:     id,
			Shadow:     bool(f.Shadow),
		}); err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.OKResult())
	}
}

// tasktreeTargetID 取请求目标 id：顶层 id → data.id → data.key（= 旧 RequestID 口径）。
func tasktreeTargetID(id, key ID) string {
	if id != "" {
		return string(id)
	}
	return string(key)
}

// taskNodeOf 把消息面节点行转门面 DTO（`node_id` 优先，回落 `task_id`；title 空回落 name）。
func taskNodeOf(p TaskNodePayload) facade.TaskNode {
	id := string(p.NodeID)
	if id == "" {
		id = string(p.TaskID)
	}
	title := p.Title
	if title == "" {
		title = p.Name // 兼容旧形态：name 即标题
	}
	n := facade.TaskNode{
		ID:           id,
		NodeType:     p.NodeType,
		TopSession:   p.TopSession,
		SessionID:    p.SessionID,
		ParentID:     p.ParentNodeID,
		Kind:         p.Kind,
		Title:        title,
		Status:       p.Status,
		ToolCallID:   p.ToolCallID,
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
		FinishedAt:   p.FinishedAt,
		State:        p.State,
		InstanceID:   p.InstanceID,
		WorkDir:      p.WorkDir,
		ArgsDigest:   p.ArgsDigest,
		ResultDigest: p.ResultDigest,
		ExecJSON:     p.ExecJSON,
		StartedAt:    p.StartedAt,
		DoneAt:       p.DoneAt,
		DeletedAt:    p.DeletedAt,
		LoopCurrent:  int(p.LoopCurrent),
		LoopTotal:    int(p.LoopTotal),
		Steps:        dslStepsOf(p.Steps),
		Shadow:       bool(p.Shadow),
		ReturnKind:   p.ReturnKind,
		ReturnInline: p.ReturnInline,
		ReturnFile:   p.ReturnFile,
		ReturnSize:   int(p.ReturnSize),
	}
	if p.Closed != nil { // 有键才带（含 false：与"无键"语义不同）
		closed := bool(*p.Closed)
		n.Closed = &closed
	}
	return n
}

// dslStepsOf 把消息面 steps 转门面步骤记录。
func dslStepsOf(steps []DslStepPayload) []facade.DslStep {
	if len(steps) == 0 {
		return nil
	}
	out := make([]facade.DslStep, 0, len(steps))
	for _, s := range steps {
		out = append(out, facade.DslStep{
			No:          int(s.No),
			Status:      s.Status,
			Purpose:     s.Purpose,
			ElapsedMs:   int64(s.ElapsedMs),
			CreatedAt:   s.CreatedAt,
			SessionID:   s.SessionID,
			StatementID: s.StatementID,
		})
	}
	return out
}
