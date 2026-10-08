// data-tasktree-* 影子域（21 任务层设计方案 §9.1 P1 交付物 2）：**复用既有 data-tasktree-*
// 方法面**，仅多带 `shadow: true` 路由到影子桶 task_shadow（+ 三个索引桶）。
// 断言：① 影子路径读写正确（含索引与级联删除、不广播 task-deleted）；② 默认路径（不传 shadow）
// 行为不变；③ 影子与 tasktree **互不影响**（各自行数与内容）。
package persist_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
)

// TestDataTasktreeShadowPath：影子路径读写 + 索引维护 + 隔离（不动 tasktree）。
func TestDataTasktreeShadowPath(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	prj := seedTasktree(t, bus) // tasktree: n1(llm/running) → n2(tool/done)

	// 影子 upsert（同一方法面 + shadow:true；带影子扩展字段）
	r := dataCall(t, bus, "data-tasktree-upsert", map[string]any{
		"req_id": "r1", "instance_id": "ins-test",
		"data": map[string]any{
			"task_id": "n3", "top_session": "top1", "session_id": "s1",
			"kind": "tool", "parent_node_id": "n1", "title": "影子工具",
			"status": "running", "state": "running", "created_at": "2026-09-18T00:00:00Z",
			"tool_call_id": "tc-3", "instance_id": "ins-test", "work_dir": "E:/ws",
			"shadow": true,
		},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("shadow upsert failed: %+v", r)
	}

	// 影子行落 task_shadow（含扩展字段），tasktree 无 n3
	var rec data.Record
	if ok, _ := prj.Table("task_shadow").Get("n3", &rec); !ok {
		t.Fatal("影子行未写入 task_shadow")
	}
	if rec["status"] != "running" || rec["node_type"] != "tool" || rec["work_dir"] != "E:/ws" ||
		rec["parent_node_id"] != "n1" {
		t.Fatalf("影子行字段错: %+v", rec)
	}
	if ok, _ := prj.Table("tasktree").Get("n3", &data.Record{}); ok {
		t.Fatal("影子写不得落到 tasktree")
	}

	// 索引桶（由 Table 写入口维护；by_top / by_session 键含 created_at 段，by_parent 无排序段）
	for _, tc := range []struct{ bucket, prefix string }{
		{"task_shadow_by_top", "top1\x00"},
		{"task_shadow_by_session", "s1\x00"},
		{"task_shadow_by_parent", "n1\x00"},
	} {
		keys, err := prj.Table(tc.bucket).ListPrefix(tc.prefix)
		if err != nil || len(keys) != 1 {
			t.Fatalf("影子索引缺失: %s prefix=%q keys=%v err=%v", tc.bucket, tc.prefix, keys, err)
		}
	}

	// 读：shadow:true → 仅影子（1 条）；默认 → 仍 2 条（既有行）
	r = dataCall(t, bus, "data-tasktree-list", map[string]any{
		"req_id": "r2", "instance_id": "ins-test",
		"data": map[string]any{"top_session": "top1", "shadow": true},
	})
	nodes, _ := dataResult(t, r)["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("影子 list=%+v", nodes)
	}
	if n, _ := nodes[0].(map[string]any); n["node_id"] != "n3" || n["title"] != "影子工具" {
		t.Fatalf("影子 list 行=%+v", nodes[0])
	}
	r = dataCall(t, bus, "data-tasktree-list", map[string]any{
		"req_id": "r3", "instance_id": "ins-test",
		"data": map[string]any{"top_session": "top1"},
	})
	nodes, _ = dataResult(t, r)["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("默认 list 应仍 2 条: %+v", nodes)
	}

	// tasks（影子）
	r = dataCall(t, bus, "data-tasktree-tasks", map[string]any{
		"req_id": "r4", "instance_id": "ins-test",
		"data": map[string]any{"session_id": "s1", "shadow": true},
	})
	list, _ := dataResult(t, r)["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("影子 tasks=%+v", list)
	}
	if m, _ := list[0].(map[string]any); m["task_id"] != "n3" {
		t.Fatalf("影子 tasks 行=%+v", list[0])
	}

	// 影子删除：级联删影子子树 + 影子索引，**不广播** task-deleted，tasktree 原样
	deletedCh := make(chan map[string]any, 1)
	if _, err := subRaw(bus, "task-deleted", func(_ string, p []byte) {
		var m map[string]any
		if json.Unmarshal(p, &m) == nil {
			select {
			case deletedCh <- m:
			default:
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	r = dataCall(t, bus, "data-tasktree-delete", map[string]any{
		"req_id": "r5", "instance_id": "ins-test",
		"data": map[string]any{"node_id": "n3", "shadow": true},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("shadow delete failed: %+v", r)
	}
	if ok, _ := prj.Table("task_shadow").Get("n3", &data.Record{}); ok {
		t.Fatal("影子行未删除")
	}
	if keys, _ := prj.Table("task_shadow_by_top").ListPrefix("top1\x00"); len(keys) != 0 {
		t.Fatalf("影子索引未清理（物理删除应同事务清索引）: %v", keys)
	}
	select {
	case m := <-deletedCh:
		t.Fatalf("影子删除不得广播 task-deleted: %+v", m)
	case <-time.After(300 * time.Millisecond):
	}
	// tasktree 完好
	if keys, _ := prj.Table("tasktree").ListKeys(); len(keys) != 2 {
		t.Fatalf("tasktree 行数=%v want 2", keys)
	}
}

// TestDataTasktreeDefaultPathUnchanged：默认路径（不传 shadow / 显式 shadow:false）行为不变，
// 且与影子桶互不影响。
func TestDataTasktreeDefaultPathUnchanged(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	prj := seedTasktree(t, bus)

	// 默认 upsert（不传 shadow）→ 落 tasktree，不落影子
	r := dataCall(t, bus, "data-tasktree-upsert", map[string]any{
		"req_id": "r1", "instance_id": "ins-test",
		"data": map[string]any{
			"task_id": "n9", "top_session": "top1", "session_id": "s1", "kind": "llm",
			"parent_node_id": "", "title": "默认节点", "status": "running",
			"created_at": "2026-09-18T00:00:00Z",
		},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("默认 upsert failed: %+v", r)
	}
	var rec data.Record
	if ok, _ := prj.Table("tasktree").Get("n9", &rec); !ok {
		t.Fatal("默认路径应落 tasktree")
	}
	if rec["node_type"] != "session" { // kind=llm → node_type=session（既有语义）
		t.Fatalf("默认路径 node_type=%v want session", rec["node_type"])
	}
	if ok, _ := prj.Table("task_shadow").Get("n9", &data.Record{}); ok {
		t.Fatal("默认路径不得污染影子桶")
	}

	// 显式 shadow:false → 同默认路径
	r = dataCall(t, bus, "data-tasktree-upsert", map[string]any{
		"req_id": "r2", "instance_id": "ins-test",
		"data": map[string]any{
			"task_id": "n10", "top_session": "top1", "kind": "tool", "title": "显式非影子",
			"status": "done", "created_at": "2026-09-18T00:00:00Z", "shadow": false,
		},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("shadow:false upsert failed: %+v", r)
	}
	if ok, _ := prj.Table("tasktree").Get("n10", &data.Record{}); !ok {
		t.Fatal("shadow:false 应落 tasktree")
	}
	if ok, _ := prj.Table("task_shadow").Get("n10", &data.Record{}); ok {
		t.Fatal("shadow:false 不得落影子桶")
	}

	// 默认 list/tasks 只见 tasktree（4 条 = n1/n2/n9/n10），影子桶为空 → 影子 list 0 条
	r = dataCall(t, bus, "data-tasktree-list", map[string]any{
		"req_id": "r3", "instance_id": "ins-test",
		"data": map[string]any{"top_session": "top1"},
	})
	if nodes, _ := dataResult(t, r)["nodes"].([]any); len(nodes) != 4 {
		t.Fatalf("默认 list=%d want 4", len(nodes))
	}
	r = dataCall(t, bus, "data-tasktree-list", map[string]any{
		"req_id": "r4", "instance_id": "ins-test",
		"data": map[string]any{"top_session": "top1", "shadow": true},
	})
	if nodes, _ := dataResult(t, r)["nodes"].([]any); len(nodes) != 0 {
		t.Fatalf("影子 list 应空: %+v", nodes)
	}

	// 默认删除仍级联 + 广播 + 不碰影子桶
	deletedCh := make(chan map[string]any, 1)
	if _, err := subRaw(bus, "task-deleted", func(_ string, p []byte) {
		var m map[string]any
		if json.Unmarshal(p, &m) == nil {
			select {
			case deletedCh <- m:
			default:
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	r = dataCall(t, bus, "data-tasktree-delete", map[string]any{
		"req_id": "r5", "instance_id": "ins-test", "id": "n1",
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("默认 delete failed: %+v", r)
	}
	select {
	case m := <-deletedCh:
		if m["node_id"] != "n1" {
			t.Fatalf("task-deleted=%+v", m)
		}
		// I-93：默认路径删除的 task-deleted 同样带 instance_id（61 §3.4）
		if m["instance_id"] != "ins-test" {
			t.Fatalf("task-deleted 应带 instance_id=ins-test: %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("默认删除未广播 task-deleted")
	}
}
