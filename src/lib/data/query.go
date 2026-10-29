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

	"github.com/lsong98sh/chonkpilot-agent/session/store"
)

// Query 是通用查询条件（类 Criteria）。唯一定义在 `chonkpilot-session/store`，
// 此处为别名（组件与数据面同一契约，主仓调用点零改动）。
type Query = store.Query

// earlyProbeMax 是「提前退出」判定 more 时额外探查的**桶键上限**。
//
// 攒够 Offset+Limit 后必须确认「后面还有没有一条匹配行」才能给出精确的 more；若尾部整段被
// Where 过滤掉（如末尾全是其它会话/状态的行），一路探到表尾就退化成全表扫描 —— 与提前退出的
// 初衷相悖。512 的取值依据：列表页实际量级为数十至数百行（`messagePerTurnCap` / 列表页
// 20~200），探测只需覆盖「页边界附近」的长度；每次探测至多一次回表 + 一次 JSON 反序列化
// （微秒级），512 键合计 < 1ms，不改变单页查询的量级。超限则**保守**置 more=true：最坏代价是
// 客户端多发一次请求拿到空页（与改前的无条件 more=true 等价），绝不丢行。
const earlyProbeMax = 512

// Query 执行通用查询：过滤/排序/分页/游标 → (recs, next_cursor, err)。
// 返回的记录带 _key 主键字段。
//
// 索引路由（Phase 1）：**仅在指定 OrderBy** 且 Where 覆盖某索引 Fields 的某个前缀
// （全部等值字符串）时启用——取**最长可用前缀** Seek 索引桶顺序遍历，每个索引值（= 主键）
// 回表 Get 主行，再应用剩余 Where；否则回退全桶扫描。
// 无 OrderBy 时**一律回退全桶扫描**（A-05）：索引遍历序 =（索引 Order 字段,主键），而游标/
// 断页（filterAfter）按（排序值,主键）定位，二者口径不同 → Offset/Cursor 翻页两页排序口径
// 不一致会漏/重行；统一走桶序即两页同口径（含原 Cursor 单独回退场景）。
// 顺序一致（OrderBy 命中索引 Order 的升序，或 OrderBy 为空时的桶序）时跳过内存排序，并允许攒够
// Offset+Limit 后提前退出——**带 Cursor 同样允许**（遍历序 == 结果序 → 越过游标的记录恰构成
// 后缀）；需排序时（OrderDesc / OrderBy 不命中索引序）收集完再排序分页。
// 提前退出时 more（是否仍有后续匹配行）取**精确值**：攒够本页后再探一条匹配行（上限
// earlyProbeMax 个桶键，超限保守置真），不再无条件假定后面还有行（S-4）。
// 返回语义（_key、next_cursor 编码、Offset/Limit/Cursor、坏行跳过）与全桶扫描逐字节一致。
//
// 索引就绪防线（Phase 3a）：二级索引桶只在**建桶之后由写入口写入**，故命中索引前先探测
// 索引桶是否整桶为空；为空则视为「索引未就绪」，回落全表扫描（见函数内注释）。
func (t *Table) Query(q Query) ([]Record, string, error) {
	// ── 游标解码（前移到扫描之前）────────────────────────────────
	// 提前退出（skipSort + Limit）需要在**流式遍历中**判定「本行是否已越过游标」，故游标必须先解码。
	// 游标同时编码 (排序值, 主键)，同值 tie 组因此可按主键切分：越过游标后同值行按主键续接，
	// 提前退出不会把 tie 组截断。坏游标仍返回同一错误（与原先「扫描后再解码」对外等价）。
	var (
		curVal, curKey string
		cursorLast     any
		hasCursor      bool
	)
	if q.Cursor != "" {
		v, k, ok := decodeCursor(q.Cursor)
		if !ok {
			return nil, "", fmt.Errorf("data: bad cursor")
		}
		curVal, curKey, hasCursor = v, k, true
		cursorLast = cursorValue(curVal)
	}

	// 无 OrderBy → 禁用索引路由，一律回退全桶扫描（A-05）：索引遍历序（索引 Order 字段,主键）
	// ≠ 桶序，与游标续查/断页 filterAfter 的 (排序值,主键) 口径不符，两页排序口径不一致会漏/重行；
	// 禁用后与全表扫描同口径（涵盖原「Cursor 且无 OrderBy」的回退场景，语义不回归）。
	// S-4（DATA-OPT-1 P3）：判定前移 —— 无 OrderBy 时**不再选路由索引**（其结果随即被丢弃）。
	var (
		idx        *Index
		prefixVals []string
	)
	if q.OrderBy != "" {
		idx, prefixVals = indexFor(t.name, q.Where)
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
		// 提前退出：按所需顺序流式产出（skipSort）且有界（Limit>0）时启用。
		// **带游标也启用**（S-4 缺陷 2 修复）：否则每页都要扫到表尾 = O(全表)。
		// 带游标时配额 = Limit（不是 Offset+Limit）——Cursor 优先于 Offset，下方会把 Offset 归零；
		// 且只有**严格越过游标**的记录才计入配额（循环内 afterCursor 判定），游标之前的行不计入。
		if skipSort && q.Limit > 0 {
			earlyTarget = q.Offset + q.Limit
			if hasCursor {
				earlyTarget = q.Limit
			}
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
		// probe = 攒够配额后为判定 more 而额外探查的桶键数（护栏见 earlyProbeMax）。
		probe := 0
		for ; k != nil; k, v = c.Next() {
			if idx != nil && !bytes.HasPrefix(k, start) {
				break // 离开前缀区段 → 结束
			}
			if earlyTarget > 0 && len(all) >= earlyTarget {
				// 本页已满：继续向后找一条**匹配行**，找到 → more 精确为真；扫到底都没有 → 保持假。
				// 超过护栏（尾部整段被 Where 过滤的极端形态）→ 保守置真，不退化成全表扫描。
				probe++
				if probe > earlyProbeMax {
					more = true // 保守：宁可多给一页（客户端多取一次得到空页），绝不丢行
					break
				}
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
			if earlyTarget > 0 && hasCursor && !afterCursor(rec, cursorLast, curKey, q.OrderBy, q.OrderDesc) {
				continue // 游标之前的行不计入本页配额（其 (排序值,主键) 未越过游标位置）
			}
			if earlyTarget > 0 && len(all) >= earlyTarget {
				more = true // 已攒够本页，且确实还有一条后续匹配行
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

	// 游标续查（优先于 Offset）。提前退出路径已在扫描内滤过游标之前的行，此处对其幂等；
	// 内存排序路径（skipSort=false，必须收全再排）由此收口。
	if hasCursor {
		q.Offset = 0 // Cursor 优先于 Offset：游标已定位本页起点，忽略 Offset（否则叠加造成二次截断）
		all = filterAfter(all, cursorLast, curKey, q.OrderBy, q.OrderDesc)
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

// cmpAny 是**全序比较的唯一实现**（排序 compare 与游标断页 filterAfter 共用），返回当前值相对
// 目标值的位置：-1 前 / 0 同 / +1 后。
//
// 全序口径（A-13/A-14）：两侧皆数值 → 数值序；数值恒 < 非数值；两侧皆非数值 → 字符串字典序。
// 两处必须同一口径，否则混类型时排序与断页不一致（如 "10" 与 8 的字典序错位）→ 翻页漏/重行。
func cmpAny(a, b any) int {
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	switch {
	case aok && bok:
		switch {
		case af > bf:
			return 1
		case af < bf:
			return -1
		}
		return 0
	case aok: // 数值 < 非数值
		return -1
	case bok:
		return 1
	}
	as, bs := fmt.Sprintf("%v", a), fmt.Sprintf("%v", b)
	switch {
	case as > bs:
		return 1
	case as < bs:
		return -1
	}
	return 0
}

func compare(a, b Record, field string, desc bool) bool {
	c := cmpAny(fieldValue(a, field), fieldValue(b, field))
	if desc {
		return c > 0
	}
	return c < 0
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

// ─── 游标（§5.2.1 连续滚动）────────────────────────────

// encodeCursor：长度前缀编码 `<len(orderVal)>|<orderVal><primaryKey>` 后 URL-safe base64。
//
// 用**长度前缀**而非裸分隔符：排序值（orderVal）本身可能含 `|`，裸 `SplitN(...,"|",2)` 会把
// 排序值拆断 → 断页错位/漏行（A-xx）。长度前缀使边界无歧义。
func encodeCursor(orderVal, pk string) string {
	raw := strconv.Itoa(len(orderVal)) + "|" + orderVal + pk
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (orderVal, pk string, ok bool) {
	b, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", false
	}
	s := string(b)
	sep := strings.IndexByte(s, '|')
	if sep < 0 {
		return "", "", false
	}
	n, err := strconv.Atoi(s[:sep])
	if err != nil || n < 0 || len(s) < sep+1+n {
		return "", "", false
	}
	return s[sep+1 : sep+1+n], s[sep+1+n:], true
}

// filterAfter 保留游标之后的记录：
//
//	升序：(orderVal > last) OR (orderVal == last AND key > lastKey)
//	降序：反向
//
// 判定委托 afterCursor（与提前退出路径共用同一实现）。
func filterAfter(all []Record, last any, lastKey, field string, desc bool) []Record {
	out := make([]Record, 0, len(all))
	for _, rec := range all {
		if afterCursor(rec, last, lastKey, field, desc) {
			out = append(out, rec)
		}
	}
	return out
}

// afterCursor 判定单条记录是否**严格**位于游标（= (排序值, 主键)）之后。
//
// 比较口径 = cmpAny（A-14，与排序同一实现）：两侧皆数值才按数值序；数值恒 < 非数值；
// 两侧皆非数值按字符串字典序。避免「一侧数值一侧字符串」时断页口径与排序口径不一致
// （如 "10" 与 8 的字典序错位）导致翻页漏/重行。
// 同排序值（tie 组）内按主键续接：只有主键**严格大于**游标主键的行才算「之后」——
// 故游标可落在 tie 组内部，提前退出按此判定不会截断 tie 组。
func afterCursor(rec Record, last any, lastKey, field string, desc bool) bool {
	cur := fieldValue(rec, field)
	if cur == nil {
		cur = "" // 列缺失：与既有断页口径一致（空串参与比较）
	}
	// cmp：当前行相对游标行的全序位置（-1 前 / 0 同 / +1 后）。
	cmp := cmpAny(cur, last)
	if desc {
		return cmp < 0 || (cmp == 0 && keyString(rec) > lastKey)
	}
	return cmp > 0 || (cmp == 0 && keyString(rec) > lastKey)
}

// cursorValue 游标值按字面形态归一：可解析为数值 → 数值（与排序侧同口径）；否则原字符串。
func cursorValue(orderVal string) any {
	if f, err := strconv.ParseFloat(orderVal, 64); err == nil {
		return f
	}
	return orderVal
}
