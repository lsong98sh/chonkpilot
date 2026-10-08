// data 二级索引地基单测（Phase 1「按查询模式建索引」）：
// ① 索引一致性（写入口维护：Upsert/Update/Insert/Delete 的键迁移与清理）；
// ② Query 索引路由正确性（与全表扫描结果一致：升/降序、Offset/Limit、Cursor、前缀+额外过滤）；
// ③ 顺序与提前退出（命中索引且顺序一致时有序 + Limit 生效 + next_cursor；无索引表行为不变）；
// ④ ListPrefix（前缀命中/空结果/顺序）；
// ⑤ 索引就绪防线（Phase 3a：索引桶整桶为空 → 回落全表扫描，主表数据不凭空消失）。
package data_test

import (
	"errors"
	"fmt"
	"sort"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

// prefixKeys 取某索引桶以 prefix 起始的主键前缀键列表（Seek 顺序）。
func prefixKeys(t *testing.T, db *data.DB, bucket, prefix string) []string {
	t.Helper()
	keys, err := db.Table(bucket).ListPrefix(prefix)
	if err != nil {
		t.Fatalf("ListPrefix(%s, %q): %v", bucket, prefix, err)
	}
	return keys
}

// clearBucket 手工清空某桶全部键（用于模拟「索引未就绪」状态：主表有行、索引桶为空）。
// 索引桶值为主键（非 JSON），Table.Delete 对非 JSON 值走 decodeRecord=false 分支后直接删键，安全。
func clearBucket(t *testing.T, db *data.DB, bucket string) {
	t.Helper()
	keys, err := db.Table(bucket).ListKeys()
	if err != nil {
		t.Fatalf("ListKeys(%s): %v", bucket, err)
	}
	for _, k := range keys {
		if err := db.Table(bucket).Delete(k); err != nil {
			t.Fatalf("Delete(%s, %q): %v", bucket, k, err)
		}
	}
	if left, _ := db.Table(bucket).ListKeys(); len(left) != 0 {
		t.Fatalf("清空后 %s 仍残留 %d 键", bucket, len(left))
	}
}

// recKeys 取记录主键序列。
func recKeys(recs []data.Record) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r[data.KeyField].(string))
	}
	return out
}

func equalSeq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestIndexMaintenance：索引由写入口在同一事务内维护（键迁移 + 清理 + 失败不脏）。
func TestIndexMaintenance(t *testing.T) {
	db := openLayer(t, data.LayerPrjUsr)
	msgs := db.Table("messages")

	if err := msgs.Upsert("M1", data.Record{
		"session_id": "s1", "turn_id": "t1", "content": "a",
		"created_at": "2026-01-01T00:00:00.000000001Z",
	}); err != nil {
		t.Fatal(err)
	}
	if got := prefixKeys(t, db, "messages_by_session", "s1\x00"); !equalSeq(got,
		[]string{"s1\x002026-01-01T00:00:00.000000001Z\x00M1"}) {
		t.Fatalf("messages_by_session=%v", got)
	}
	if got := prefixKeys(t, db, "messages_by_turn", "t1\x00"); len(got) != 1 {
		t.Fatalf("messages_by_turn=%v", got)
	}

	// Update 改 session_id / turn_id / created_at → 旧索引键消失、新键存在（含 created_at 迁移）
	if err := msgs.Update("M1", data.Record{
		"session_id": "s2", "turn_id": "t2", "content": "a",
		"created_at": "2026-02-01T00:00:00.000000000Z",
	}); err != nil {
		t.Fatal(err)
	}
	if got := prefixKeys(t, db, "messages_by_session", "s1\x00"); len(got) != 0 {
		t.Fatalf("旧 session 索引残留: %v", got)
	}
	if got := prefixKeys(t, db, "messages_by_session", "s2\x00"); !equalSeq(got,
		[]string{"s2\x002026-02-01T00:00:00.000000000Z\x00M1"}) {
		t.Fatalf("新 session 索引=%v", got)
	}
	if got := prefixKeys(t, db, "messages_by_turn", "t1\x00"); len(got) != 0 {
		t.Fatalf("旧 turn 索引残留: %v", got)
	}
	if got := prefixKeys(t, db, "messages_by_turn", "t2\x00"); len(got) != 1 {
		t.Fatalf("新 turn 索引=%v", got)
	}

	// Delete → 索引无残留
	if err := msgs.Delete("M1"); err != nil {
		t.Fatal(err)
	}
	if got := prefixKeys(t, db, "messages_by_session", "s2\x00"); len(got) != 0 {
		t.Fatalf("delete 后索引残留: %v", got)
	}
	if got := prefixKeys(t, db, "messages_by_turn", "t2\x00"); len(got) != 0 {
		t.Fatalf("delete 后 turn 索引残留: %v", got)
	}

	// Insert 重复主键报错且索引不脏
	if err := msgs.Insert("M2", data.Record{"session_id": "s3", "created_at": "2026-03-01T00:00:00.000000000Z"}); err != nil {
		t.Fatal(err)
	}
	if err := msgs.Insert("M2", data.Record{"session_id": "sX"}); !errors.Is(err, data.ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
	if got := prefixKeys(t, db, "messages_by_session", "s3\x00"); len(got) != 1 {
		t.Fatalf("s3 索引应为唯一一条: %v", got)
	}
	if got := prefixKeys(t, db, "messages_by_session", "sX\x00"); len(got) != 0 {
		t.Fatalf("失败 Insert 不得写索引: %v", got)
	}

	// Upsert 覆盖改 created_at → 索引键随之迁移
	_ = msgs.Upsert("M3", data.Record{"session_id": "s4", "created_at": "2026-04-01T00:00:00.000000000Z"})
	_ = msgs.Upsert("M3", data.Record{"session_id": "s4", "created_at": "2026-05-01T00:00:00.000000000Z"})
	if got := prefixKeys(t, db, "messages_by_session", "s4\x00"); !equalSeq(got,
		[]string{"s4\x002026-05-01T00:00:00.000000000Z\x00M3"}) {
		t.Fatalf("upsert 迁移后索引=%v", got)
	}

	// 会话顶层索引：parent_id 缺失/空 → 前缀 "\x00" 命中
	sessions := db.Table("sessions")
	_ = sessions.Upsert("top1", data.Record{"session_id": "top1", "created_at": "2026-01-01T00:00:00.000000000Z"})
	_ = sessions.Upsert("child", data.Record{"session_id": "child", "parent_id": "top1", "created_at": "2026-01-02T00:00:00.000000000Z"})
	if got := prefixKeys(t, db, "sessions_by_top", "\x00"); len(got) != 1 {
		t.Fatalf("顶层会话索引（parent_id 缺失→空前缀）=%v", got)
	}
	if got := prefixKeys(t, db, "sessions_by_top", "top1\x00"); len(got) != 1 {
		t.Fatalf("子会话索引=%v", got)
	}
}

// TestQueryIndexRouting：命中索引的查询与全表扫描结果一致（升/降序、Offset/Limit、Cursor、前缀+额外过滤）。
func TestQueryIndexRouting(t *testing.T) {
	db := openLayer(t, data.LayerPrjUsr)
	msgs := db.Table("messages")
	// s1: M1..M5；s2: M6,M7；created_at 各异；turn_id = i 两两一组。
	for i := 1; i <= 7; i++ {
		sess := "s1"
		if i > 5 {
			sess = "s2"
		}
		if err := msgs.Upsert(fmt.Sprintf("M%d", i), data.Record{
			"session_id":   sess,
			"turn_id":      fmt.Sprintf("t%d", (i+1)/2),
			"tool_call_id": fmt.Sprintf("tc%d", i),
			"created_at":   fmt.Sprintf("2026-01-0%dT00:00:00.000000000Z", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 参考结果（无 Where → 全表扫描，不经索引）：按 created_at 排序后按会话过滤。
	refFor := func(sess string, desc bool) []string {
		recs, _, _ := msgs.Query(data.Query{OrderBy: "created_at", OrderDesc: desc})
		var out []string
		for _, r := range recs {
			if r["session_id"] == sess {
				out = append(out, r[data.KeyField].(string))
			}
		}
		return out
	}

	// 升序
	got, next, _ := msgs.Query(data.Query{Where: map[string]any{"session_id": "s1"}, OrderBy: "created_at"})
	if !equalSeq(recKeys(got), refFor("s1", false)) {
		t.Fatalf("asc=%v want %v", recKeys(got), refFor("s1", false))
	}
	if next != "" {
		t.Fatalf("无 Limit 不应给出 next_cursor: %q", next)
	}
	// 降序（created_at 各异，无 tie）
	got, _, _ = msgs.Query(data.Query{Where: map[string]any{"session_id": "s1"}, OrderBy: "created_at", OrderDesc: true})
	if !equalSeq(recKeys(got), refFor("s1", true)) {
		t.Fatalf("desc=%v want %v", recKeys(got), refFor("s1", true))
	}
	// Offset/Limit + next_cursor
	exp := refFor("s1", false)
	got, next, _ = msgs.Query(data.Query{Where: map[string]any{"session_id": "s1"}, OrderBy: "created_at", Offset: 1, Limit: 2})
	if !equalSeq(recKeys(got), exp[1:3]) {
		t.Fatalf("offset/limit=%v want %v", recKeys(got), exp[1:3])
	}
	if next == "" {
		t.Fatal("仍应给出 next_cursor")
	}
	// Cursor 连续滚动第 2 页
	got2, next2, _ := msgs.Query(data.Query{Where: map[string]any{"session_id": "s1"}, OrderBy: "created_at", Limit: 2, Cursor: next})
	if !equalSeq(recKeys(got2), exp[3:5]) {
		t.Fatalf("cursor page2=%v want %v", recKeys(got2), exp[3:5])
	}
	if next2 != "" {
		t.Fatalf("末页不应有 next_cursor: %q", next2)
	}
	// 前缀 + 额外过滤（session_id + tool_call_id）
	got, _, _ = msgs.Query(data.Query{Where: map[string]any{"session_id": "s1", "tool_call_id": "tc3"}, OrderBy: "created_at"})
	if !equalSeq(recKeys(got), []string{exp[2]}) {
		t.Fatalf("session+tool_call=%v want %v", recKeys(got), []string{exp[2]})
	}
	// turn 前缀
	got, _, _ = msgs.Query(data.Query{Where: map[string]any{"turn_id": "t2"}, OrderBy: "created_at"})
	if !equalSeq(recKeys(got), []string{"M3", "M4"}) {
		t.Fatalf("turn=%v", recKeys(got))
	}
	// 无命中
	got, _, _ = msgs.Query(data.Query{Where: map[string]any{"session_id": "nope"}, OrderBy: "created_at"})
	if len(got) != 0 {
		t.Fatalf("no-match=%v", recKeys(got))
	}
}

// TestQueryOrderAndEarlyExit：顺序一致时有序 + Limit 提前退出；无索引表行为与改前一致。
func TestQueryOrderAndEarlyExit(t *testing.T) {
	db := openLayer(t, data.LayerPrjUsr)
	msgs := db.Table("messages")
	for i := 1; i <= 5; i++ {
		if err := msgs.Upsert(fmt.Sprintf("M%d", i), data.Record{
			"session_id": "s1",
			"created_at": fmt.Sprintf("2026-01-0%dT00:00:00.000000000Z", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 命中索引 + 顺序一致 → 有序 + Limit 生效 + 提前退出（next 非空）
	got, next, _ := msgs.Query(data.Query{Where: map[string]any{"session_id": "s1"}, OrderBy: "created_at", Limit: 2})
	if !equalSeq(recKeys(got), []string{"M1", "M2"}) {
		t.Fatalf("limit2=%v", recKeys(got))
	}
	if next == "" {
		t.Fatal("应有 next_cursor（提前退出）")
	}
	// Limit 覆盖全部 → 无 next_cursor
	got, next, _ = msgs.Query(data.Query{Where: map[string]any{"session_id": "s1"}, OrderBy: "created_at", Limit: 10})
	if len(got) != 5 || next != "" {
		t.Fatalf("limit10 got=%v next=%q", recKeys(got), next)
	}
	// 顺序不一致（OrderBy 非索引 Order）→ 内存排序路径仍返回全量
	got, _, _ = msgs.Query(data.Query{Where: map[string]any{"session_id": "s1"}, OrderBy: "content"})
	if len(got) != 5 {
		t.Fatalf("mismatch-order got=%v", recKeys(got))
	}

	// 无索引表（items 未注册索引）：全桶扫描 + 内存排序 + 分页，行为与改前一致
	items := db.Table("items")
	for i := 1; i <= 5; i++ {
		if err := items.Insert(fmt.Sprintf("k%d", i), data.Record{"cat": "a", "ts": i}); err != nil {
			t.Fatal(err)
		}
	}
	got, _, _ = items.Query(data.Query{Where: map[string]any{"cat": "a"}, OrderBy: "ts", OrderDesc: true, Limit: 2})
	if !equalSeq(recKeys(got), []string{"k5", "k4"}) {
		t.Fatalf("no-index=%v", recKeys(got))
	}
	// 无索引 + 无 OrderBy → 桶序（主键升序）+ 提前退出
	got, next, _ = items.Query(data.Query{Where: map[string]any{"cat": "a"}, Limit: 2})
	if !equalSeq(recKeys(got), []string{"k1", "k2"}) {
		t.Fatalf("no-order=%v", recKeys(got))
	}
	if next == "" {
		t.Fatal("无 OrderBy + Limit 应可提前退出（next_cursor 非空）")
	}
}

// TestSessionTopRouting：sessions_by_top 顶层路由（Where{parent_id:""}）与全表扫描 + Go 侧顶层过滤
// 结果一致（Phase 2a 把 SessionList/LatestTopSession 的顶层过滤下推为等值前缀查询）。
func TestSessionTopRouting(t *testing.T) {
	db := openLayer(t, data.LayerPrjUsr)
	sessions := db.Table("sessions")
	// 顶层会话恒写 parent_id=""（SessionEnsure 口径）；子会话 parent_id=父 id。
	_ = sessions.Upsert("top1", data.Record{"session_id": "top1", "parent_id": "", "created_at": "2026-01-01T00:00:00.000000000Z"})
	_ = sessions.Upsert("top2", data.Record{"session_id": "top2", "parent_id": "", "created_at": "2026-02-01T00:00:00.000000000Z"})
	_ = sessions.Upsert("child", data.Record{"session_id": "child", "parent_id": "top1", "created_at": "2026-03-01T00:00:00.000000000Z"})

	// 参考路径：全表扫描（无 Where，不经索引）+ Go 侧顶层过滤（缺键/空串均视为顶层），created_at 降序。
	all, _, _ := sessions.Query(data.Query{OrderBy: "created_at", OrderDesc: true})
	var ref []string
	for _, r := range all {
		if s, _ := r["parent_id"].(string); s == "" {
			ref = append(ref, r[data.KeyField].(string))
		}
	}
	if !equalSeq(ref, []string{"top2", "top1"}) {
		t.Fatalf("参考顶层集合不符: %v", ref)
	}

	// 索引路径：Where{parent_id:""} 命中 sessions_by_top 前缀 "\x00" → 与参考一致（子会话被前缀排除）。
	got, _, _ := sessions.Query(data.Query{
		Where: map[string]any{"parent_id": ""}, OrderBy: "created_at", OrderDesc: true,
	})
	if !equalSeq(recKeys(got), ref) {
		t.Fatalf("顶层路由=%v want %v", recKeys(got), ref)
	}

	// LatestTopSession 口径：降序首个 = 最近创建顶层。
	if k := recKeys(got)[0]; k != "top2" {
		t.Fatalf("最近顶层应为 top2: %v", recKeys(got))
	}
}

// TestListPrefix：主键前缀读（顺序 = 主键字典序；空结果；空前缀 = 全部）。
func TestListPrefix(t *testing.T) {
	db := openLayer(t, data.LayerPrjUsr)
	tb := db.Table("memory_extract")
	_ = tb.Upsert("s1\x00项目概要", data.Record{"session_id": "s1", "category": "项目概要"})
	_ = tb.Upsert("s1\x00用户偏好", data.Record{"session_id": "s1", "category": "用户偏好"})
	_ = tb.Upsert("s2\x00项目概要", data.Record{"session_id": "s2", "category": "项目概要"})

	got, err := tb.ListPrefix("s1\x00")
	if err != nil {
		t.Fatal(err)
	}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("ListPrefix 应为主键字典序: %v", got)
	}
	want := []string{"s1\x00用户偏好", "s1\x00项目概要"}
	if !equalSeq(got, want) {
		t.Fatalf("ListPrefix=%v want %v", got, want)
	}
	// 空结果
	if k, _ := tb.ListPrefix("zzz"); len(k) != 0 {
		t.Fatalf("不匹配前缀应为空: %v", k)
	}
	// 空前缀 = 全部主键
	if k, _ := tb.ListPrefix(""); len(k) != 3 {
		t.Fatalf("空前缀应返回全部主键: %v", k)
	}
}

// TestIndexEmptyFallbackFullScan（Phase 3a 防线）：主表有行但索引桶被清空（模拟升级前的开发库：
// 主表已落数据、索引桶整桶为空）时，命中索引的查询必须**回落全表扫描**、返回全部命中行，
// 结果与索引正常时逐条一致（数据不凭空消失）；分页 / next_cursor / Where 过滤语义不变。
func TestIndexEmptyFallbackFullScan(t *testing.T) {
	db := openLayer(t, data.LayerPrjUsr)
	msgs := db.Table("messages")
	// 3 条 s1 + 1 条 s2（s2 用于验证回落仍应用 Where，而非退化成返回全表）
	for i := 1; i <= 3; i++ {
		if err := msgs.Upsert(fmt.Sprintf("M%d", i), data.Record{
			"session_id": "s1",
			"created_at": fmt.Sprintf("2026-01-0%dT00:00:00.000000000Z", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := msgs.Upsert("MX", data.Record{
		"session_id": "s2", "created_at": "2026-01-04T00:00:00.000000000Z",
	}); err != nil {
		t.Fatal(err)
	}

	// 索引正常（非空）时的参考结果（走索引路径）
	ref, _, _ := msgs.Query(data.Query{Where: map[string]any{"session_id": "s1"}, OrderBy: "created_at"})
	if !equalSeq(recKeys(ref), []string{"M1", "M2", "M3"}) {
		t.Fatalf("索引路径参考=%v", recKeys(ref))
	}

	// 主表有行、索引桶整桶清空 → 「索引未就绪」
	clearBucket(t, db, "messages_by_session")

	// 回落全表扫描：仍返回全部命中行（与索引路径一致），而非空
	got, _, _ := msgs.Query(data.Query{Where: map[string]any{"session_id": "s1"}, OrderBy: "created_at"})
	if !equalSeq(recKeys(got), recKeys(ref)) {
		t.Fatalf("索引空回落结果=%v want %v", recKeys(got), recKeys(ref))
	}
	if len(got) != 3 {
		t.Fatalf("回落不得返回全表或空: %v", recKeys(got))
	}

	// 回落路径下分页 + next_cursor 语义与索引路径一致
	page, next, _ := msgs.Query(data.Query{Where: map[string]any{"session_id": "s1"}, OrderBy: "created_at", Limit: 2})
	if !equalSeq(recKeys(page), []string{"M1", "M2"}) || next == "" {
		t.Fatalf("索引空回落分页=%v next=%q", recKeys(page), next)
	}

	// 另一张带索引的表（turns_by_session）同样被兜住
	turns := db.Table("turns")
	for i := 1; i <= 2; i++ {
		if err := turns.Upsert(fmt.Sprintf("T%d", i), data.Record{
			"session_id": "s1",
			"created_at": fmt.Sprintf("2026-02-0%dT00:00:00.000000000Z", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	wantTurns, _, _ := turns.Query(data.Query{Where: map[string]any{"session_id": "s1"}, OrderBy: "created_at"})
	clearBucket(t, db, "turns_by_session")
	gotTurns, _, _ := turns.Query(data.Query{Where: map[string]any{"session_id": "s1"}, OrderBy: "created_at"})
	if len(wantTurns) != 2 || !equalSeq(recKeys(gotTurns), recKeys(wantTurns)) {
		t.Fatalf("turns 索引空回落=%v want %v", recKeys(gotTurns), recKeys(wantTurns))
	}
}

// TestIndexNonEmptyNoFallback（Phase 3a 防线的反向断言）：索引桶非空时**不**回落，仍走索引。
// 用一条仅索引路径才成立的「tie 保序」间接区分两条路径：
//
//	两行同 session、同 content（OrderBy=content 有 tie），created_at 顺序与主键顺序相反：
//	  索引遍历序（= (created_at, 主键)，skipSort=false → 稳定排序前序）= [M2, M1]
//	  全表扫描序（= 主键桶序）                                  = [M1, M2]
//
// 故 OrderBy=content 的结果保序即可反推走了哪条路径；若误回落，结果将变为 [M1, M2]。
func TestIndexNonEmptyNoFallback(t *testing.T) {
	db := openLayer(t, data.LayerPrjUsr)
	msgs := db.Table("messages")
	// 主键序 M1<M2；created_at 序 M2<M1（相反）
	if err := msgs.Upsert("M2", data.Record{
		"session_id": "s1", "content": "x", "created_at": "2026-01-01T00:00:00.000000000Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := msgs.Upsert("M1", data.Record{
		"session_id": "s1", "content": "x", "created_at": "2026-01-02T00:00:00.000000000Z",
	}); err != nil {
		t.Fatal(err)
	}

	// 索引非空 → 走索引：稳定排序前序 = created_at 序 → tie 保序 [M2, M1]
	got, _, _ := msgs.Query(data.Query{Where: map[string]any{"session_id": "s1"}, OrderBy: "content"})
	if !equalSeq(recKeys(got), []string{"M2", "M1"}) {
		t.Fatalf("索引非空应走索引（tie 保序 [M2 M1]），实际 %v（疑似误回落）", recKeys(got))
	}

	// 反证：清空索引桶 → 回落全表扫描：稳定排序前序 = 主键桶序 → tie 保序 [M1, M2]
	clearBucket(t, db, "messages_by_session")
	got, _, _ = msgs.Query(data.Query{Where: map[string]any{"session_id": "s1"}, OrderBy: "content"})
	if !equalSeq(recKeys(got), []string{"M1", "M2"}) {
		t.Fatalf("索引空应回落（tie 保序 [M1 M2]），实际 %v", recKeys(got))
	}
}
