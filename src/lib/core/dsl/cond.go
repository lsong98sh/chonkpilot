package dsl

import (
	"strings"
)

// ─── 条件 AST ───

type condCmpN struct {
	Ln    int
	Left  *Expr
	Op    string
	Right *Expr
}

type condExistN struct {
	Ln      int
	Operand *Expr
}

// condPlain 单操作数真值判断（缺失/null 视 false）。
type condPlainN struct {
	Ln      int
	Operand *Expr
}

type condNotN struct {
	Ln  int
	Sub Cond
}

type condAndN struct {
	Ln    int
	Items []Cond
}

type condOrN struct {
	Ln    int
	Items []Cond
}

func (c *condCmpN) line() int   { return c.Ln }
func (c *condExistN) line() int { return c.Ln }
func (c *condPlainN) line() int { return c.Ln }
func (c *condNotN) line() int   { return c.Ln }
func (c *condAndN) line() int   { return c.Ln }
func (c *condOrN) line() int    { return c.Ln }

var cmpOps = map[string]bool{"==": true, "!=": true, ">": true, ">=": true, "<": true, "<=": true}

// parseCond 解析条件（优先级 not > and > or；括号可覆盖），返回条件与消耗 token 数。
func parseCond(toks []tok) (Cond, int, error) {
	c, i, err := parseOr(toks, 0)
	if err != nil {
		return nil, i, err
	}
	return c, i, nil
}

func parseOr(toks []tok, i int) (Cond, int, error) {
	left, i, err := parseAnd(toks, i)
	if err != nil {
		return nil, i, err
	}
	items := []Cond{left}
	for i < len(toks) && toks[i].kind == tokWord && strings.EqualFold(toks[i].text, "or") {
		i++
		right, ni, err := parseAnd(toks, i)
		if err != nil {
			return nil, ni, err
		}
		items = append(items, right)
		i = ni
	}
	if len(items) == 1 {
		return items[0], i, nil
	}
	return &condOrN{Ln: items[0].line(), Items: items}, i, nil
}

func parseAnd(toks []tok, i int) (Cond, int, error) {
	left, i, err := parseUnary(toks, i)
	if err != nil {
		return nil, i, err
	}
	items := []Cond{left}
	for i < len(toks) && toks[i].kind == tokWord && strings.EqualFold(toks[i].text, "and") {
		i++
		right, ni, err := parseUnary(toks, i)
		if err != nil {
			return nil, ni, err
		}
		items = append(items, right)
		i = ni
	}
	if len(items) == 1 {
		return items[0], i, nil
	}
	return &condAndN{Ln: items[0].line(), Items: items}, i, nil
}

func parseUnary(toks []tok, i int) (Cond, int, error) {
	if i < len(toks) && toks[i].kind == tokWord && strings.EqualFold(toks[i].text, "not") {
		ln := toks[i].line
		i++
		sub, ni, err := parseUnary(toks, i)
		if err != nil {
			return nil, ni, err
		}
		return &condNotN{Ln: ln, Sub: sub}, ni, nil
	}
	return parseFactor(toks, i)
}

func parseFactor(toks []tok, i int) (Cond, int, error) {
	if i >= len(toks) {
		return nil, i, lineErr(0, "条件缺少操作数")
	}
	ln := toks[i].line
	// 括号
	if toks[i].kind == tokOp && toks[i].text == "(" {
		c, ni, err := parseOr(toks, i+1)
		if err != nil {
			return nil, ni, err
		}
		if ni >= len(toks) || toks[ni].kind != tokOp || toks[ni].text != ")" {
			return nil, ni, lineErr(ln, "条件缺右括号")
		}
		return c, ni + 1, nil
	}
	// exist
	if toks[i].kind == tokWord && strings.EqualFold(toks[i].text, "exist") {
		e, ni, err := parseExpr(toks, i+1)
		if err != nil {
			return nil, ni, err
		}
		return &condExistN{Ln: ln, Operand: e}, ni, nil
	}
	// 比较 / 单操作数真值
	left, ni, err := parseExpr(toks, i)
	if err != nil {
		return nil, ni, err
	}
	if ni < len(toks) && toks[ni].kind == tokOp && cmpOps[toks[ni].text] {
		op := toks[ni].text
		right, n2, err := parseExpr(toks, ni+1)
		if err != nil {
			return nil, n2, err
		}
		return &condCmpN{Ln: ln, Left: left, Op: op, Right: right}, n2, nil
	}
	return &condPlainN{Ln: ln, Operand: left}, ni, nil
}

// evalCond 求值条件。
func evalCond(sc *Scope, c Cond) (bool, error) {
	switch t := c.(type) {
	case *condOrN:
		for _, it := range t.Items {
			b, err := evalCond(sc, it)
			if err != nil {
				return false, err
			}
			if b {
				return true, nil
			}
		}
		return false, nil
	case *condAndN:
		for _, it := range t.Items {
			b, err := evalCond(sc, it)
			if err != nil {
				return false, err
			}
			if !b {
				return false, nil
			}
		}
		return true, nil
	case *condNotN:
		b, err := evalCond(sc, t.Sub)
		if err != nil {
			return false, err
		}
		return !b, nil
	case *condExistN:
		return evalExist(sc, t.Operand, t.Ln)
	case *condPlainN:
		v, err := evalExprValue(sc, t.Operand)
		if err != nil {
			return false, err
		}
		return truthy(v), nil
	case *condCmpN:
		lv, err := evalExprValue(sc, t.Left)
		if err != nil {
			return false, err
		}
		rv, err := evalExprValue(sc, t.Right)
		if err != nil {
			return false, err
		}
		return compare(lv, rv, t.Op), nil
	}
	return false, nil
}

// truthy 真值判断（缺失/null 视 false）。
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case *Rec:
		return truthy(t.V)
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return true
	case *VFile:
		return t.handle().Exists()
	case *VDB:
		return t.DB.OpenDB(t.Path).Exists()
	case *VTable:
		return t.Th.Exists()
	default:
		return true
	}
}

// evalExist 存在性：文件句柄/路径、库句柄、表句柄、变量/列表。
func evalExist(sc *Scope, e *Expr, ln int) (bool, error) {
	switch e.Kind {
	case eHandle:
		if e.Rune == '#' {
			p, err := sc.Interp(e.Path)
			if err != nil {
				return false, err
			}
			return sc.eng.files.Open(p).Exists(), nil
		}
		p, err := sc.Interp(e.Path)
		if err != nil {
			return false, err
		}
		return sc.eng.dbs.OpenDB(p).Exists(), nil
	case eLit:
		// "文件路径" 字符串 → 文件存在性（dsl-core 5.1：IF exist "路径"）
		if s, ok := e.Lit.(string); ok {
			p, err := sc.Interp(s)
			if err != nil {
				return false, err
			}
			return sc.eng.files.Open(p).Exists(), nil
		}
		return truthy(e.Lit), nil
	default:
		// 求值为句柄值则按句柄判存在；变量/列表非空判存在
		v, err := evalExprValue(sc, e)
		if err != nil {
			return false, err
		}
		switch t := v.(type) {
		case *VFile:
			return t.handle().Exists(), nil
		case *VDB:
			return t.DB.OpenDB(t.Path).Exists(), nil
		case *VTable:
			return t.Th.Exists(), nil
		case *dbTablesList:
			return true, nil
		default:
			return truthy(v), nil
		}
	}
}

// compare 比较两个值。
func compare(lv, rv any, op string) bool {
	// 数值优先
	lf, lok := toNum(lv)
	rf, rok := toNum(rv)
	var res int
	if lok && rok {
		switch {
		case lf < rf:
			res = -1
		case lf > rf:
			res = 1
		}
	} else {
		ls := normText(lv)
		rs := normText(rv)
		res = strings.Compare(ls, rs)
	}
	// nil 语义：==/!= 特殊
	switch op {
	case "==":
		if isNil(lv) || isNil(rv) {
			return isNil(lv) && isNil(rv)
		}
		return res == 0
	case "!=":
		if isNil(lv) || isNil(rv) {
			return !(isNil(lv) && isNil(rv))
		}
		return res != 0
	case ">":
		return res > 0
	case ">=":
		return res >= 0
	case "<":
		return res < 0
	case "<=":
		return res <= 0
	}
	return false
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	if r, ok := v.(*Rec); ok {
		return isNil(r.V)
	}
	return false
}

func toNum(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case *Rec:
		return toNum(t.V)
	}
	return 0, false
}

func normText(v any) string {
	if isNil(v) {
		return ""
	}
	switch t := v.(type) {
	case float64:
		return fmtNumber(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case *Rec:
		return normText(t.V)
	}
	s, err := interpText(v)
	if err != nil {
		return ""
	}
	return s
}
