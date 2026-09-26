// 通用 Query（对齐 12-数据层）：
// Where 等值过滤（点路径）→ OrderBy 排序 → Offset/Limit 分页 或 Cursor 连续滚动。
package data

import (
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
func (t *Table) Query(q Query) ([]Record, string, error) {
	var all []Record
	err := t.db.b.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(t.name))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			rec := Record{}
			if err := json.Unmarshal(v, &rec); err != nil {
				return nil // 跳过坏行
			}
			rec[KeyField] = string(k)
			if len(q.Where) > 0 && !matchWhere(rec, q.Where) {
				return nil
			}
			all = append(all, rec)
			return nil
		})
	})
	if err != nil {
		return nil, "", err
	}

	// 排序（升序；OrderDesc 反向）
	if q.OrderBy != "" {
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

	// next_cursor：还有后续 → 编码最后一行的 (orderByValue, 主键)
	var next string
	if end < len(all) && len(page) > 0 {
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
func compareValue(a, b any) bool {
	if af, ok := toFloat(a); ok {
		if bf, ok2 := toFloat(b); ok2 {
			return af < bf
		}
		return true // 数值 < 非数值
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
// 比较对齐 compareValue：数值优先（避免 "10" < "8" 的字典序错位），否则字符串。
func filterAfter(all []Record, lastVal, lastKey, field string, desc bool) []Record {
	lastF, lastErr := strconv.ParseFloat(lastVal, 64)
	lastIsNum := lastErr == nil
	out := make([]Record, 0, len(all))
	for _, rec := range all {
		key := keyString(rec)
		cur := fieldValue(rec, field)
		var after bool
		if cf, ok := toFloat(cur); ok && lastIsNum {
			if desc {
				after = cf < lastF || (cf == lastF && key > lastKey)
			} else {
				after = cf > lastF || (cf == lastF && key > lastKey)
			}
		} else {
			cs := fieldString(rec, field)
			if desc {
				after = cs < lastVal || (cs == lastVal && key > lastKey)
			} else {
				after = cs > lastVal || (cs == lastVal && key > lastKey)
			}
		}
		if after {
			out = append(out, rec)
		}
	}
	return out
}
