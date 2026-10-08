// 声明式二级索引（Phase 1「按查询模式建索引」地基，对齐 12-数据层）。
//
// 设计要点：
//   - 表名 → 索引集（IndexesFor）：未知表 → nil（写入口零额外开销）；
//   - 索引键 = 前缀列值 +（可选）排序字段值 + 主键，各段以 \x00 分隔；
//     前缀恒以分隔符结尾，故 Cursor.Seek(前缀) 落在该前缀区段起点；
//   - 索引值 = 主键（省空间）：取值时按主键回表 Get 主行，回表后再应用剩余 Where；
//   - 一致性内建：索引由 Table 写入口在**同一 bolt 事务内**维护（见 table.go），
//     不散在各 facade；索引桶名与建桶清单见 migrate.go。
package data

import (
	"encoding/json"
	"fmt"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// indexSep 是索引键内各段的分隔符（\x00；会话/节点/轮次 id 与 RFC3339 时间均不含该字节）。
const indexSep = "\x00"

// Index 声明一个二级索引桶。
type Index struct {
	Bucket string   // 索引桶名（同库内）
	Fields []string // 前缀列（按序，等值可命中）
	// Order 键内承载的排序字段（可空）。**仅限字典序安全字段**（RFC3339 时间/定长文本）：
	// query.go 的 skipSort 以键字节序当作 compareValue 语义序（A-06），数值字段会静默错序，
	// 故不得把数值列填入 Order（需数值序时应回退内存排序，不设 Order）。
	Order string
	// Build 由记录值生成完整索引键；ok=false 则该行不入此索引。
	// 写入口会把主键注入 rec[KeyField] 后再调用（键尾 = 主键）。
	Build func(rec Record) (indexKey string, ok bool)
}

// keyBuilder 生成索引键构造器：前缀列值 + 可选排序字段值 + 主键，段间 \x00 分隔。
// 字段缺失/空值按空串编码（不跳过该行）——保证 Query 回表后应用 matchWhere 的行集与全表扫描
// 完全一致：跳过空值会让「以空值等值过滤」（如顶层会话 parent_id=""）漏行，故一律入索引。
func keyBuilder(fields []string, order string) func(Record) (string, bool) {
	return func(rec Record) (string, bool) {
		var sb strings.Builder
		for _, f := range fields {
			sb.WriteString(indexFieldStr(rec, f))
			sb.WriteString(indexSep)
		}
		if order != "" {
			sb.WriteString(indexFieldStr(rec, order))
			sb.WriteString(indexSep)
		}
		sb.WriteString(indexFieldStr(rec, KeyField)) // 键尾 = 主键（唯一性保证）
		return sb.String(), true
	}
}

// indexFieldStr 取记录顶层列的字符串值（缺失/nil → ""；数字/布尔归一为文本）。
func indexFieldStr(rec Record, name string) string {
	v, ok := rec[name]
	if !ok || v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	case json.Number:
		return s.String()
	default:
		return fmt.Sprint(s)
	}
}

// indexPrefix 由前缀列值生成查询前缀（恒以分隔符结尾），供 Cursor.Seek。
func indexPrefix(vals []string) string {
	var sb strings.Builder
	for _, v := range vals {
		sb.WriteString(v)
		sb.WriteString(indexSep)
	}
	return sb.String()
}

// 各表索引集（包级预置，避免每次写入重复构造闭包）。
var (
	indexesMessages = []Index{
		{Bucket: "messages_by_session", Fields: []string{"session_id"}, Order: "created_at",
			Build: keyBuilder([]string{"session_id"}, "created_at")},
		{Bucket: "messages_by_turn", Fields: []string{"turn_id"}, Order: "created_at",
			Build: keyBuilder([]string{"turn_id"}, "created_at")},
	}
	indexesTurns = []Index{
		{Bucket: "turns_by_session", Fields: []string{"session_id"}, Order: "created_at",
			Build: keyBuilder([]string{"session_id"}, "created_at")},
	}
	indexesSessions = []Index{
		// 顶层会话 parent_id 缺失/空 → 前缀 "\x00" 命中。
		{Bucket: "sessions_by_top", Fields: []string{"parent_id"}, Order: "created_at",
			Build: keyBuilder([]string{"parent_id"}, "created_at")},
	}
	// 任务树（权威表 tasktree 与影子表 task_shadow 同构；列名为 parent_node_id）。
	indexesTasktree = tasktreeIndexes("tasktree")
	indexesShadow   = tasktreeIndexes("task_shadow")
)

// tasktreeIndexes 生成任务树/影子表的索引集（桶名以表名为前缀）。
func tasktreeIndexes(table string) []Index {
	return []Index{
		{Bucket: table + "_by_top", Fields: []string{"top_session"}, Order: "created_at",
			Build: keyBuilder([]string{"top_session"}, "created_at")},
		{Bucket: table + "_by_session", Fields: []string{"session_id"}, Order: "created_at",
			Build: keyBuilder([]string{"session_id"}, "created_at")},
		{Bucket: table + "_by_parent", Fields: []string{"parent_node_id"}, Order: "",
			Build: keyBuilder([]string{"parent_node_id"}, "")},
	}
}

// IndexesFor 返回表名对应的索引集（未知表 → nil）。
func IndexesFor(table string) []Index {
	switch table {
	case "messages":
		return indexesMessages
	case "turns":
		return indexesTurns
	case "sessions":
		return indexesSessions
	case "tasktree":
		return indexesTasktree
	case "task_shadow":
		return indexesShadow
	}
	return nil
}

// ─── 写入口维护（由 table.go 在同一事务内调用）────────────────────

// buildIndexKey 计算记录在某索引下的键（主键 pk 注入 rec[KeyField]，用后移除）。
func buildIndexKey(idx Index, pk string, rec Record) (string, bool) {
	if idx.Build == nil || rec == nil {
		return "", false
	}
	rec[KeyField] = pk
	key, ok := idx.Build(rec)
	delete(rec, KeyField)
	return key, ok
}

// indexPut 事务内写入记录的全部索引项（value = 主键）。
func indexPut(tx *bolt.Tx, table, pk string, rec Record) error {
	for _, idx := range IndexesFor(table) {
		ik, ok := buildIndexKey(idx, pk, rec)
		if !ok {
			continue
		}
		b, err := tx.CreateBucketIfNotExists([]byte(idx.Bucket))
		if err != nil {
			return err
		}
		if err := b.Put([]byte(ik), []byte(pk)); err != nil {
			return err
		}
	}
	return nil
}

// indexDelete 事务内删除记录的全部索引项（索引桶不存在 → 跳过）。
func indexDelete(tx *bolt.Tx, table, pk string, rec Record) error {
	for _, idx := range IndexesFor(table) {
		ik, ok := buildIndexKey(idx, pk, rec)
		if !ok {
			continue
		}
		if b := tx.Bucket([]byte(idx.Bucket)); b != nil {
			if err := b.Delete([]byte(ik)); err != nil {
				return err
			}
		}
	}
	return nil
}

// decodeRecord 解析桶值为主行记录（带主键）；空值/坏行 → ok=false（调用方跳过）。
func decodeRecord(raw []byte, key string) (Record, bool) {
	if raw == nil {
		return nil, false
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, false
	}
	if rec == nil {
		rec = Record{}
	}
	rec[KeyField] = key
	return rec, true
}

// ─── 查询路由（由 query.go 调用）──────────────────────────────

// indexFor 选路由索引：返回最长可用等值前缀（列序连续、值均为字符串）对应的索引与前缀值。
// 无可用前缀 → (nil, nil)，Query 走全桶扫描。
func indexFor(table string, where map[string]any) (*Index, []string) {
	if len(where) == 0 {
		return nil, nil
	}
	idxes := IndexesFor(table)
	var (
		bestIdx  *Index
		bestVals []string
	)
	for i := range idxes {
		vals, ok := matchIndexFields(idxes[i].Fields, where)
		if !ok {
			continue
		}
		if bestIdx == nil || len(vals) > len(bestVals) {
			bestIdx, bestVals = &idxes[i], vals
		}
	}
	return bestIdx, bestVals
}

// matchIndexFields 取 where 覆盖的、从首列连续的等值前缀；值非字符串 → 不命中（回退全扫）。
func matchIndexFields(fields []string, where map[string]any) ([]string, bool) {
	vals := make([]string, 0, len(fields))
	for _, f := range fields {
		v, ok := where[f]
		if !ok {
			break
		}
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		vals = append(vals, s)
	}
	if len(vals) == 0 {
		return nil, false
	}
	return vals, true
}
