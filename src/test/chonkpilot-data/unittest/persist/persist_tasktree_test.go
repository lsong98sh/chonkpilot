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
	// 索引桶由 Table 写入口同事务维护（indexes.go），无需手工播种。
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
	// 索引随主行维护：逻辑删除保留行 → 索引键仍在（by_parent 键 = "<父>\x00<子>"）
	if keys, _ := prj.Table("tasktree_by_parent").ListPrefix("n1\x00"); len(keys) != 1 {
		t.Fatalf("逻辑删除保留行 → 索引键应保留: %v", keys)
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

// TestDataTasktreeDeleteMultiLevelCascade：级联删除改走 tasktree_by_parent 索引递归后，
// **多层子树 + 跨 top_session 后代**仍被完整级联关闭（结果与旧全表扫一致，O(子树)）；
// 无关子树不受影响。旧实现按 parent_node_id 级联（不看 top_session），故跨会话后代同样应关。
func TestDataTasktreeDeleteMultiLevelCascade(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	prj, err := data.PrjUsr("ins-test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	put := func(id, parent, top string) {
		_ = prj.Table("tasktree").Upsert(id, data.Record{
			"node_id": id, "session_id": "s-" + top, "top_session": top,
			"parent_node_id": parent, "kind": "tool", "status": "done", "created_at": now,
		})
	}
	// 待删子树：r1 → r2 → r3 →（r4/r5 跨 top_session，父仍是 r3）
	put("r1", "", "top1")
	put("r2", "r1", "top1")
	put("r3", "r2", "top1")
	put("r4", "r3", "top2") // 跨 top_session：旧实现按 parent_node_id 级联 → 仍应被关闭
	put("r5", "r3", "top2")
	// 无关子树：o1 → o2（不得受影响）
	put("o1", "", "top1")
	put("o2", "o1", "top1")

	r := dataCall(t, bus, "data-tasktree-delete", map[string]any{
		"req_id": "r1", "instance_id": "ins-test", "node_id": "r1",
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("delete failed: %+v", r)
	}
	closedOf := func(id string) bool {
		var rec data.Record
		ok, _ := prj.Table("tasktree").Get(id, &rec)
		if !ok {
			t.Fatalf("节点 %s 被物理删除（应逻辑删除保留行）", id)
		}
		c, _ := rec["closed"].(bool)
		return c
	}
	for _, id := range []string{"r1", "r2", "r3", "r4", "r5"} {
		if !closedOf(id) {
			t.Fatalf("级联子树节点 %s 未被关闭", id)
		}
	}
	for _, id := range []string{"o1", "o2"} {
		if closedOf(id) {
			t.Fatalf("无关子树节点 %s 被误关闭", id)
		}
	}
	// 跨 top_session 后代已关闭 → list(top2) 默认过滤后为空
	r = dataCall(t, bus, "data-tasktree-list", map[string]any{
		"req_id": "r2", "instance_id": "ins-test", "data": map[string]any{"top_session": "top2"},
	})
	if nodes, _ := dataResult(t, r)["nodes"].([]any); len(nodes) != 0 {
		t.Fatalf("跨 top_session 后代应已关闭并被过滤: %+v", nodes)
	}
}

// TestDataTasktreeDeleteCycleGuard：数据异常出现环（c1↔c2）或自环（s1→s1）时，
// 索引递归级联删除须**终止**且各节点仅处理一次（seen 去重，口径同 task/layer.go）。
// 旧全表扫 BFS 无 seen，遇环会死循环；本用例即回归护栏。
func TestDataTasktreeDeleteCycleGuard(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	prj, err := data.PrjUsr("ins-test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	put := func(id, parent string) {
		_ = prj.Table("tasktree").Upsert(id, data.Record{
			"node_id": id, "session_id": "s1", "top_session": "top1",
			"parent_node_id": parent, "kind": "tool", "status": "done", "created_at": now,
		})
	}
	put("c1", "c2") // 环：c1→c2→c1
	put("c2", "c1")
	put("s1", "s1") // 自环

	// 环删除：若不终止，dataCall 会 3s 超时失败
	r := dataCall(t, bus, "data-tasktree-delete", map[string]any{
		"req_id": "r1", "instance_id": "ins-test", "node_id": "c1",
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("环删除 failed: %+v", r)
	}
	for _, id := range []string{"c1", "c2"} {
		var rec data.Record
		if ok, _ := prj.Table("tasktree").Get(id, &rec); !ok {
			t.Fatalf("环节点 %s 被物理删除", id)
		}
		if c, _ := rec["closed"].(bool); !c {
			t.Fatalf("环节点 %s 未被关闭", id)
		}
	}
	// 自环删除：同样须终止
	r = dataCall(t, bus, "data-tasktree-delete", map[string]any{
		"req_id": "r2", "instance_id": "ins-test", "node_id": "s1",
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("自环删除 failed: %+v", r)
	}
	var rec data.Record
	if ok, _ := prj.Table("tasktree").Get("s1", &rec); !ok || rec["closed"] != true {
		t.Fatalf("自环节点 s1 未被关闭: %+v", rec)
	}
}
