// 文件清单（file_list）域往返：data-filelist-{list,put,del}——
// put 单条 upsert（按 key）落**项目级 prj 库** file_list 表；list 全量/前缀/分页；
// del 按 key 批量删除（缺失视为成功但不计 deleted）；未登记实例 → 失败应答。
package persist_test

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

func TestDataFileListPutListDel(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	put := func(reqID, key, path string, size float64, mtime, md5 string, docIDs []any, chunks float64, indexedAt string) {
		t.Helper()
		r := dataCall(t, bus, "data-filelist-put", map[string]any{
			"req_id": reqID, "instance_id": "ins-test",
			"data": map[string]any{
				"key": key, "path": path, "size": size, "mtime": mtime, "md5": md5,
				"doc_ids": docIDs, "chunks": chunks, "indexed_at": indexedAt,
			},
		})
		res := dataResult(t, r)
		if ok, _ := res["ok"].(bool); !ok || res["id"] != key {
			t.Fatalf("put %s failed: %+v", key, r)
		}
	}

	// 落库断言（prj 主库 file_list 表）
	directGet := func(key string) (data.Record, bool) {
		t.Helper()
		db, err := data.Prj("ins-test")
		if err != nil {
			t.Fatal(err)
		}
		var rec data.Record
		ok, err := db.Table("file_list").Get(key, &rec)
		if err != nil {
			t.Fatal(err)
		}
		return rec, ok
	}

	put("f1", "k1", "C:/ws/a.txt", 12, "2026-09-12T10:00:00Z", "md5-a", []any{"1", "2"}, 2, "2026-09-12T10:00:01Z")
	put("f2", "k2", "C:/ws/sub/b.go", 34, "2026-09-12T11:00:00Z", "md5-b", []any{"3"}, 1, "2026-09-12T11:00:01Z")

	// 落库 + doc_ids 机制（按文件删除所需的主键列表）落盘
	rec, ok := directGet("k1")
	if !ok || rec["path"] != "C:/ws/a.txt" || rec["md5"] != "md5-a" {
		t.Fatalf("k1 未落库: ok=%v rec=%+v", ok, rec)
	}
	ids, _ := rec["doc_ids"].([]any)
	if len(ids) != 2 || ids[0] != "1" || ids[1] != "2" {
		t.Fatalf("k1 doc_ids=%+v", rec["doc_ids"])
	}
	if v, _ := rec["chunks"].(float64); v != 2 {
		t.Fatalf("k1 chunks=%v", rec["chunks"])
	}

	// list 全量
	r := dataCall(t, bus, "data-filelist-list", map[string]any{"req_id": "l1", "instance_id": "ins-test"})
	res := dataResult(t, r)
	list := dataList(res)
	if len(list) != 2 {
		t.Fatalf("list n=%d want 2: %+v", len(list), list)
	}
	if v, _ := res["total"].(float64); v != 2 {
		t.Fatalf("total=%v", res["total"])
	}
	first, _ := list[0].(map[string]any)
	if first["key"] != "k1" || first["mtime"] != "2026-09-12T10:00:00Z" || first["indexed_at"] != "2026-09-12T10:00:01Z" {
		t.Fatalf("记录字段往返不一致: %+v", first)
	}

	// list 前缀过滤（按 path）
	r = dataCall(t, bus, "data-filelist-list", map[string]any{
		"req_id": "l2", "instance_id": "ins-test",
		"data": map[string]any{"prefix": "C:/ws/sub/"},
	})
	res = dataResult(t, r)
	if list = dataList(res); len(list) != 1 {
		t.Fatalf("prefix 过滤 n=%d want 1: %+v", len(list), list)
	}
	if v, _ := res["total"].(float64); v != 1 {
		t.Fatalf("prefix total=%v", res["total"])
	}

	// list 分页（offset/limit）
	r = dataCall(t, bus, "data-filelist-list", map[string]any{
		"req_id": "l3", "instance_id": "ins-test",
		"data": map[string]any{"offset": 1, "limit": 1},
	})
	res = dataResult(t, r)
	if list = dataList(res); len(list) != 1 {
		t.Fatalf("分页 n=%d want 1: %+v", len(list), list)
	}
	if m, _ := list[0].(map[string]any); m["key"] != "k2" {
		t.Fatalf("分页应取第二条: %+v", list[0])
	}
	if v, _ := res["total"].(float64); v != 2 {
		t.Fatalf("分页 total 应为过滤后总数: %v", res["total"])
	}

	// upsert：同 key 覆盖（模拟引擎重建后新 doc_ids）
	put("f3", "k1", "C:/ws/a.txt", 20, "2026-09-12T12:00:00Z", "md5-a2", []any{"7"}, 1, "2026-09-12T12:00:01Z")
	if rec, _ := directGet("k1"); rec["md5"] != "md5-a2" {
		t.Fatalf("upsert 未覆盖: %+v", rec)
	}
	r = dataCall(t, bus, "data-filelist-list", map[string]any{"req_id": "l4", "instance_id": "ins-test"})
	if list = dataList(dataResult(t, r)); len(list) != 2 {
		t.Fatalf("upsert 后应仍 2 条: %+v", list)
	}

	// del 批量（含一个不存在的 key：不影响已删除计数）
	r = dataCall(t, bus, "data-filelist-del", map[string]any{
		"req_id": "d1", "instance_id": "ins-test",
		"data": map[string]any{"keys": []any{"k1", "k2", "k-ghost"}},
	})
	res = dataResult(t, r)
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("del failed: %+v", r)
	}
	if v, _ := res["deleted"].(float64); v != 2 {
		t.Fatalf("deleted=%v want 2", res["deleted"])
	}
	if _, ok := directGet("k1"); ok {
		t.Fatal("k1 应已删除")
	}
	r = dataCall(t, bus, "data-filelist-list", map[string]any{"req_id": "l5", "instance_id": "ins-test"})
	if list = dataList(dataResult(t, r)); len(list) != 0 {
		t.Fatalf("del 后应为空: %+v", list)
	}

	// 未登记实例 → 失败应答
	r = dataCall(t, bus, "data-filelist-list", map[string]any{"req_id": "l6", "instance_id": "ins-ghost"})
	if ok, _ := r["ok"].(bool); ok {
		t.Fatalf("未登记实例应失败: %+v", r)
	}
}
