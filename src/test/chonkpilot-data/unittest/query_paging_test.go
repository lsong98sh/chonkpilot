// Query 分页缺陷回归（S-4 剩余两条）：
//
//	① more 精确性：末页**恰好满**且后面无匹配行 → next_cursor 为空（原实现无条件置 more=true）；
//	② 带 Cursor 的提前退出：skipSort（遍历序 == 结果序）成立时，每页与「无 Cursor + Offset」
//	   逐条一致，同值 tie 组跨页不漏不重；
//	③ 尾部整段被 Where 过滤（> earlyProbeMax 个桶键）→ 保守 more=true（不丢行、不退化成全扫描）；
//	④ 回归：无 Limit 不返回 next_cursor；skipSort=false（需内存排序）分页结果不变。
package data_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
)

// seedTS 生成第 i 行的 created_at（perGroup 行同值 → 构造 tie 组）。
func seedTS(perGroup int) func(i int) string {
	return func(i int) string {
		return fmt.Sprintf("2026-01-%02dT00:00:00.000000000Z", i/perGroup+1)
	}
}

// seedMessagesWith 在 messages 表（含 messages_by_session 索引）按 keys 顺序写 s1 消息。
func seedMessagesWith(t *testing.T, db *data.DB, keys []string, ts func(i int) string) {
	t.Helper()
	tb := db.Table("messages")
	for i, k := range keys {
		if err := tb.Upsert(k, data.Record{"session_id": "s1", "created_at": ts(i)}); err != nil {
			t.Fatalf("upsert %s: %v", k, err)
		}
	}
}

func msgKeys(n int) []string {
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprintf("M%02d", i))
	}
	return out
}

// TestQueryLastPageExactlyFullNoMore：缺陷 1 回归点 ——
// 末页恰好满（Limit 用尽）且其后**无**匹配行 → next_cursor 必须为空（不再假定还有后续）。
func TestQueryLastPageExactlyFullNoMore(t *testing.T) {
	db := openLayer(t, data.LayerPrjUsr)
	keys := msgKeys(4) // 4 条 + 页大小 2 → 第 2 页恰好满且无后续
	seedMessagesWith(t, db, keys, seedTS(1))
	where := map[string]any{"session_id": "s1"}

	page1, next1, err := db.Table("messages").Query(data.Query{Where: where, OrderBy: "created_at", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !equalSeq(recKeys(page1), keys[0:2]) || next1 == "" {
		t.Fatalf("第 1 页=%v next=%q（应满页且有 next_cursor）", recKeys(page1), next1)
	}
	page2, next2, err := db.Table("messages").Query(data.Query{Where: where, OrderBy: "created_at", Limit: 2, Cursor: next1})
	if err != nil {
		t.Fatal(err)
	}
	if !equalSeq(recKeys(page2), keys[2:4]) {
		t.Fatalf("第 2 页=%v want %v", recKeys(page2), keys[2:4])
	}
	if next2 != "" {
		t.Fatalf("末页恰好满且后面无匹配行 → next_cursor 必须为空（缺陷 1），got %q", next2)
	}

	// 桶序路径（无 OrderBy / 无索引）同口径
	items := db.Table("items")
	for i := 1; i <= 4; i++ {
		if err := items.Insert(fmt.Sprintf("k%d", i), data.Record{"cat": "a"}); err != nil {
			t.Fatal(err)
		}
	}
	p1, n1, _ := items.Query(data.Query{Limit: 2})
	p2, n2, _ := items.Query(data.Query{Limit: 2, Cursor: n1})
	if !equalSeq(recKeys(p1), []string{"k1", "k2"}) || !equalSeq(recKeys(p2), []string{"k3", "k4"}) {
		t.Fatalf("桶序分页 p1=%v p2=%v", recKeys(p1), recKeys(p2))
	}
	if n2 != "" {
		t.Fatalf("桶序末页恰好满 → next_cursor 必须为空，got %q", n2)
	}
}

// TestQueryLastPageExactlyFullWithMore：末页恰好满但后面**仍有一条**匹配行 → next_cursor 非空。
func TestQueryLastPageExactlyFullWithMore(t *testing.T) {
	db := openLayer(t, data.LayerPrjUsr)
	keys := msgKeys(5) // 5 条 + 页大小 2 → 第 2 页恰好满，第 5 条仍在后面
	seedMessagesWith(t, db, keys, seedTS(1))
	where := map[string]any{"session_id": "s1"}

	p1, n1, err := db.Table("messages").Query(data.Query{Where: where, OrderBy: "created_at", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	p2, n2, err := db.Table("messages").Query(data.Query{Where: where, OrderBy: "created_at", Limit: 2, Cursor: n1})
	if err != nil {
		t.Fatal(err)
	}
	if !equalSeq(recKeys(p1), keys[0:2]) || !equalSeq(recKeys(p2), keys[2:4]) {
		t.Fatalf("p1=%v p2=%v", recKeys(p1), recKeys(p2))
	}
	if n2 == "" {
		t.Fatal("末页恰好满但后面仍有匹配行 → next_cursor 必须非空")
	}
	p3, n3, err := db.Table("messages").Query(data.Query{Where: where, OrderBy: "created_at", Limit: 2, Cursor: n2})
	if err != nil {
		t.Fatal(err)
	}
	if !equalSeq(recKeys(p3), keys[4:5]) || n3 != "" {
		t.Fatalf("第 3 页=%v next=%q（应为末页且无 next_cursor）", recKeys(p3), n3)
	}
}

// TestQueryWhereFilteredTailConservativeMore：配额正好在最后一条匹配行填满、其后 >512 个桶键
// 全被 Where 过滤 —— 不得为判定 more 一路扫到表尾；超护栏后**保守**置 more=true
// （代价至多是一次空页，绝不丢行），查询本身不超时且本页结果正确。
func TestQueryWhereFilteredTailConservativeMore(t *testing.T) {
	db := openLayer(t, data.LayerPrjUsr)
	items := db.Table("items")
	// 2 条匹配（配额恰在此填满）
	for i := 1; i <= 2; i++ {
		if err := items.Insert(fmt.Sprintf("k%04d", i), data.Record{"cat": "a"}); err != nil {
			t.Fatal(err)
		}
	}
	// 600 条不匹配（> earlyProbeMax=512），桶序排在匹配行之后
	for i := 100; i < 700; i++ {
		if err := items.Insert(fmt.Sprintf("k%04d", i), data.Record{"cat": "b"}); err != nil {
			t.Fatal(err)
		}
	}

	start := time.Now()
	page, next, err := items.Query(data.Query{Where: map[string]any{"cat": "a"}, Limit: 2})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if !equalSeq(recKeys(page), []string{"k0001", "k0002"}) {
		t.Fatalf("本页=%v want [k0001 k0002]", recKeys(page))
	}
	if next == "" {
		t.Fatal("超护栏应保守置 more=true（否则即一路扫到表尾）→ next_cursor 非空")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("尾部过滤保护失效：单页查询耗时 %v", elapsed)
	}
	// 保守 next 的代价：多一次空页；不丢行（前面 2 条已全部返回）
	p2, n2, err := items.Query(data.Query{Where: map[string]any{"cat": "a"}, Limit: 2, Cursor: next})
	if err != nil {
		t.Fatal(err)
	}
	if len(p2) != 0 || n2 != "" {
		t.Fatalf("乐观续页应为空页且无 next_cursor，got %v next=%q", recKeys(p2), n2)
	}
}

// TestQueryCursorPagingMatchesOffset：带 Cursor 的提前退出（索引路径，skipSort 成立）——
// 每页与「无 Cursor + Offset」逐条一致；created_at 每 3 条同值，页大小 2 → 页边界必然落在
// tie 组内部，用于证明同值 tie 组跨页不漏不重（游标含主键，可按主键切分 tie 组）。
func TestQueryCursorPagingMatchesOffset(t *testing.T) {
	db := openLayer(t, data.LayerPrjUsr)
	keys := msgKeys(12)
	seedMessagesWith(t, db, keys, seedTS(3)) // 12 条 / 每组 3 条同 created_at → 4 个 tie 组
	where := map[string]any{"session_id": "s1"}
	tb := db.Table("messages")

	ref, refNext, err := tb.Query(data.Query{Where: where, OrderBy: "created_at"}) // 无 Limit：全量、遍历序
	if err != nil {
		t.Fatal(err)
	}
	if refNext != "" {
		t.Fatalf("无 Limit 不应给出 next_cursor，got %q", refNext)
	}
	refKeys := recKeys(ref)
	if len(refKeys) != len(keys) {
		t.Fatalf("参考全量=%d want %d: %v", len(refKeys), len(keys), refKeys)
	}

	// 游标翻页：逐页与「Offset + Limit」切片比对（页大小 2，必然切在 tie 组内部）
	cursor, pageNo := "", 0
	for {
		want := refKeys[pageNo*2 : min((pageNo+1)*2, len(refKeys))]
		page, next, err := tb.Query(data.Query{Where: where, OrderBy: "created_at", Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("cursor page %d: %v", pageNo, err)
		}
		if !equalSeq(recKeys(page), want) {
			t.Fatalf("游标第 %d 页=%v want %v（tie 组被截断或漏/重）", pageNo, recKeys(page), want)
		}
		offPage, _, err := tb.Query(data.Query{Where: where, OrderBy: "created_at", Offset: pageNo * 2, Limit: 2})
		if err != nil {
			t.Fatalf("offset page %d: %v", pageNo, err)
		}
		if !equalSeq(recKeys(page), recKeys(offPage)) {
			t.Fatalf("第 %d 页：游标=%v 与 Offset=%v 不一致", pageNo, recKeys(page), recKeys(offPage))
		}
		if next == "" {
			break
		}
		cursor, pageNo = next, pageNo+1
		if pageNo > len(refKeys) {
			t.Fatalf("游标翻页未收敛（可能是 next_cursor 恒非空）")
		}
	}
	if pageNo != len(refKeys)/2-1 {
		t.Fatalf("页数=%d want %d", pageNo+1, len(refKeys)/2)
	}

	// 桶序路径（无 OrderBy / 无索引）同样允许带游标提前退出：与 Offset 分页一致
	items := db.Table("items")
	var itemKeys []string
	for i := 1; i <= 7; i++ {
		k := fmt.Sprintf("k%d", i)
		itemKeys = append(itemKeys, k)
		if err := items.Insert(k, data.Record{"cat": "a"}); err != nil {
			t.Fatal(err)
		}
	}
	cursor = ""
	var walked []string
	for {
		page, next, err := items.Query(data.Query{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		walked = append(walked, recKeys(page)...)
		if next == "" {
			break
		}
		cursor = next
	}
	if !equalSeq(walked, itemKeys) {
		t.Fatalf("桶序游标翻页=%v want %v", walked, itemKeys)
	}
	for off := 0; off < len(itemKeys); off += 2 {
		want := itemKeys[off:min(off+2, len(itemKeys))]
		got, _, err := items.Query(data.Query{Offset: off, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if !equalSeq(recKeys(got), want) {
			t.Fatalf("桶序 Offset=%d got=%v want %v", off, recKeys(got), want)
		}
	}
}

// TestQueryDescSortPagingUnchanged：skipSort=false（OrderDesc 需内存排序）—— 分页与游标结果
// 保持不变（每页 = 降序全序的对应切片），确认缺陷 2 的改动不影响内存排序路径。
func TestQueryDescSortPagingUnchanged(t *testing.T) {
	db := openLayer(t, data.LayerPrjUsr)
	keys := msgKeys(6)
	seedMessagesWith(t, db, keys, seedTS(2)) // 3 个 tie 组
	where := map[string]any{"session_id": "s1"}
	tb := db.Table("messages")

	ref, _, err := tb.Query(data.Query{Where: where, OrderBy: "created_at", OrderDesc: true})
	if err != nil {
		t.Fatal(err)
	}
	refKeys := recKeys(ref)
	if len(refKeys) != len(keys) {
		t.Fatalf("降序全量=%v", refKeys)
	}

	// 无 Limit → 不给 next_cursor（下游据此判定「已到末页」）
	if _, next, err := tb.Query(data.Query{Where: where, OrderBy: "created_at", OrderDesc: true}); err != nil || next != "" {
		t.Fatalf("无 Limit 的降序查询不应给 next_cursor，next=%q err=%v", next, err)
	}

	// 游标翻页（内存排序路径）与 Offset 切片一致
	cursor, pageNo := "", 0
	for {
		want := refKeys[pageNo*2 : min((pageNo+1)*2, len(refKeys))]
		page, next, err := tb.Query(data.Query{Where: where, OrderBy: "created_at", OrderDesc: true, Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("desc cursor page %d: %v", pageNo, err)
		}
		if !equalSeq(recKeys(page), want) {
			t.Fatalf("降序游标第 %d 页=%v want %v", pageNo, recKeys(page), want)
		}
		offPage, _, err := tb.Query(data.Query{Where: where, OrderBy: "created_at", OrderDesc: true, Offset: pageNo * 2, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if !equalSeq(recKeys(page), recKeys(offPage)) {
			t.Fatalf("降序第 %d 页：游标=%v 与 Offset=%v 不一致", pageNo, recKeys(page), recKeys(offPage))
		}
		if next == "" {
			break
		}
		cursor, pageNo = next, pageNo+1
		if pageNo > len(refKeys) {
			t.Fatal("降序游标翻页未收敛")
		}
	}
}
