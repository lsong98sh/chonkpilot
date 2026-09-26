// 数据面访问（21 §9.2 P2 · store.go）：层的存储**只经 data 面**（bus 上的 data-tasktree-*
// 方法面），**绝不直连 bbolt / 不打开数据库文件**——bbolt 是单写者，chonkpilot-data 是 DB 的
// 唯一拥有者（21 §6-3 / §9.1）。
//
// P2 单写者：层写**权威表 tasktree**（`data-tasktree-upsert` **不带** `shadow` → persist 默认
// 路径），取代 P1 影子域；**不新增 MQ 主题**（61 零变更）。
// 「关闭任务」= 逻辑删除（persist `data-tasktree-delete` 标记 `closed`/`deleted_at`），故前端视图
// 读（list/tasks）默认**过滤 closed**；层内部读（幂等回读 / 启动恢复 / 一致性校验）需看到 closed
// 行以保留其标记 → 带既有载荷的**增补字段** `include_closed: true`（非新主题）。
package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// 数据面主题（相对主题；总线注入 chonk. 前缀）。全部为**既有**主题，不新增。
const (
	subjectTasktreeUpsert = "data-tasktree-upsert"
	subjectTasktreeList   = "data-tasktree-list"
	subjectTasktreeTasks  = "data-tasktree-tasks"
)

// includeClosedField 是数据面读选项（既有载荷的增补字段，非新主题）：true = 连**逻辑删除**
// （closed）行一并返回；缺省 = 前端视图口径（过滤 closed）。
const includeClosedField = "include_closed"

// errInstanceRequired 是数据面入口的**空 instance_id 守卫**（MW-11）：层恒显式透传真实实例，
// 绝不以空串走 data 面「唯一实例回退」——多实例下会报 ErrInstanceIDRequired，误回退时更会跨实例串库。
var errInstanceRequired = errors.New("task: 缺 instance_id（拒绝唯一实例回退，避免多实例串库）")

// store 是层的存储出口（同步 ack：Emit + 等应答，对齐 dataRequest 语义）。
type store struct {
	bus     mq.Bus
	timeout time.Duration
}

// request 发 data-<domain>-<op> 请求并等应答（应答发布到**同一主题**，载荷
// {req_id, ok, result}；按 req_id 匹配，忽略他人请求 / 应答）。
func (s *store) request(subject string, req map[string]any) (map[string]any, error) {
	reqID := newID()
	req["req_id"] = reqID
	type reply struct {
		result map[string]any
		err    error
	}
	done := make(chan reply, 1)
	sub, err := s.bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m struct {
			ReqID  string         `json:"req_id"`
			OK     *bool          `json:"ok"`
			Result map[string]any `json:"result"`
			Error  string         `json:"error"`
		}
		if json.Unmarshal(v.Payload, &m) != nil || m.ReqID != reqID || m.OK == nil {
			return nil
		}
		if !*m.OK {
			msg := m.Error
			if msg == "" {
				msg = "data error"
			}
			select {
			case done <- reply{err: errors.New(msg)}:
			default:
			}
			return nil
		}
		select {
		case done <- reply{result: m.Result}:
		default:
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = sub.Unsubscribe() }()

	b, _ := json.Marshal(req)
	f := s.bus.Emit(context.Background(), subject, b)
	if err := f.Wait().Err(); err != nil {
		return nil, err
	}
	select {
	case r := <-done:
		return r.result, r.err
	case <-time.After(s.timeout):
		return nil, errors.New(subject + " via data 面 timeout")
	}
}

// upsert 同步写权威行（幂等 upsert；默认路径，不传 shadow）。
func (s *store) upsert(rec *Record) error {
	return s.upsertRow(rec.InstanceID, row(rec))
}

// upsertRow 同步写一条**数据面行**（键 = task_id / node_id；persist 侧按已识别字段整条覆盖写）。
// 供 Recover 回写「库中原行 + status=interrupted」用（保留未识别字段不需 —— 已识别字段全覆盖）。
func (s *store) upsertRow(instanceID string, data map[string]any) error {
	if instanceID == "" {
		return errInstanceRequired
	}
	_, err := s.request(subjectTasktreeUpsert, map[string]any{
		"instance_id": instanceID,
		"data":        data,
	})
	return err
}

// list 读某主会话的权威树（includeClosed = 连 closed 行一并返回）。
func (s *store) list(instanceID, topSession string, includeClosed bool) ([]map[string]any, error) {
	if instanceID == "" {
		return nil, errInstanceRequired
	}
	payload := map[string]any{"top_session": topSession}
	if includeClosed {
		payload[includeClosedField] = true
	}
	res, err := s.request(subjectTasktreeList, map[string]any{
		"instance_id": instanceID, "data": payload,
	})
	if err != nil {
		return nil, err
	}
	raw, _ := res["nodes"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// listTasks 读该实例全量任务快照（data-tasktree-tasks；空过滤 = 该 instance 库全任务）。
// 启动恢复据此枚举遗留非终态节点所在主会话（快照为精简形态，全字段回写仍走 list）。
func (s *store) listTasks(instanceID string, includeClosed bool) ([]map[string]any, error) {
	if instanceID == "" {
		return nil, errInstanceRequired
	}
	payload := map[string]any{}
	if includeClosed {
		payload[includeClosedField] = true
	}
	res, err := s.request(subjectTasktreeTasks, map[string]any{
		"instance_id": instanceID, "data": payload,
	})
	if err != nil {
		return nil, err
	}
	raw, _ := res["list"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// row 把层记录映射为数据面行（字段与 tasktree 行同源：task_id / top_session / session_id /
// kind / parent_node_id / title / status / created_at / finished_at? / tool_call_id?，
// 另附 task 层扩展字段 workdir / instance_id / state / 摘要 / exec_json / started_at / done_at /
// closed / deleted_at —— 前端只读已知字段，扩展字段为**增补**、不改既有语义）。
//
// status 走 **前端可见状态口径**（`frontVisibleStatus`，P3 相位门控）：detached / awaiting 落到 status
// 会让前端节点丢失状态图标（行为变化）→ status 恒为 running，真实相位只落 `state` / `exec_json`。
func row(rec *Record) map[string]any {
	data := map[string]any{
		"task_id":        rec.TaskID,
		"top_session":    rec.TopSession,
		"session_id":     rec.SessionID,
		"kind":           rec.NodeType,
		"parent_node_id": rec.ParentID,
		"title":          rec.Title,
		"status":         frontVisibleStatus(rec.State),
		"created_at":     rec.CreatedAt,
		"instance_id":    rec.InstanceID,
		"workdir":        rec.WorkDir,
		"state":          rec.State,
		"args_digest":    rec.ArgsDigest,
		"result_digest":  rec.ResultDigest,
		"exec_json":      rec.ExecJSON,
		"started_at":     rec.StartedAt,
		"done_at":        rec.DoneAt,
		"closed":         rec.Closed,
	}
	if rec.DeletedAt != "" {
		data["deleted_at"] = rec.DeletedAt
	}
	if rec.DoneAt != "" {
		data["finished_at"] = rec.DoneAt
	}
	if rec.ToolCallID != "" {
		data["tool_call_id"] = rec.ToolCallID
	}
	return data
}

// rowToRecord 把数据面行还原为层记录（用于层内热视图补齐 / 跨进程幂等 / 取消判定）。
func rowToRecord(row map[string]any) *Record {
	taskID := sval(row["node_id"])
	if taskID == "" {
		taskID = sval(row["task_id"])
	}
	if taskID == "" {
		return nil
	}
	state := sval(row["state"])
	if state == "" {
		state = sval(row["status"])
	}
	kind := sval(row["kind"])
	if kind == "" {
		kind = NodeTypeTool
	}
	rec := &Record{
		WorkDir:      sval(row["workdir"]),
		TaskID:       taskID,
		InstanceID:   sval(row["instance_id"]),
		ParentID:     sval(row["parent_node_id"]),
		TopSession:   sval(row["top_session"]),
		SessionID:    sval(row["session_id"]),
		NodeType:     kind,
		ToolCallID:   sval(row["tool_call_id"]),
		State:        state,
		Title:        sval(row["title"]),
		ArgsDigest:   sval(row["args_digest"]),
		ResultDigest: sval(row["result_digest"]),
		ExecJSON:     sval(row["exec_json"]),
		CreatedAt:    sval(row["created_at"]),
		StartedAt:    sval(row["started_at"]),
		UpdatedAt:    sval(row["updated_at"]),
		DoneAt:       sval(row["finished_at"]),
		DeletedAt:    sval(row["deleted_at"]),
	}
	if v, ok := row["closed"].(bool); ok {
		rec.Closed = v
	}
	if rec.DoneAt == "" {
		rec.DoneAt = sval(row["done_at"])
	}
	return rec
}

// sval 把数据面 JSON 值转字符串（string / number / bool / nil）。
func sval(v any) string {
	switch n := v.(type) {
	case nil:
		return ""
	case string:
		return n
	case bool:
		if n {
			return "true"
		}
		return "false"
	case float64:
		return strconvFormat(n)
	case json.Number:
		return n.String()
	}
	return ""
}

// strconvFormat float64 → 字符串（数据面数字字段极少；保持十进制整形态）。
func strconvFormat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// newID 生成短随机 hex（数据面请求 id）。
func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
