// data 根包 Table DAO 原语测试：钉死本轮调整的事务原语行为——Upsert 隐式时间戳口径与
// 调用方值保留（A-16）、UpdateIn 的 nil 放弃写入与索引迁移（A-17~A-19 依赖）、ForEach
// 单事务扫描（A-20）、ReplaceAll 整体替换（A-22）。
package data

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openTestTableDB 打开临时库（测试隔离）。
func openTestTableDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// assertFixedNano 断言值为固定 9 位纳秒 RFC3339（UTC → Z 结尾；Parse 布局要求小数段恰 9 位）。
func assertFixedNano(t *testing.T, tag, v string) {
	t.Helper()
	if _, err := time.Parse(RFC3339FixedNano, v); err != nil {
		t.Fatalf("%s 不合 RFC3339FixedNano：%q (%v)", tag, v, err)
	}
	if frac := strings.TrimSuffix(strings.SplitN(v, ".", 2)[1], "Z"); len(frac) != 9 {
		t.Fatalf("%s 小数段应为 9 位：%q", tag, v)
	}
}

// TestUpsertUpdatedAtPreserveAndNano（A-16）：rec 未带 updated_at → 隐式写固定 9 位纳秒
// 口径；调用方已设置 → 保留不覆盖。
func TestUpsertUpdatedAtPreserveAndNano(t *testing.T) {
	db := openTestTableDB(t)
	tb := db.Table("things")

	if err := tb.Upsert("a", Record{"v": 1}); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	var rec Record
	if ok, err := tb.Get("a", &rec); err != nil || !ok {
		t.Fatalf("get a: ok=%v err=%v", ok, err)
	}
	ua, _ := rec["updated_at"].(string)
	assertFixedNano(t, "隐式 updated_at", ua)

	// 显式 updated_at：保留调用方值
	const explicit = "2020-01-01T00:00:00.123456789Z"
	if err := tb.Upsert("b", Record{"updated_at": explicit}); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	if ok, err := tb.Get("b", &rec); err != nil || !ok {
		t.Fatalf("get b: ok=%v err=%v", ok, err)
	}
	if got, _ := rec["updated_at"].(string); got != explicit {
		t.Fatalf("调用方 updated_at 被覆盖：got=%q want=%q", got, explicit)
	}
}

// TestUpdateInNilAborts：fn 返回 nil = 放弃写入——行不存在不建行；行存在保持原状。
func TestUpdateInNilAborts(t *testing.T) {
	db := openTestTableDB(t)
	tb := db.Table("things")

	if err := tb.UpdateIn("nope", func(rec Record) Record {
		if len(rec) != 0 {
			t.Fatalf("不存在的行应收到空记录：%v", rec)
		}
		return nil
	}); err != nil {
		t.Fatalf("updatein nope: %v", err)
	}
	if ok, _ := tb.Get("nope", new(Record)); ok {
		t.Fatal("nil 放弃写入不应建行")
	}

	// 行存在 → nil 放弃写入保持原值
	const want = "keep"
	if err := tb.Upsert("k1", Record{"v": want}); err != nil {
		t.Fatalf("upsert k1: %v", err)
	}
	if err := tb.UpdateIn("k1", func(rec Record) Record { return nil }); err != nil {
		t.Fatalf("updatein k1: %v", err)
	}
	var rec Record
	if ok, err := tb.Get("k1", &rec); err != nil || !ok {
		t.Fatalf("get k1: ok=%v err=%v", ok, err)
	}
	if got, _ := rec["v"].(string); got != want {
		t.Fatalf("nil 放弃写入不应改行：got=%q want=%q", got, want)
	}
}

// TestUpdateInIndexMigration：sessions 表带 sessions_by_top 索引（parent_id + created_at），
// fn 原地改索引列后旧索引键须清理、新键须写入（Query 前缀路由可证）。
func TestUpdateInIndexMigration(t *testing.T) {
	db := openTestTableDB(t)
	tb := db.Table("sessions")
	const createdAt = "2026-01-01T00:00:00.000000000Z"
	if err := tb.Insert("s1", Record{"session_id": "s1", "parent_id": "p1", "created_at": createdAt}); err != nil {
		t.Fatalf("insert s1: %v", err)
	}

	// 原地改 parent_id（fn 直接改传入 map 并返回）→ 索引从 "p1" 区段迁到顶层 "" 区段
	if err := tb.UpdateIn("s1", func(rec Record) Record {
		rec["parent_id"] = ""
		return rec
	}); err != nil {
		t.Fatalf("updatein s1: %v", err)
	}

	top, _, err := tb.Query(Query{Where: Record{"parent_id": ""}, OrderBy: "created_at"})
	if err != nil {
		t.Fatalf("query top: %v", err)
	}
	if len(top) != 1 || top[0][KeyField] != "s1" {
		t.Fatalf("顶层索引应命中 s1：%v", top)
	}
	old, _, err := tb.Query(Query{Where: Record{"parent_id": "p1"}})
	if err != nil {
		t.Fatalf("query p1: %v", err)
	}
	if len(old) != 0 {
		t.Fatalf("旧索引键未清理，仍命中 %v", old)
	}
}

// TestForEachScanOrder（A-20）：单事务按主键字节序遍历全部行；fn 错误中止并透传。
func TestForEachScanOrder(t *testing.T) {
	db := openTestTableDB(t)
	tb := db.Table("things")
	for _, k := range []string{"b", "a", "c"} {
		if err := tb.Upsert(k, Record{"v": k}); err != nil {
			t.Fatalf("upsert %s: %v", k, err)
		}
	}
	var got []string
	if err := tb.ForEach(func(k string, rec Record) error {
		if v, _ := rec["v"].(string); v != k {
			t.Fatalf("行值不符：key=%s rec=%v", k, rec)
		}
		got = append(got, k)
		return nil
	}); err != nil {
		t.Fatalf("foreach: %v", err)
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("应按主键字节序产出：%v", got)
	}

	stop := errors.New("stop")
	if err := tb.ForEach(func(string, Record) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("fn 错误应中止并透传：%v", err)
	}
}

// TestReplaceAllClearAndWrite（A-22）：整体替换 = 旧行全清 + 新行落库（单事务）。
func TestReplaceAllClearAndWrite(t *testing.T) {
	db := openTestTableDB(t)
	tb := db.Table("things")
	if err := tb.Upsert("old1", Record{"v": 1}); err != nil {
		t.Fatalf("upsert old1: %v", err)
	}
	if err := tb.Upsert("old2", Record{"v": 2}); err != nil {
		t.Fatalf("upsert old2: %v", err)
	}

	if err := tb.ReplaceAll(map[string]Record{
		"n1": {"v": "10"},
		"n2": {"v": "20"},
	}); err != nil {
		t.Fatalf("replaceall: %v", err)
	}
	var keys []string
	if err := tb.ForEach(func(k string, rec Record) error {
		v, _ := rec["v"].(string)
		keys = append(keys, k+":"+v)
		return nil
	}); err != nil {
		t.Fatalf("foreach: %v", err)
	}
	if strings.Join(keys, ",") != "n1:10,n2:20" {
		t.Fatalf("整体替换结果不符：%v", keys)
	}

	// 空集 → 清空
	if err := tb.ReplaceAll(map[string]Record{}); err != nil {
		t.Fatalf("replaceall empty: %v", err)
	}
	if keys, err := tb.ListKeys(); err != nil || len(keys) != 0 {
		t.Fatalf("空集替换后应无行：keys=%v err=%v", keys, err)
	}
}
