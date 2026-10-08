// 通用 Query（对齐 12-数据层）：
// Where 等值过滤（点路径）→ OrderBy 排序 → Offset/Limit 分页 或 Cursor 连续滚动。
package data

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// Query 是通用查询条件（类 Criteria）。
type Query struct {
	Where     map[string]any // 等值过滤（支持点路径）
	OrderBy   string         // 排序字段（点路径；唯一性由排序键+主键复合保证）
	OrderDesc bool
	Limit     int    // 0 = 不限
	Offset    int    // 普通分页偏移（与 Cursor 互斥）
	Cursor    string // 连续滚动游标（上页 next_cursor；优先于 Offset）
}

// Query 执行通用查询：过滤/排序/分页/游标 → (recs, next_cursor, err)。
// 返回的记录带 _key 主键字段。
//
// 索引路由（Phase 1）：**仅在指定 OrderBy** 且 Where 覆盖某索引 Fields 的某个前缀
// （全部等值字符串）时启用——取**最长可用前缀** Seek 索引桶顺序遍历，每个索引值（= 主键）
// 回表 Get 主行，再应用剩余 Where；否则回退全桶扫描。
// 无 OrderBy 时**一律回退全桶扫描**（A-05）：索引遍历序 =（索引 Order 字段,主键），而游标/
// 断页（filterAfter）按（排序值,主键）定位，二者口径不同 → Offset/Cursor 翻页两页排序口径
// 不一致会漏/重行；统一走桶序即两页同口径（含原 Cursor 单独回退场景）。
// 顺序一致（OrderBy 命中索引 Order 的升序）时跳过内存排序，并允许攒够 Offset+Limit 后提前退出；
// 无 OrderBy（桶序）或需排序时，收集完再排序分页。
// 返回语义（_key、next_cursor 编码、Offset/Limit/Cursor、坏行跳过）与全桶扫描逐字节一致。
//
// 索引就绪防线（Phase 3a）：二级索引桶只在**建桶之后由写入口写入**，故命中索引前先探测
// 索引桶是否整桶为空；为空则视为「索引未就绪」，回落全表扫描（见函数内注释）。
func (t *Table) Query(q Query) ([]Record, string, error) {
	idx, prefixVals := indexFor(t.name, q.Where)
	// 无 OrderBy → 禁用索引路由，一律回退全桶扫描（A-05）：索引遍历序（索引 Order 字段,主键）
	// ≠ 桶序，与游标续查/断页 filterAfter 的 (排序值,主键) 口径不符，两页排序口径不一致会漏/重行；
	// 禁用后与全表扫描同口径（涵盖原「Cursor 且无 OrderBy」的回退场景，语义不回归）。
	if q.OrderBy == "" {
		idx = nil
	}

	var (
		all         []Record
		more        bool // 提前退出时是否仍有后续匹配行（决定 next_cursor）
		skipSort    bool // 遍历顺序即结果顺序 → 跳过内存排序
		earlyTarget int  // >0 时攒够 Offset+Limit 行即可提前退出
	)
	err := t.db.b.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(t.name))
		if b == nil {
			return nil
		}
		// ── 索引就绪探测（Phase 3a 防线）──────────────────────────────
		// 二级索引桶只在**建桶之后由写入口在同一事务内写入**；若某库主表已有数据而索引桶
		// 整桶为空（典型：升级前建立、升级后尚无新写入的开发库），走索引会命中空前缀、
		// **静默返回空**——表象是「数据凭空消失」而非「查询变慢」，属误判风险。故命中索引后
		// 先做一次 O(1) 探测（Cursor().First()）：**整桶无任何键（或桶缺失）→ 视为索引未就绪，
		// 回落为原有全表扫描路径**，结果与引入索引前逐字节一致；索引非空 → 正常走索引。
		// 代价：仅命中索引的查询每查询多一次 O(1) 探测；仅在索引整桶为空时才触发回落。
		var ib *bolt.Bucket
		if idx != nil {
			ib = tx.Bucket([]byte(idx.Bucket))
			var first []byte
			if ib != nil {
				first, _ = ib.Cursor().First()
			}
			if ib == nil || first == nil {
				idx, ib, prefixVals = nil, nil, nil // 索引未就绪 → 回落全表扫描
			}
		}

		// 顺序一致 → 遍历顺序即结果顺序，跳过内存排序。
		// 注意：须在探测**之后**判定——一旦回落全表扫描，遍历序 = 桶序（≠ 排序字段序），
		// 不能沿用「命中索引」时的顺序假设。
		// A-06：此处以索引 Order 字段的**字节序**当作 compareValue 的**语义序**，仅对
		// **字典序安全字段**（RFC3339/定长文本）成立；数值字段会在此静默错序。
		// 故索引的 Order 列仅允许字典序安全字段（约束见 indexes.go Index.Order）。
		skipSort = q.OrderBy == "" || (idx != nil && q.OrderBy == idx.Order && !q.OrderDesc)
		// 提前退出：仅当按所需顺序流式产出且有界（Limit>0 且无游标）。
		if skipSort && q.Cursor == "" && q.Limit > 0 {
			earlyTarget = q.Offset + q.Limit
			if earlyTarget < 0 {
				earlyTarget = 0
			}
		}

		var (
			c     *bolt.Cursor
			start []byte
			main  = b
		)
		if idx != nil {
			c = ib.Cursor()
			start = []byte(indexPrefix(prefixVals))
		} else {
			c = b.Cursor()
		}
		var k, v []byte
		if idx != nil {
			k, v = c.Seek(start)
		} else {
			k, v = c.First()
		}
		for ; k != nil; k, v = c.Next() {
			if idx != nil && !bytes.HasPrefix(k, start) {
				break // 离开前缀区段 → 结束
			}
			var (
				rec Record
				pk  string
			)
			if idx != nil {
				pk = string(v) // 索引值 = 主键
				raw := main.Get([]byte(pk))
				if raw == nil {
					continue // 脏索引（主行缺失）→ 跳过
				}
				if err := json.Unmarshal(raw, &rec); err != nil {
					continue // 跳过坏行
				}
			} else {
				pk = string(k)
				if err := json.Unmarshal(v, &rec); err != nil {
					continue // 跳过坏行
				}
			}
			if rec == nil {
				rec = Record{} // 记录值为 JSON null 时防御 rec[KeyField]=pk panic
			}
			rec[KeyField] = pk
			if len(q.Where) > 0 && !matchWhere(rec, q.Where) {
				continue
			}
			if earlyTarget > 0 && len(all) >= earlyTarget {
				more = true // 已攒够本页，仍有后续匹配行
				break
			}
			all = append(all, rec)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}

	// 排序（升序；OrderDesc 反向）——顺序一致（skipSort）时跳过。
	if !skipSort && q.OrderBy != "" {
		sort.SliceStable(all, func(i, j int) bool {
			return compare(all[i], all[j], q.OrderBy, q.OrderDesc)
		})
	}

	// 游标续查（优先于 Offset）
	if q.Cursor != "" {
		lastVal, lastKey, ok := decodeCursor(q.Cursor)
		if !ok {
			return nil, "", fmt.Errorf("data: bad cursor")
		}
		all = filterAfter(all, lastVal, lastKey, q.OrderBy, q.OrderDesc)
	}

	start := q.Offset
	if start < 0 {
		start = 0
	}
	end := len(all)
	if q.Limit > 0 && start+q.Limit < end {
		end = start + q.Limit
	}
	if start > end {
		start = end
	}
	page := all[start:end]

	// next_cursor：还有后续（含提前退出时窥见的 more）→ 编码最后一行的 (orderByValue, 主键)
	var next string
	if (end < len(all) || more) && len(page) > 0 {
		last := page[len(page)-1]
		next = encodeCursor(fieldString(last, q.OrderBy), keyString(last))
	}
	return page, next, nil
}

// ─── 过滤（Where 点路径等值）────────────────────────────

func matchWhere(rec Record, where map[string]any) bool {
	for k, want := range where {
		got, ok := lookup(rec, k)
		if !ok {
			return false
		}
		if !equalValue(got, want) {
			return false
		}
	}
	return true
}

// lookup 按点路径取值（a.b.c）。
// 顶层 rec 是命名类型 Record，需先显式转回 map[string]any 才能断言；
// 嵌套层均为 json 解码的普通 map[string]any。
func lookup(rec Record, path string) (any, bool) {
	cur := any(map[string]any(rec))
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// equalValue JSON 数值容错比较（json 解码后数字为 float64）。
func equalValue(a, b any) bool {
	if af, ok := toFloat(a); ok {
		if bf, ok2 := toFloat(b); ok2 {
			return af == bf
		}
	}
	return reflect.DeepEqual(a, b)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// ─── 排序 ───────────────────────────────────────────────

func compare(a, b Record, field string, desc bool) bool {
	av := fieldValue(a, field)
	bv := fieldValue(b, field)
	less := compareValue(av, bv)
	if desc {
		return !less && !equalValue(av, bv)
	}
	return less
}

func fieldValue(rec Record, field string) any {
	if v, ok := lookup(rec, field); ok {
		return v
	}
	return nil
}

func fieldString(rec Record, field string) string {
	v := fieldValue(rec, field)
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprintf("%v", x)
	}
}

func keyString(rec Record) string {
	if k, ok := rec[KeyField].(string); ok {
		return k
	}
	return ""
}

// compareValue 数值优先，其次字符串（时间 RFC3339 字符串可直接字典序比较）。
//
// 全序口径（A-13）：两侧皆数值 → 按数值序；数值恒 < 非数值；两侧皆非数值 → 字符串字典序。
// 「非数值 vs 数值」的分支必须与「数值 vs 非数值」对称（返回 false = 非数值 > 数值），
// 否则混类型下不满足严格弱序（如 1<"1" 与 "1"<1 同时为真）→ 排序结果不稳定。
func compareValue(a, b any) bool {
	if af, ok := toFloat(a); ok {
		if bf, ok2 := toFloat(b); ok2 {
			return af < bf
		}
		return true // 数值 < 非数值
	}
	if _, ok := toFloat(b); ok {
		return false // 非数值 > 数值（与上一分支对称，保证全序一致）
	}
	as, bs := fmt.Sprintf("%v", a), fmt.Sprintf("%v", b)
	return as < bs
}

// ─── 游标（§5.2.1 连续滚动）────────────────────────────

// encodeCursor：<orderByValue>|<primaryKey> URL-safe base64。
func encodeCursor(orderVal, pk string) string {
	raw := orderVal + "|" + pk
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (orderVal, pk string, ok bool) {
	b, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// filterAfter 保留游标之后的记录：
//
//	升序：(orderVal > last) OR (orderVal == last AND key > lastKey)
//	降序：反向
//
// 比较口径与 compareValue **全序一致**（A-14）：两侧皆数值才按数值序；数值恒 < 非数值；
// 两侧皆非数值按字符串字典序。避免「一侧数值一侧字符串」时断页口径与排序口径不一致
// （如 "10" 与 8 的字典序错位）导致翻页漏/重行。
func filterAfter(all []Record, lastVal, lastKey, field string, desc bool) []Record {
	lastF, lastErr := strconv.ParseFloat(lastVal, 64)
	lastIsNum := lastErr == nil
	out := make([]Record, 0, len(all))
	for _, rec := range all {
		key := keyString(rec)
		cur := fieldValue(rec, field)
		cf, curIsNum := toFloat(cur)
		// cmp：当前行相对游标行的全序位置（-1 前 / 0 同 / +1 后），口径对齐 compareValue。
		var cmp int
		switch {
		case curIsNum && lastIsNum:
			switch {
			case cf > lastF:
				cmp = 1
			case cf < lastF:
				cmp = -1
			}
		case curIsNum: // 当前数值、游标非数值 → 数值 < 非数值 → 当前在前
			cmp = -1
		case lastIsNum: // 当前非数值、游标数值 → 当前在后
			cmp = 1
		default: // 两侧非数值 → 字符串字典序
			cs := fieldString(rec, field)
			switch {
			case cs > lastVal:
				cmp = 1
			case cs < lastVal:
				cmp = -1
			}
		}
		var after bool
		if desc {
			after = cmp < 0 || (cmp == 0 && key > lastKey)
		} else {
			after = cmp > 0 || (cmp == 0 && key > lastKey)
		}
		if after {
			out = append(out, rec)
		}
	}
	return out
}
