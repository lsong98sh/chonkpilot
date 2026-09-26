// 层单测的数据面桩（L1）：模拟 chonkpilot-data 的 data-tasktree-* 方法面（P2 = **单表权威
// tasktree** + 逻辑删除 + include_closed 读选项）；仅内存总线，不依赖真实 GUI / WebView / DB。
package task

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// fakeData 是 data 面（persist）最小桩：rows = 权威表行（键 = task_id，与 persist 同主键）。
type fakeData struct {
	mu         sync.Mutex
	rows       map[string]map[string]any // task_id → 权威行
	upserts    int                       // upsert 次数（幂等 / 拒绝断言用）
	upsertRows []map[string]any          // 历次 upsert 的载荷（closed 保留等断言用）
	deletes    []string                  // 收到的删除请求 node_id（P2 层不再主动删除）
	subs       []mq.Sub
	bus        mq.Bus
}

// newFakeData 建桩并订阅数据面主题（层经总线调用，同真实 persist 面）。
func newFakeData(t *testing.T, bus mq.Bus) *fakeData {
	t.Helper()
	f := &fakeData{rows: map[string]map[string]any{}, bus: bus}
	for _, subject := range []string{
		subjectTasktreeUpsert, subjectTasktreeList, subjectTasktreeTasks, "data-tasktree-delete",
	} {
		s := subject
		sh, err := bus.On(s, 0, func(_ context.Context, _ string, v *mq.Value) error {
			f.handle(s, v.Payload)
			return nil
		})
		if err != nil {
			t.Fatalf("fake data 订阅 %s: %v", s, err)
		}
		f.subs = append(f.subs, sh)
	}
	t.Cleanup(func() {
		for _, sh := range f.subs {
			_ = sh.Unsubscribe()
		}
	})
	return f
}

func (f *fakeData) handle(subject string, payload []byte) {
	// 应答（带 ok）忽略，避免自回环（对齐 persist.handle 的 ok 过滤）。
	var probe struct {
		OK *bool `json:"ok"`
	}
	if json.Unmarshal(payload, &probe) == nil && probe.OK != nil {
		return
	}
	var req struct {
		ReqID string         `json:"req_id"`
		Data  map[string]any `json:"data"`
	}
	if json.Unmarshal(payload, &req) != nil || req.ReqID == "" {
		return
	}
	switch subject {
	case subjectTasktreeUpsert:
		row := buildRow(req.Data)
		f.mu.Lock()
		id := sval(row["task_id"])
		f.upserts++
		f.upsertRows = append(f.upsertRows, row)
		f.rows[id] = row
		f.mu.Unlock()
		f.reply(subject, req.ReqID, map[string]any{"ok": true})
	case subjectTasktreeList, subjectTasktreeTasks:
		top := sval(req.Data["top_session"])
		session := sval(req.Data["session_id"])
		includeClosed := boolVal(req.Data[includeClosedField])
		f.mu.Lock()
		nodes := []any{}
		for _, row := range f.rows {
			if top != "" && sval(row["top_session"]) != top {
				continue
			}
			if session != "" && sval(row["session_id"]) != session {
				continue
			}
			if !includeClosed && f.rowClosed(row) {
				continue
			}
			if subject == subjectTasktreeTasks {
				nodes = append(nodes, tasksView(row))
				continue
			}
			nodes = append(nodes, row)
		}
		f.mu.Unlock()
		key := "nodes"
		if subject == subjectTasktreeTasks {
			key = "list"
		}
		f.reply(subject, req.ReqID, map[string]any{key: nodes})
	case "data-tasktree-delete":
		id := sval(req.Data["node_id"])
		f.mu.Lock()
		f.deletes = append(f.deletes, id)
		for _, row := range f.rows {
			if id == sval(row["node_id"]) || id == sval(row["task_id"]) {
				row["closed"] = true
				row["deleted_at"] = "2026-09-18T09:00:00Z"
			}
		}
		f.mu.Unlock()
		f.reply(subject, req.ReqID, map[string]any{"ok": true})
	}
}

// rowClosed 复刻 persist.closedRow。
func (f *fakeData) rowClosed(row map[string]any) bool {
	if v, ok := row["closed"].(bool); ok && v {
		return true
	}
	return sval(row["deleted_at"]) != ""
}

func (f *fakeData) reply(subject, reqID string, result map[string]any) {
	b, _ := json.Marshal(map[string]any{"req_id": reqID, "ok": true, "result": result})
	f.bus.Emit(context.Background(), subject, b)
}

// upsertCount upsert 次数（全部落权威表）。
func (f *fakeData) upsertCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.upserts
}

// row 取权威行副本（不存在 → nil）。
func (f *fakeData) row(taskID string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[taskID]
}

// rowCount 权威行数（「无重复行」回归断言用）。
func (f *fakeData) rowCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

// lastRow 最近一次 upsert 的行（无 → nil）。
func (f *fakeData) lastRow() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.upsertRows) == 0 {
		return nil
	}
	return f.upsertRows[len(f.upsertRows)-1]
}

// setRowField 直接改权威行字段（制造差异用）。
func (f *fakeData) setRowField(taskID, field, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row := f.rows[taskID]; row != nil {
		row[field] = value
	}
}

// putRow 直接塞一条权威行（制造「仅权威有」/ 恢复用例的存量库行）。
func (f *fakeData) putRow(row map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[sval(row["task_id"])] = row
}

// buildRow 复刻 persist.tasktreeUpsert 的行构造（P2：扩展字段在默认路径同样落库）。
func buildRow(data map[string]any) map[string]any {
	kind := sval(data["kind"])
	nodeType := "tool"
	if kind == "llm" {
		nodeType = "session"
	}
	title := sval(data["title"])
	if title == "" {
		title = sval(data["name"])
	}
	row := map[string]any{
		"node_id":        sval(data["task_id"]),
		"node_type":      nodeType,
		"top_session":    sval(data["top_session"]),
		"session_id":     sval(data["session_id"]),
		"task_id":        sval(data["task_id"]),
		"kind":           kind,
		"parent_node_id": sval(data["parent_node_id"]),
		"title":          title,
		"status":         sval(data["status"]),
		"created_at":     sval(data["created_at"]),
	}
	if f := sval(data["finished_at"]); f != "" {
		row["finished_at"] = f
	}
	if tc := sval(data["tool_call_id"]); tc != "" {
		row["tool_call_id"] = tc
	}
	for _, k := range []string{
		"instance_id", "workdir", "state", "args_digest", "result_digest",
		"exec_json", "started_at", "done_at", "closed", "deleted_at",
	} {
		if v, ok := data[k]; ok {
			row[k] = v
		}
	}
	return row
}

// tasksView 复刻 persist.tasktreeTasks 的快照形态（精简，供启动恢复按 top_session 分组）。
func tasksView(row map[string]any) map[string]any {
	return map[string]any{
		"task_id":     sval(row["node_id"]),
		"status":      sval(row["status"]),
		"kind":        sval(row["kind"]),
		"session_id":  sval(row["session_id"]),
		"top_session": sval(row["top_session"]),
		"name":        sval(row["title"]),
	}
}

// boolVal 复刻 persist 的布尔字段解析（bool / "true" / "1"）。
func boolVal(v any) bool {
	switch n := v.(type) {
	case bool:
		return n
	case string:
		return n == "true" || n == "1"
	}
	return false
}

// newLayer 建层 + 桩（层已 Start）。
func newLayer(t *testing.T) (*Layer, *fakeData) {
	t.Helper()
	return newLayerWith(t, Options{})
}

// newLayerWith 建层（可注入 Options）+ 桩。
func newLayerWith(t *testing.T, opts Options) (*Layer, *fakeData) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	fake := newFakeData(t, bus)
	l := New(bus, opts)
	if err := l.Start(); err != nil {
		t.Fatalf("layer.Start: %v", err)
	}
	t.Cleanup(l.Stop)
	return l, fake
}

// nodePayload 造 llm 节点快照载荷（TaskNode 广播子集 + P2 增补字段 workdir）。
func nodePayload(over map[string]any) []byte {
	ev := map[string]any{
		"task_id": "tk-1", "tool": "core_file_read", "tool_call_id": "tc-1",
		"parent_id": "tk-root", "top_session": "top-1", "kind": "tool",
		"session_id": "s-1", "turn_id": "t-1", "instance_id": "ins-test",
		"workdir": "E:/ws", "name": "读取文件", "simplified": "读取文件", "state": "running",
		"started_at": "2026-09-18T00:00:00Z", "finished_at": "",
	}
	for k, v := range over {
		if v == nil {
			delete(ev, k)
			continue
		}
		ev[k] = v
	}
	b, _ := json.Marshal(ev)
	return b
}

// reportPayload 造 gateway mcp-tasks-report 载荷。
func reportPayload(over map[string]any) []byte {
	ev := map[string]any{
		"task_id": "tk-1", "tool": "core_read", "tool_call_id": "tc-1",
		"state": "done", "result_summary": "done", "session": "s-1",
		"turn": "t-1", "instance_id": "ins-test",
	}
	for k, v := range over {
		if v == nil {
			delete(ev, k)
			continue
		}
		ev[k] = v
	}
	b, _ := json.Marshal(ev)
	return b
}
