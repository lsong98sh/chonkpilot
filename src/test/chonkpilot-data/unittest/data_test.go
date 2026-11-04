// data 单测（对齐 12-数据层：建表/迁移/CRUD/Query/游标/分根/seed 幂等）。
package data_test

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

func openLayer(t *testing.T, layer data.Layer) *data.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chonkpilot.db")
	db, err := data.OpenLayer(path, layer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestBuckets 按层建桶（不再三层同构）——各层只建自己实际使用的桶；
// prj 层不再有 file_list（vfts 清单插件自持）；prjusr 不再有 index_queue
// （索引由 vfts/codegraph 插件订阅 filesys.changed 自管）。
// 2026-10-25：迁移体系整体移除，库结构 = bucketsFor 直建（无 _meta/_migrations）。
func TestBuckets(t *testing.T) {
	cases := []struct {
		layer   data.Layer
		has     []string
		missing []string
	}{
		{data.LayerUsr, []string{"config", "llms"}, []string{"sessions", "file_list", "inbox_items", "index_queue"}},
		{data.LayerPrj, []string{"config"}, []string{"llms", "sessions", "inbox_items", "file_list", "index_queue"}},
		// 2026-11-03：prjusr 只建「宿主/server 独占」的表（config + 对话目录 + 进度/名册 + 事件）；
		// 会话正文桶与二级索引已随旧布局退场 → 不再创建（读写都只走对话库）。
		{data.LayerPrjUsr, []string{"config", "dialogs", "turn_events", "memory_extract", "group_participants", "group_round_targets"}, []string{"sessions", "turns", "messages", "tasktree", "task_shadow", "inbox_items", "sessions_by_top", "messages_by_session", "file_list", "index_queue"}},
		// SESSION-DB-2：对话库 = Session 独占，只建会话正文域；配置（config）与对话目录（dialogs）
		// 归宿主/server 独占库，不得出现在对话库内。
		{data.LayerDialog, []string{"sessions", "turns", "messages", "inbox_items"}, []string{"config", "dialogs", "llms"}},
	}
	for _, c := range cases {
		db := openLayer(t, c.layer)
		for _, bucket := range c.has {
			if _, err := db.Table(bucket).ListKeys(); err != nil {
				t.Fatalf("%s should have %s bucket: %v", c.layer, bucket, err)
			}
		}
		for _, bucket := range c.missing {
			if db.HasBucket(bucket) {
				t.Fatalf("%s 不应有 %s 桶（按层建桶）", c.layer, bucket)
			}
		}
	}
}

func TestTableCRUD(t *testing.T) {
	db := openLayer(t, data.LayerUsr)
	tb := db.Table("t1")

	if err := tb.Insert("k1", data.Record{"name": "a", "n": 1}); err != nil {
		t.Fatal(err)
	}
	if err := tb.Insert("k1", nil); !errors.Is(err, data.ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
	var rec data.Record
	ok, err := tb.Get("k1", &rec)
	if err != nil || !ok {
		t.Fatalf("get k1: ok=%v err=%v", ok, err)
	}
	if rec["name"] != "a" || rec[data.KeyField] != "k1" {
		t.Fatalf("rec=%v", rec)
	}
	if err := tb.Update("k2", data.Record{"x": 1}); !errors.Is(err, data.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := tb.Upsert("k1", data.Record{"name": "a2"}); err != nil {
		t.Fatal(err)
	}
	ok, _ = tb.Get("k1", &rec)
	if rec["name"] != "a2" || rec["updated_at"] == nil {
		t.Fatalf("after upsert rec=%v", rec)
	}
	if err := tb.Delete("k9"); !errors.Is(err, data.ErrNotFound) {
		t.Fatalf("want ErrNotFound delete, got %v", err)
	}
	if err := tb.Delete("k1"); err != nil {
		t.Fatal(err)
	}
	ok, _ = tb.Get("k1", &rec)
	if ok {
		t.Fatal("k1 should be deleted")
	}
}

func TestQueryFilterSortPaging(t *testing.T) {
	db := openLayer(t, data.LayerUsr)
	tb := db.Table("items")
	for i := 1; i <= 10; i++ {
		cat := "a"
		if i > 5 {
			cat = "b"
		}
		tb.Insert(itob(i), data.Record{"id": i, "cat": cat, "ts": i})
	}

	// Where 过滤
	recs, _, _ := tb.Query(data.Query{Where: map[string]any{"cat": "a"}})
	if len(recs) != 5 {
		all, _, _ := tb.Query(data.Query{})
		t.Logf("all=%d recs: %v", len(all), all)
		t.Fatalf("where cat=a got %d", len(recs))
	}
	// 排序 + Limit
	recs, _, _ = tb.Query(data.Query{OrderBy: "id", OrderDesc: true, Limit: 3})
	if len(recs) != 3 || recs[0]["id"] != float64(10) {
		t.Fatalf("desc top3 first=%v", recs[0])
	}
}

func TestQueryCursorContinuous(t *testing.T) {
	db := openLayer(t, data.LayerUsr)
	tb := db.Table("msgs")
	for i := 1; i <= 10; i++ {
		tb.Insert(itob(i), data.Record{"id": i, "created_at": i})
	}

	// 游标连续滚动：Limit 3 一页，拉到全部 10 条，不重不漏
	seen := map[string]bool{}
	recs, next, _ := tb.Query(data.Query{OrderBy: "created_at", OrderDesc: true, Limit: 3})
	for len(recs) > 0 {
		for _, r := range recs {
			k := r[data.KeyField].(string)
			if seen[k] {
				t.Fatalf("duplicate key %s", k)
			}
			seen[k] = true
		}
		if next == "" {
			break
		}
		recs, next, _ = tb.Query(data.Query{OrderBy: "created_at", OrderDesc: true, Limit: 3, Cursor: next})
	}
	if len(seen) != 10 {
		t.Fatalf("cursor paging got %d (want 10)", len(seen))
	}
	// 每页 3 条 → next_cursor 存在直到最后一页
	_, next, _ = tb.Query(data.Query{OrderBy: "created_at", OrderDesc: true, Limit: 3})
	if next == "" {
		t.Fatal("first page should have next_cursor")
	}
}

func itob(i int) string {
	return string(rune('a' + i))
}

// TestNoSeed v6：三层均不预写默认值——缺 key 即沿 fallback 继承系统常量/资源，
// 保证"删本层即恢复继承"语义（12-数据层）。
func TestNoSeed(t *testing.T) {
	for _, layer := range []data.Layer{data.LayerUsr, data.LayerPrj, data.LayerPrjUsr} {
		db := openLayer(t, layer)
		keys, err := db.Table("config").ListKeys()
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 0 {
			t.Fatalf("%s should have no seed, got %v", layer, keys)
		}
	}
}

// TestPaths v6：三级路径解析（prj 常规/临时形态 + prjusr 数据根）。
func TestPaths(t *testing.T) {
	wd := t.TempDir()
	dd := t.TempDir()

	// prj 常规形态 → <workdir>/.chonkpilot/chonkpilot.db
	if p := data.ProjectPath(wd, ""); p != filepath.Join(wd, ".chonkpilot", "chonkpilot.db") {
		t.Fatalf("ProjectPath(workDir) = %s", p)
	}
	// prj 临时形态（CLI）→ <dataDir>/chonkpilot.db
	if p := data.ProjectPath(wd, dd); p != filepath.Join(dd, "chonkpilot.db") {
		t.Fatalf("ProjectPath(dataDir) = %s", p)
	}

	// prjusr 数据根与主库路径 = <dataRoot>/users/<OS 用户名>[/chonkpilot.db]（49 DATAROOT-1）
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	data.SetDataHome(filepath.Join(root, "chonkpilot.db"), dataRoot)
	t.Cleanup(func() { data.SetDataHome("", "") })
	if p := data.PrjUsrPath(wd, ""); p != filepath.Join(dataRoot, "users", data.UserDirName()) {
		t.Fatalf("PrjUsrPath = %s", p)
	}
	if p := data.PrjUsrDBPath(wd, ""); p != filepath.Join(dataRoot, "users", data.UserDirName(), "chonkpilot.db") {
		t.Fatalf("PrjUsrDBPath = %s", p)
	}
	if p := data.UserPath(); p != filepath.Join(root, "chonkpilot.db") {
		t.Fatalf("UserPath = %s", p)
	}
}

// TestSetDataHomeConcurrent：SetDataHome（运行期可被 persist.New / CLI 数据根准备调用）与
// UserPath/DataRoot/PrjUsrPath（任意 goroutine 并发读）并发执行无数据竞争 —— 用 `-race` 运行验证。
func TestSetDataHomeConcurrent(t *testing.T) {
	t.Cleanup(func() { data.SetDataHome("", "") })
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			data.SetDataHome(filepath.Join("x", "usr.db"), filepath.Join("x", "data"))
		}
	}()
	for i := 0; i < 500; i++ {
		_ = data.UserPath()
		_ = data.PrjUsrPath("wd", "")
	}
	wg.Wait()
}
