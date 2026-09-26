// data-tasktree-* 消息面测试（B 类随迁）：list/tasks/delete（级联 + 索引桶清理 + task-deleted 事件）。
// 外部模块黑盒（package persist_test）：经 persist.New/Start/Stop + 总线 data-* 主题驱动；
// 种子落库经 data.Prj 直连（regInstance 已 data.Register）。
package persist_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// seedTasktree 造任务树：n1(llm/running, top1) → n2(tool/done)。
func seedTasktree(t *testing.T, bus mq.Bus) *data.DB {
	t.Helper()
	regInstance(t, bus)
	prj, err := data.PrjUsr("ins-test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_ = prj.Table("tasktree").Upsert("n1", data.Record{
		"node_id": "n1", "session_id": "s1", "top_session": "top1",
		"parent_node_id": "", "kind": "llm", "status": "running",
		"title": "根任务", "created_at": now,
	})
	_ = prj.Table("tasktree").Upsert("n2", data.Record{
		"node_id": "n2", "session_id": "s1", "top_session": "top1",
		"parent_node_id": "n1", "kind": "tool", "status": "done",
		"title": "子工具", "created_at": now,
		"tool_call_id": "tc-n2", // I-97：tasks 视图须带 tool_call_id（供按 tool_call_id 定位/取消）
	})
	// 索引桶（server 侧同款结构：<父键>\x00<子 node_id>）
	_ = prj.Table("tasktree_by_parent").Upsert("n1\x00n2", data.Record{"parent_node_id": "n1", "node_id": "n2"})
	return prj
}

// TestDataTasktreeListTasks：list（含 mode=init 存活过滤）与 tasks（快照数组）。
func TestDataTasktreeListTasks(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	seedTasktree(t, bus)

	r := dataCall(t, bus, "data-tasktree-list", map[string]any{
		"req_id": "r1", "instance_id": "ins-test", "data": map[string]any{"top_session": "top1"},
	})
	nodes, _ := dataResult(t, r)["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("nodes=%+v", nodes)
	}

	// init：仅 llm + running
	r = dataCall(t, bus, "data-tasktree-list", map[string]any{
		"req_id": "r2", "instance_id": "ins-test", "data": map[string]any{"top_session": "top1", "mode": "init"},
	})
	nodes, _ = dataResult(t, r)["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("init nodes=%+v", nodes)
	}
	if n, _ := nodes[0].(map[string]any); n["node_id"] != "n1" {
		t.Fatalf("init node=%+v", nodes[0])
	}

	// tasks：session 过滤 → 快照
	r = dataCall(t, bus, "data-tasktree-tasks", map[string]any{
		"req_id": "r3", "instance_id": "ins-test", "data": map[string]any{"session_id": "s1"},
	})
	list, _ := dataResult(t, r)["list"].([]any)
	if len(list) != 2 {
		t.Fatalf("tasks=%+v", list)
	}
	// tasks 视图增补 tool_call_id（I-97）：与 data-tasktree-list 口径一致，
	// 调用方据此可直接发起 task-stop{tool_call_id}。
	if t2, _ := list[1].(map[string]any); t2["task_id"] != "n2" || t2["status"] != "done" || t2["tool_call_id"] != "tc-n2" {
		t.Fatalf("task=%+v", list[1])
	}
}

// TestDataTasktreeTasksAwaiting：tasks 视图**增补 `awaiting` 对象**（I-99）——层权威 `state=="awaiting"`
// 且 `exec_json` 携带 options 时输出 `{reason, options, timeout_s}`（从 exec_json 解析，不新增库表字段）；
// 非 awaiting / 无 options → **不输出该字段**（存在即带、缺省不加，缺省与既有载荷逐字节等价）。
func TestDataTasktreeTasksAwaiting(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	prj, err := data.PrjUsr("ins-test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// aw1：待裁决（state=awaiting + exec_json.detail 含 timeout_s/options）→ 须输出 awaiting
	_ = prj.Table("tasktree").Upsert("aw1", data.Record{
		"node_id": "aw1", "session_id": "s-aw", "top_session": "top-aw", "kind": "tool",
		"status": "running", "state": "awaiting", "title": "慢工具", "created_at": now,
		"tool_call_id": "tc-aw",
		"exec_json": `{"gw_task_id":"tk-0001","phase":"awaiting","state":"running",` +
			`"detail":{"mode":"manual","timeout_s":30,"options":["detach","cancel"]}}`,
	})
	// run1：非 awaiting（state=running）+ exec_json 有明细 → 不得输出 awaiting
	_ = prj.Table("tasktree").Upsert("run1", data.Record{
		"node_id": "run1", "session_id": "s-aw", "top_session": "top-aw", "kind": "tool",
		"status": "running", "state": "running", "title": "在飞工具", "created_at": now,
		"tool_call_id": "tc-run",
		"exec_json":    `{"phase":"started","detail":{"timeout_s":30,"options":["wait","cancel"]}}`,
	})
	// det1：awaiting 但 exec_json 无 options → 不输出（存在即带）
	_ = prj.Table("tasktree").Upsert("det1", data.Record{
		"node_id": "det1", "session_id": "s-aw", "top_session": "top-aw", "kind": "tool",
		"status": "running", "state": "awaiting", "title": "无选项", "created_at": now,
		"exec_json": `{"phase":"awaiting","detail":{"timeout_s":9}}`,
	})

	r := dataCall(t, bus, "data-tasktree-tasks", map[string]any{
		"req_id": "r1", "instance_id": "ins-test", "data": map[string]any{"session_id": "s-aw"},
	})
	list, _ := dataResult(t, r)["list"].([]any)
	if len(list) != 3 {
		t.Fatalf("tasks=%+v", list)
	}
	byID := map[string]map[string]any{}
	for _, it := range list {
		if m, ok := it.(map[string]any); ok {
			byID[m["task_id"].(string)] = m
		}
	}
	aw, _ := byID["aw1"]["awaiting"].(map[string]any)
	if aw == nil {
		t.Fatalf("awaiting 行未输出 awaiting 字段: %+v", byID["aw1"])
	}
	if aw["reason"] != "timeout" {
		t.Fatalf("awaiting.reason=%v want timeout", aw["reason"])
	}
	opts, _ := aw["options"].([]any)
	if len(opts) != 2 || opts[0] != "detach" || opts[1] != "cancel" {
		t.Fatalf("awaiting.options=%v want [detach cancel]", aw["options"])
	}
	if ts, _ := aw["timeout_s"].(float64); ts != 30 {
		t.Fatalf("awaiting.timeout_s=%v want 30", aw["timeout_s"])
	}
	if _, ok := byID["run1"]["awaiting"]; ok {
		t.Fatalf("非 awaiting 行不得输出 awaiting: %+v", byID["run1"])
	}
	if _, ok := byID["det1"]["awaiting"]; ok {
		t.Fatalf("exec_json 缺 options 不得输出 awaiting: %+v", byID["det1"])
	}
}

// TestDataTasktreeDelete：「关闭任务」= **逻辑删除**（21 任务层设计方案 §8.1-2 / §9.2 P2 ④）：
// 级联子树逐行标 closed/deleted_at（**行保留、历史可查**） + 索引桶清理 + task-deleted 事件广播；
// list/tasks 默认过滤 closed，带 include_closed 时可见。
func TestDataTasktreeDelete(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	seedTasktree(t, bus)

	// 订阅 task-deleted（persist 逻辑删除后广播；server onTaskTreeDeleted 同款监听）
	ev := make(chan map[string]any, 1)
	_, err := subRaw(bus, "task-deleted", func(_ string, p []byte) {
		var m map[string]any
		if json.Unmarshal(p, &m) == nil {
			select {
			case ev <- m:
			default:
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	r := dataCall(t, bus, "data-tasktree-delete", map[string]any{
		"req_id": "r1", "instance_id": "ins-test", "id": "n1",
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete failed: %+v", r)
	}

	select {
	case m := <-ev:
		if m["node_id"] != "n1" {
			t.Fatalf("task-deleted=%+v", m)
		}
		// I-93：载荷补 instance_id（61 §3.4 = {instance_id, node_id}；取自本 data 请求上下文）
		if m["instance_id"] != "ins-test" {
			t.Fatalf("task-deleted 应带 instance_id=ins-test（61 §3.4）: %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no task-deleted broadcast")
	}

	// 逻辑删除：行仍在库 + closed/deleted_at 标记（级联子树 n1 → n2 同样标记）
	prj, _ := data.PrjUsr("ins-test")
	for _, id := range []string{"n1", "n2"} {
		var rec data.Record
		ok, _ := prj.Table("tasktree").Get(id, &rec)
		if !ok {
			t.Fatalf("node %s 被物理删除（应逻辑删除保留历史）", id)
		}
		if closed, _ := rec["closed"].(bool); !closed {
			t.Fatalf("node %s closed 标记缺失: %+v", id, rec)
		}
		if at, _ := rec["deleted_at"].(string); at == "" {
			t.Fatalf("node %s deleted_at 缺失: %+v", id, rec)
		}
	}
	// 索引桶 suffix 匹配清理（n1\x00n2 → 删 n1 级联含 n2；既有行为保留）
	if ok, _ := prj.Table("tasktree_by_parent").Get("n1\x00n2", &data.Record{}); ok {
		t.Fatal("index entry not cleaned")
	}
	// 视图过滤 closed：默认 list/tasks 不再返回；include_closed=true → 可见（历史可查）
	r = dataCall(t, bus, "data-tasktree-list", map[string]any{
		"req_id": "r2", "instance_id": "ins-test", "data": map[string]any{"top_session": "top1"},
	})
	if nodes, _ := dataResult(t, r)["nodes"].([]any); len(nodes) != 0 {
		t.Fatalf("关闭后 list 应过滤 closed: %+v", nodes)
	}
	r = dataCall(t, bus, "data-tasktree-tasks", map[string]any{
		"req_id": "r3", "instance_id": "ins-test", "data": map[string]any{"top_session": "top1"},
	})
	if list, _ := dataResult(t, r)["list"].([]any); len(list) != 0 {
		t.Fatalf("关闭后 tasks 应过滤 closed: %+v", list)
	}
	r = dataCall(t, bus, "data-tasktree-list", map[string]any{
		"req_id": "r4", "instance_id": "ins-test",
		"data": map[string]any{"top_session": "top1", "include_closed": true},
	})
	nodes, _ := dataResult(t, r)["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("include_closed 应返回历史行（2 条）: %+v", nodes)
	}
}
