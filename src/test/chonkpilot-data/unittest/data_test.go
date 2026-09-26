// data 单测（对齐 12-数据层：建表/迁移/CRUD/Query/游标/分根/seed 幂等）。
package data_test

import (
	"errors"
	"path/filepath"
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

// TestMigrationBuckets v6：三层（usr/prj/prjusr）同构——每层都有同一套桶。
func TestMigrationBuckets(t *testing.T) {
	for _, layer := range []data.Layer{data.LayerUsr, data.LayerPrj, data.LayerPrjUsr} {
		db := openLayer(t, layer)
		for _, bucket := range []string{"config", "llms", "mcps", "sessions", "tasktree"} {
			if _, err := db.Table(bucket).ListKeys(); err != nil {
				t.Fatalf("%s should have %s bucket: %v", layer, bucket, err)
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

	// prjusr 数据根与主库路径 = <dataRoot>/<project-id>[/chonkpilot.db]
	root := t.TempDir()
	data.SetDataHome(filepath.Join(root, "chonkpilot.db"), filepath.Join(root, "data"))
	t.Cleanup(func() { data.SetDataHome("", "") })
	if p := data.PrjUsrPath("pid-1"); p != filepath.Join(root, "data", "pid-1") {
		t.Fatalf("PrjUsrPath = %s", p)
	}
	if p := data.PrjUsrDBPath("pid-1"); p != filepath.Join(root, "data", "pid-1", "chonkpilot.db") {
		t.Fatalf("PrjUsrDBPath = %s", p)
	}
	if p := data.UserPath(); p != filepath.Join(root, "chonkpilot.db") {
		t.Fatalf("UserPath = %s", p)
	}
}

// TestConfigOpen v6：三层全开 + prj 库自动生成 project-id + prjusr 落在 data/<id>。
func TestConfigOpen(t *testing.T) {
	root := t.TempDir()
	data.SetDataHome(filepath.Join(root, "usr", "chonkpilot.db"), filepath.Join(root, "data"))
	t.Cleanup(func() {
		data.Reset()
		data.SetDataHome("", "")
	})

	wd := t.TempDir()
	cfg, err := data.OpenConfig(wd, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()

	if cfg.Usr().Layer() != data.LayerUsr || cfg.Prj().Layer() != data.LayerPrj || cfg.PrjUsr().Layer() != data.LayerPrjUsr {
		t.Fatal("layer mismatch")
	}
	if _, err := cfg.PrjUsr().Table("sessions").ListKeys(); err != nil {
		t.Fatalf("prjusr sessions bucket missing: %v", err)
	}
	// project-id 首次生成并写入 prj 库
	id, ok := data.ReadProjectID(cfg.Prj())
	if !ok || id == "" {
		t.Fatal("project-id should be generated on first open")
	}
	if cfg.PrjUsr().Path() != data.PrjUsrDBPath(id) {
		t.Fatalf("prjusr path = %s, want %s", cfg.PrjUsr().Path(), data.PrjUsrDBPath(id))
	}
	// 复用：再次打开同一 workdir → 同一 project-id
	cfg.Close()
	cfg2, err := data.OpenConfig(wd, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg2.Close()
	if id2, _ := data.ReadProjectID(cfg2.Prj()); id2 != id {
		t.Fatalf("project-id should be stable: %s vs %s", id2, id)
	}
}
