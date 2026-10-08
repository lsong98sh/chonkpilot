package dsl

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ─── 表达式解析 ───

// parseExpr 从 toks[i:] 解析一个值/路径/句柄表达式，返回节点与下一个下标。
func parseExpr(toks []tok, i int) (*Expr, int, error) {
	if i >= len(toks) {
		return nil, i, lineErr(0, "缺少表达式")
	}
	t := toks[i]
	ln := t.line
	e := &Expr{Ln: ln}
	switch t.kind {
	case tokStr:
		e.Kind = eLit
		e.Lit = t.text
		i++
	case tokNum:
		e.Kind = eLit
		e.Lit = t.f
		i++
	case tokJSON:
		e.Kind = eLit
		e.Lit = t.val
		i++
	case tokWord:
		switch strings.ToLower(t.text) {
		case "true":
			e.Kind = eLit
			e.Lit = true
			i++
		case "false":
			e.Kind = eLit
			e.Lit = false
			i++
		case "null":
			e.Kind = eLit
			e.Lit = nil
			i++
		default:
			e.Kind = eVar
			e.Name = t.text
			i++
		}
	case tokOp:
		if t.text == "#" || t.text == "@" {
			e.Kind = eHandle
			e.Rune = t.text[0]
			i++
			if i >= len(toks) || toks[i].kind != tokStr {
				return nil, i, lineErr(ln, "句柄需要 #\"路径\" 形式")
			}
			e.Path = toks[i].text
			i++
		} else {
			return nil, i, lineErr(ln, "表达式不能以 %q 开头", t.text)
		}
	default:
		return nil, i, lineErr(ln, "表达式错误")
	}
	// 下标 [N]（仅单层，跟在变量/句柄之后）
	if i < len(toks) && toks[i].kind == tokSub {
		subToks, ok := toks[i].val.([]tok)
		if !ok {
			return nil, i, lineErr(ln, "下标内部错误")
		}
		idx, _, err := parseExpr(subToks, 0)
		if err != nil {
			return nil, i, wrapLn(err, ln)
		}
		e.Idx = idx
		i++
	}
	// 访问器链
	for i < len(toks) && toks[i].kind == tokOp && toks[i].text == "." {
		i++
		if i >= len(toks) || toks[i].kind != tokWord {
			return nil, i, lineErr(ln, "'.' 后需要字段名或调用")
		}
		seg := Seg{Name: toks[i].text}
		i++
		if i < len(toks) && toks[i].kind == tokOp && toks[i].text == "(" {
			seg.Call = true
			i++
			for {
				if i >= len(toks) {
					return nil, i, lineErr(ln, "调用 %s 缺右括号", seg.Name)
				}
				if toks[i].kind == tokOp && toks[i].text == ")" {
					i++
					break
				}
				arg, ni, err := parseExpr(toks, i)
				if err != nil {
					return nil, ni, err
				}
				seg.Args = append(seg.Args, arg)
				i = ni
				if i < len(toks) && toks[i].kind == tokOp && toks[i].text == "," {
					i++
					continue
				}
			}
		}
		e.Segs = append(e.Segs, seg)
	}
	return e, i, nil
}

// ─── 求值 ───

// dbTablesList 是 db.tables 的中间值（后跟表名=表句柄；单独结束=表名列表）。
type dbTablesList struct{ db *VDB }

// evalExprValue 求值表达式为运行时值。
func evalExprValue(sc *Scope, e *Expr) (any, error) {
	if e == nil {
		return nil, nil
	}
	var cur any
	switch e.Kind {
	case eLit:
		cur = e.Lit
		if s, ok := cur.(string); ok {
			return sc.Interp(s)
		}
		return cur, nil
	case eHandle:
		p, err := sc.Interp(e.Path)
		if err != nil {
			return nil, lineErr(e.Ln, "%s", err.Error())
		}
		if e.Rune == '#' {
			cur = &VFile{FS: sc.eng.files, Path: p}
		} else {
			cur = &VDB{DB: sc.eng.dbs, Path: p}
		}
	case eVar:
		if e.Name == returnVar {
			// $RETURN 是只写结果通道，读取一律报错（见 return.go）。
			return nil, lineErr(e.Ln, "%s", returnNoRead)
		}
		cur, _ = sc.Lookup(e.Name) // 未定义 = nil（IF 视缺失）
	default:
		return nil, lineErr(e.Ln, "未知表达式类型")
	}
	// 下标求值
	if e.Idx != nil && cur != nil {
		idxVal, err := evalExprValue(sc, e.Idx)
		if err != nil {
			return nil, err
		}
		idx, err := toIntIndex(idxVal, e.Ln)
		if err != nil {
			return nil, err
		}
		list, ok := cur.([]any)
		if !ok {
			return nil, lineErr(e.Ln, "下标只能用于列表")
		}
		if idx < 0 {
			idx += len(list)
		}
		if idx < 0 || idx >= len(list) {
			return nil, lineErr(e.Ln, "下标 %d 越界（列表长度 %d）", idx, len(list))
		}
		cur = list[idx]
	}
	for _, seg := range e.Segs {
		if cur == nil {
			// 缺失/null：链式读取在 nil 上继续返回 nil（IF 视 false）
			cur = nil
			continue
		}
		v, err := applySeg(cur, seg, e.Ln)
		if err != nil {
			return nil, err
		}
		cur = v
	}
	if mt, ok := cur.(*dbTablesList); ok {
		db := mt.db.DB.OpenDB(mt.db.Path)
		names, err := db.TableNames()
		if err != nil {
			return nil, lineErr(e.Ln, "读取表列表失败：%s", err.Error())
		}
		list := make([]any, len(names))
		for i, n := range names {
			list[i] = n
		}
		return list, nil
	}
	return cur, nil
}

var fileAccessors = map[string]bool{
	"path": true, "size": true, "count": true, "blocks": true,
	"content": true, "lines": true, "array": true, "object": true, "eof": true,
}

func isFieldAccessor(name string) bool {
	n := strings.ToLower(name)
	return fileAccessors[n] || n == "tables" || n == "rows" || n == "range" || n == "query"
}

// applySeg 对当前值应用一个访问器段。
func applySeg(cur any, seg Seg, ln int) (any, error) {
	if seg.Call {
		n := strings.ToLower(seg.Name)
		switch n {
		case "range":
			args, err := rangeArgs(seg, ln)
			if err != nil {
				return nil, err
			}
			return applyRange(cur, args[0], args[1], ln)
		case "query":
			if len(seg.Args) != 1 || seg.Args[0].Kind != eLit {
				return nil, lineErr(ln, "query 需要 1 个字符串参数")
			}
			qstr, _ := seg.Args[0].Lit.(string)
			tb, ok := cur.(*VTable)
			if !ok {
				return nil, lineErr(ln, "query 只能用于表句柄")
			}
			recs, err := tb.Th.Query(qstr)
			if err != nil {
				return nil, lineErr(ln, "查询失败：%s", err.Error())
			}
			return recs, nil
		default:
			return nil, lineErr(ln, "不支持对当前值调用 .%s()", seg.Name)
		}
	}
	// map/记录字段
	if m, ok := asMap(cur); ok {
		v, ok := m[seg.Name]
		if !ok {
			return nil, nil
		}
		return v, nil
	}
	name := seg.Name
	lc := strings.ToLower(name)
	switch t := cur.(type) {
	case *VFile:
		return fileAccess(t, lc, ln)
	case *VDB:
		if lc == "tables" {
			return &dbTablesList{db: t}, nil
		}
		return nil, lineErr(ln, "数据库句柄不支持访问 .%s", name)
	case *dbTablesList:
		db := t.db.DB.OpenDB(t.db.Path)
		th := db.Table(name) // 裸名永远代表表名
		return &VTable{DB: t.db, Name: name, Th: th}, nil
	case *VTable:
		switch lc {
		case "rows":
			return float64(t.Th.Count()), nil
		case "content":
			return smartTableRead(t, ln)
		case "eof":
			return "", nil
		}
		return nil, lineErr(ln, "表句柄不支持访问 .%s", name)
	case []any:
		return nil, nil
	}
	return nil, nil
}

// fileAccess 文件句柄访问器。
func fileAccess(f *VFile, lc string, ln int) (any, error) {
	fh := f.handle()
	switch lc {
	case "path":
		return f.Path, nil
	case "eof":
		return "", nil
	case "size", "count", "blocks":
		st, err := fh.Stat()
		if err != nil {
			return nil, lineErr(ln, "stat 失败：%s", err.Error())
		}
		switch lc {
		case "size":
			return float64(st.Size), nil
		case "count":
			return float64(st.Lines), nil
		default:
			return float64(st.Blocks), nil
		}
	case "content":
		s, err := fh.ReadText()
		if err != nil {
			return nil, lineErr(ln, "读取失败：%s", err.Error())
		}
		return s, nil
	case "lines":
		ls, err := fh.ReadLines()
		if err != nil {
			return nil, lineErr(ln, "读取失败：%s", err.Error())
		}
		out := make([]any, len(ls))
		for i, s := range ls {
			out[i] = s
		}
		return out, nil
	case "array":
		s, err := fh.ReadText()
		if err != nil {
			return nil, lineErr(ln, "读取失败：%s", err.Error())
		}
		var list []any
		if err := json.Unmarshal([]byte(s), &list); err != nil {
			return nil, lineErr(ln, "解析为 JSON 数组失败（非数组）：%s", err.Error())
		}
		return list, nil
	case "object":
		s, err := fh.ReadText()
		if err != nil {
			return nil, lineErr(ln, "读取失败：%s", err.Error())
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(s), &obj); err != nil {
			return nil, lineErr(ln, "解析为 JSON 对象失败（非对象）：%s", err.Error())
		}
		return obj, nil
	}
	return nil, lineErr(ln, "文件句柄不支持访问 .%s", lc)
}

// smartTableRead 智能内容：小返回记录数组，大返回描述+预览。
func smartTableRead(t *VTable, ln int) (any, error) {
	c := t.Th.Count()
	if c <= 50 {
		recs, err := t.Th.Records()
		if err != nil {
			return nil, lineErr(ln, "读取表失败：%s", err.Error())
		}
		return recs, nil
	}
	pre, _ := t.Th.RecordsRange(0, 2)
	return fmt.Sprintf("[table: %s, %d rows, preview: %v...]", t.Name, c, pre), nil
}

// rangeArgs 解析 range 的两个整型参数。
func rangeArgs(seg Seg, ln int) ([2]int, error) {
	var out [2]int
	if len(seg.Args) != 2 {
		return out, lineErr(ln, "range 需要 2 个参数 (N,M)")
	}
	for i := 0; i < 2; i++ {
		a := seg.Args[i]
		if a.Kind != eLit {
			return out, lineErr(ln, "range 参数须为数字")
		}
		f, ok := a.Lit.(float64)
		if !ok {
			return out, lineErr(ln, "range 参数须为数字")
		}
		out[i] = int(f)
	}
	return out, nil
}

// applyRange 对可切片值应用范围（负数为倒数；越界报错）。
func applyRange(cur any, n, m int, ln int) (any, error) {
	switch t := cur.(type) {
	case []any:
		return sliceRange(t, n, m, ln)
	case *Rec:
		return applyRange(t.V, n, m, ln)
	case *VFile:
		fh := t.handle()
		ls, err := fh.ReadRange(n, m)
		if err != nil {
			return nil, lineErr(ln, "range 越界或读取失败：%s", err.Error())
		}
		out := make([]any, len(ls))
		for i, s := range ls {
			out[i] = s
		}
		return out, nil
	case *VTable:
		recs, err := t.Th.RecordsRange(n, m)
		if err != nil {
			return nil, lineErr(ln, "range 越界：%s", err.Error())
		}
		return recs, nil
	}
	return nil, lineErr(ln, ".range 只能用于列表/文件/表")
}

// sliceRange 对内存列表切片（n..m 含端点；负=倒数；越界报错）。
func sliceRange(list []any, n, m int, ln int) ([]any, error) {
	L := len(list)
	if L == 0 {
		if (n == 0 && (m == -1 || m == 0)) || (n == 0 && m == 0) {
			return []any{}, nil
		}
		return nil, lineErr(ln, "range 越界：列表为空")
	}
	n, m = normalizeIdx(n, m, L)
	if n < 0 || n >= L || m < n || m >= L {
		return nil, lineErr(ln, "range(%d,%d) 越界（列表长度 %d）", n, m, L)
	}
	return list[n : m+1], nil
}

func normalizeIdx(n, m, L int) (int, int) {
	if n < 0 {
		n += L
	}
	if m < 0 {
		m += L
	}
	return n, m
}

// toIntIndex 把下标值转为整数索引（float64 → int；整数标量 → int；其余报错）。
func toIntIndex(v any, ln int) (int, error) {
	switch t := v.(type) {
	case float64:
		i := int(t)
		if float64(i) != t {
			return 0, lineErr(ln, "下标必须为整数，得到 %v", t)
		}
		return i, nil
	case int:
		return t, nil
	case int64:
		return int(t), nil
	}
	return 0, lineErr(ln, "下标必须为数字，得到 %T", v)
}
