package dsl

import (
	"strings"
)

// ─── AST ───

// Script 一段脚本（顶层语句序列）。
type Script struct {
	Stmts []Stmt
}

// Stmt 语句节点。
type Stmt interface {
	Line() int
}

type baseStmt struct{ Ln int }

func (b baseStmt) Line() int { return b.Ln }

// ActionStmt 动作语句（LLM/WIN/CLK/...）。
type ActionStmt struct {
	baseStmt
	Verb        string // 动词（原文）
	Args        string // 参数规范文本（非 Raw 动作，可含 => 前部分）
	Target      *Expr  // 可选 => 目标
	RawArgs     string // Raw 动作：参数原文（verb 之后整行）
	RawArgsMode bool
}

// SetStmt 赋值：SET 值 => 目标。
type SetStmt struct {
	baseStmt
	Value  *Expr
	Target *Expr
}

// IfStmt IF 块（无 ELSE）。
type IfStmt struct {
	baseStmt
	Cond  Cond
	Block []Stmt
}

// LoopStmt LOOP 块。
type LoopStmt struct {
	baseStmt
	Var         string
	Source      *Expr
	Concurrency int
	Block       []Stmt
}

// ParallelStmt PARALLEL 块（每个顶层语句 = 一个并发分支）。
type ParallelStmt struct {
	baseStmt
	Block []Stmt
}

// BreakStmt / ContinueStmt / ExitStmt 流控。
type BreakStmt struct{ baseStmt }
type ContinueStmt struct{ baseStmt }
type ExitStmt struct{ baseStmt }

// TypeofStmt TYPEOF 值 => 变量。
type TypeofStmt struct {
	baseStmt
	Value  *Expr
	Target *Expr
}

// EntryStmt ENTRY 对象 => 变量。
type EntryStmt struct {
	baseStmt
	Value  *Expr
	Target *Expr
}

// SplitStmt SPLIT 文本, 分隔符 => 变量。
type SplitStmt struct {
	baseStmt
	Text   *Expr
	Sep    *Expr
	Target *Expr
}

// JoinStmt JOIN 数组, 分隔符 => 变量。
type JoinStmt struct {
	baseStmt
	Array  *Expr
	Sep    *Expr
	Target *Expr
}

// PushStmt PUSH 值 => 变量。
type PushStmt struct {
	baseStmt
	Value  *Expr
	Target *Expr
}

// ─── 表达式（值/路径/句柄 + 访问器链）───

type exprKind int

const (
	eLit    exprKind = iota // 字面量（string/number/bool/nil）
	eVar                    // 变量路径（可带访问器链）
	eHandle                 // #"path" / @"path"（可带访问器链）
)

// Seg 访问器段：字段名或调用（.content / .range(N,M) / .query("..")）。
type Seg struct {
	Name string
	Call bool
	Args []*Expr // call 参数（字面量表达式）
}

// Expr 表达式节点。
type Expr struct {
	Kind exprKind
	Ln   int
	Lit  any    // eLit
	Name string // eVar：变量名
	Rune byte   // eHandle：'#' / '@'
	Path string // eHandle：路径模板（可含 {{}}）
	Idx  *Expr  // 下标 [N]（仅单层，不支持链式）
	Segs []Seg
}

// Cond 条件节点。
type Cond interface{ line() int }

type condCmp struct {
	Ln    int
	Left  *Expr
	Op    string
	Right *Expr
}

type condExist struct {
	Ln      int
	Operand *Expr
}

type condNot struct {
	Ln  int
	Sub Cond
}

type condAnd struct {
	Ln    int
	Items []Cond
}

type condOr struct {
	Ln    int
	Items []Cond
}

func (c *condCmp) line() int   { return c.Ln }
func (c *condExist) line() int { return c.Ln }
func (c *condNot) line() int   { return c.Ln }
func (c *condAnd) line() int   { return c.Ln }
func (c *condOr) line() int    { return c.Ln }

// ─── 语句/条件解析 ───

type parser struct {
	stmts []rawStmt
	i     int
}

var reservedVerbs = map[string]bool{
	"SET": true, "IF": true, "LOOP": true, "PARALLEL": true,
	"BREAK": true, "CONTINUE": true, "EXIT": true, "END": true,
	"TYPEOF": true, "ENTRY": true, "SPLIT": true, "JOIN": true, "PUSH": true,
}

// ParseScript 解析脚本（无 Raw 动词；仅语法）。
func ParseScript(input string) (*Script, error) {
	return parseWithRaw(input, nil)
}

// parseWithRaw 解析脚本（rawVerbs 动词的参数按原文透传）。
func parseWithRaw(input string, rawVerbs map[string]bool) (*Script, error) {
	stmts, err := lexToStmts(input, rawVerbs)
	if err != nil {
		return nil, err
	}
	p := &parser{stmts: stmts}
	body, err := p.parseBody(0)
	if err != nil {
		return nil, err
	}
	if p.i < len(p.stmts) {
		rs := p.stmts[p.i]
		return nil, lineErr(rs.line, "意外的语句：%s", strings.ToUpper(firstWord(&rs)))
	}
	return &Script{Stmts: body}, nil
}

// parseBody 解析语句序列直到 END（END 消费掉）。depth = 当前块深度。
func (p *parser) parseBody(depth int) ([]Stmt, error) {
	if depth > MaxDepth {
		rs := p.current()
		if rs != nil {
			return nil, lineErr(rs.line, "嵌套深度超过 %d", MaxDepth)
		}
		return nil, lineErr(0, "嵌套深度超过 %d", MaxDepth)
	}
	var out []Stmt
	for p.i < len(p.stmts) {
		rs := &p.stmts[p.i]
		first := strings.ToUpper(firstWord(rs))
		if first == "END" {
			p.i++
			return out, nil
		}
		st, err := p.parseOne(rs, depth)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

func (p *parser) current() *rawStmt {
	if p.i < len(p.stmts) {
		return &p.stmts[p.i]
	}
	return nil
}

func firstWord(rs *rawStmt) string {
	for _, t := range rs.toks {
		if t.kind == tokWord {
			return t.text
		}
	}
	return ""
}

// parseOne 解析单条语句并消费。
func (p *parser) parseOne(rs *rawStmt, depth int) (Stmt, error) {
	verb := strings.ToUpper(firstWord(rs))
	ln := rs.line
	toks := rs.toks
	switch verb {
	case "SET":
		return p.parseSet(rs)
	case "IF":
		cond, rest, err := parseCond(toks[1:])
		if err != nil {
			return nil, wrapLn(err, ln)
		}
		if rest < len(toks)-1 {
			return nil, lineErr(ln, "IF 条件后有多余内容")
		}
		p.i++
		block, err := p.parseBody(depth + 1)
		if err != nil {
			return nil, err
		}
		return &IfStmt{baseStmt: baseStmt{Ln: ln}, Cond: cond, Block: block}, nil
	case "LOOP":
		return p.parseLoop(rs, depth)
	case "PARALLEL":
		p.i++
		block, err := p.parseBody(depth + 1)
		if err != nil {
			return nil, err
		}
		return &ParallelStmt{baseStmt: baseStmt{Ln: ln}, Block: block}, nil
	case "TYPEOF":
		return p.parseTypeof(rs)
	case "ENTRY":
		return p.parseEntry(rs)
	case "SPLIT":
		return p.parseSplit(rs)
	case "JOIN":
		return p.parseJoin(rs)
	case "PUSH":
		return p.parsePush(rs)
	case "BREAK", "CONTINUE", "EXIT":
		if len(toks) > 1 {
			return nil, lineErr(ln, "%s 后不允许跟内容", verb)
		}
		p.i++
		switch verb {
		case "BREAK":
			return &BreakStmt{baseStmt{Ln: ln}}, nil
		case "CONTINUE":
			return &ContinueStmt{baseStmt{Ln: ln}}, nil
		default:
			return &ExitStmt{baseStmt{Ln: ln}}, nil
		}
	case "END":
		return nil, lineErr(ln, "意外的 END")
	default:
		if rs.isRaw {
			// Raw 动作：参数原文透传（工具动词坐标/按键等自由文法）；行尾 "=> 目标" 已由 lex 分离
			verb := rs.toks[0].text
			p.i++
			st := &ActionStmt{baseStmt: baseStmt{Ln: ln}, Verb: verb, RawArgs: rs.argsRaw, RawArgsMode: true}
			if len(rs.targetToks) > 0 {
				e, _, err := parseExpr(rs.targetToks, 0)
				if err != nil {
					return nil, wrapLn(err, ln)
				}
				if isReadOnlyTarget(e) {
					return nil, lineErr(ln, "%s => 目标 %s 是只读访问器", verb, exprText(e))
				}
				st.Target = e
			}
			return st, nil
		}
		// 动作语句：动词 + 参数（可选 => 目标）
		return p.parseAction(rs)
	}
}

func wrapLn(err error, ln int) error {
	if le, ok := err.(*LineError); ok {
		return le
	}
	return &LineError{Line: ln, Msg: err.Error()}
}

// parseSet 解析 SET：SET <值表达式> => <目标表达式>
func (p *parser) parseSet(rs *rawStmt) (Stmt, error) {
	toks := rs.toks
	arrow := findArrow(toks)
	if arrow < 0 {
		return nil, lineErr(rs.line, "SET 需要 '=>' 分隔值与目标")
	}
	val, _, err := parseExpr(toks[1:arrow], 0)
	if err != nil {
		return nil, wrapLn(err, rs.line)
	}
	tgt, _, err := parseExpr(toks[arrow+1:], 0)
	if err != nil {
		return nil, wrapLn(err, rs.line)
	}
	// 下标方向规则：
	// 两侧均含下标 → 报错
	if hasSubscript(val) && hasSubscript(tgt) {
		return nil, lineErr(rs.line, "SET 值与目标不能同时含有下标")
	}
	// 左下标 + 右字面量 → 报错（不做交换）
	if hasSubscript(val) && isLiteral(tgt) {
		return nil, lineErr(rs.line, "SET 目标不能是字面量")
	}
	// 兼容 dsl-core 幂等样例的 "SET item.done => true" 书写（字段在左、值在右）：
	// 目标为字面量且值侧是对象字段路径时交换为 "值 => 字段路径"。
	// 含下标的表达式不参与交换。
	if isLiteral(tgt) && val.Kind == eVar && len(val.Segs) > 0 && !segAnyCall(val) && !hasSubscript(val) {
		val, tgt = tgt, val
	}
	if isReadOnlyTarget(tgt) {
		return nil, lineErr(rs.line, "SET 目标 %s 是只读访问器，不能作为写入目标", exprText(tgt))
	}
	p.i++
	return &SetStmt{baseStmt: baseStmt{Ln: rs.line}, Value: val, Target: tgt}, nil
}

func isLiteral(e *Expr) bool { return e != nil && e.Kind == eLit }

// ─── 新指令解析 ───

// parseTwoArg 通用"动词 值, 参数 => 目标"解析（SPLIT/JOIN 共用）。
func (p *parser) parseTwoArg(rs *rawStmt, verb string) (*Expr, *Expr, *Expr, error) {
	toks := rs.toks
	arrow := findArrow(toks)
	if arrow < 0 {
		return nil, nil, nil, lineErr(rs.line, "%s 需要 '=>' 分隔值与目标", verb)
	}
	mid := findComma(toks[1:arrow])
	if mid < 0 {
		return nil, nil, nil, lineErr(rs.line, "%s 需要逗号分隔两个参数", verb)
	}
	mid += 1 // 相对 toks 偏移
	first, _, err := parseExpr(toks[1:mid], 0)
	if err != nil {
		return nil, nil, nil, wrapLn(err, rs.line)
	}
	second, _, err := parseExpr(toks[mid+1:arrow], 0)
	if err != nil {
		return nil, nil, nil, wrapLn(err, rs.line)
	}
	tgt, _, err := parseExpr(toks[arrow+1:], 0)
	if err != nil {
		return nil, nil, nil, wrapLn(err, rs.line)
	}
	return first, second, tgt, nil
}

// findComma 找逗号 token 下标（从起始位置）。
func findComma(toks []tok) int {
	for i, t := range toks {
		if t.kind == tokOp && t.text == "," {
			return i
		}
	}
	return -1
}

// parseTypeof TYPEOF 值 => 变量
func (p *parser) parseTypeof(rs *rawStmt) (Stmt, error) {
	toks := rs.toks
	arrow := findArrow(toks)
	if arrow < 0 {
		return nil, lineErr(rs.line, "TYPEOF 需要 '=>' 分隔值与目标")
	}
	val, _, err := parseExpr(toks[1:arrow], 0)
	if err != nil {
		return nil, wrapLn(err, rs.line)
	}
	tgt, _, err := parseExpr(toks[arrow+1:], 0)
	if err != nil {
		return nil, wrapLn(err, rs.line)
	}
	if tgt.Kind != eVar || len(tgt.Segs) > 0 || tgt.Idx != nil {
		return nil, lineErr(rs.line, "TYPEOF 目标必须是裸变量")
	}
	p.i++
	return &TypeofStmt{baseStmt: baseStmt{Ln: rs.line}, Value: val, Target: tgt}, nil
}

// parseEntry ENTRY 对象 => 变量
func (p *parser) parseEntry(rs *rawStmt) (Stmt, error) {
	toks := rs.toks
	arrow := findArrow(toks)
	if arrow < 0 {
		return nil, lineErr(rs.line, "ENTRY 需要 '=>' 分隔值与目标")
	}
	val, _, err := parseExpr(toks[1:arrow], 0)
	if err != nil {
		return nil, wrapLn(err, rs.line)
	}
	tgt, _, err := parseExpr(toks[arrow+1:], 0)
	if err != nil {
		return nil, wrapLn(err, rs.line)
	}
	if tgt.Kind != eVar || len(tgt.Segs) > 0 || tgt.Idx != nil {
		return nil, lineErr(rs.line, "ENTRY 目标必须是裸变量")
	}
	p.i++
	return &EntryStmt{baseStmt: baseStmt{Ln: rs.line}, Value: val, Target: tgt}, nil
}

// parseSplit SPLIT 文本, 分隔符 => 变量
func (p *parser) parseSplit(rs *rawStmt) (Stmt, error) {
	first, second, tgt, err := p.parseTwoArg(rs, "SPLIT")
	if err != nil {
		return nil, err
	}
	if tgt.Kind != eVar || len(tgt.Segs) > 0 || tgt.Idx != nil {
		return nil, lineErr(rs.line, "SPLIT 目标必须是裸变量")
	}
	p.i++
	return &SplitStmt{baseStmt: baseStmt{Ln: rs.line}, Text: first, Sep: second, Target: tgt}, nil
}

// parseJoin JOIN 数组, 分隔符 => 变量
func (p *parser) parseJoin(rs *rawStmt) (Stmt, error) {
	first, second, tgt, err := p.parseTwoArg(rs, "JOIN")
	if err != nil {
		return nil, err
	}
	if tgt.Kind != eVar || len(tgt.Segs) > 0 || tgt.Idx != nil {
		return nil, lineErr(rs.line, "JOIN 目标必须是裸变量")
	}
	p.i++
	return &JoinStmt{baseStmt: baseStmt{Ln: rs.line}, Array: first, Sep: second, Target: tgt}, nil
}

// parsePush PUSH 值 => 变量
func (p *parser) parsePush(rs *rawStmt) (Stmt, error) {
	toks := rs.toks
	arrow := findArrow(toks)
	if arrow < 0 {
		return nil, lineErr(rs.line, "PUSH 需要 '=>' 分隔值与目标")
	}
	val, _, err := parseExpr(toks[1:arrow], 0)
	if err != nil {
		return nil, wrapLn(err, rs.line)
	}
	tgt, _, err := parseExpr(toks[arrow+1:], 0)
	if err != nil {
		return nil, wrapLn(err, rs.line)
	}
	if tgt.Kind != eVar || len(tgt.Segs) > 0 || tgt.Idx != nil {
		return nil, lineErr(rs.line, "PUSH 目标必须是裸变量")
	}
	p.i++
	return &PushStmt{baseStmt: baseStmt{Ln: rs.line}, Value: val, Target: tgt}, nil
}

// hasSubscript 表达式是否含下标。
func hasSubscript(e *Expr) bool { return e != nil && e.Idx != nil }

func segAnyCall(e *Expr) bool {
	for _, s := range e.Segs {
		if s.Call {
			return true
		}
	}
	return false
}

func (p *parser) parseAction(rs *rawStmt) (Stmt, error) {
	toks := rs.toks
	verb := toks[0].text
	rest := toks[1:]
	arrow := findArrow(rest)
	var tgt *Expr
	if arrow >= 0 {
		e, _, err := parseExpr(rest[arrow+1:], 0)
		if err != nil {
			return nil, wrapLn(err, rs.line)
		}
		if isReadOnlyTarget(e) {
			return nil, lineErr(rs.line, "%s => 目标 %s 是只读访问器", verb, exprText(e))
		}
		tgt = e
		rest = rest[:arrow]
	}
	p.i++
	args := tokensText(rest)
	return &ActionStmt{baseStmt: baseStmt{Ln: rs.line}, Verb: verb, Args: args, Target: tgt}, nil
}

// parseLoop 解析 `LOOP 变量名=数据源 [concurrency=N]` 或**无参** `LOOP`，直到 END。
// 无参形式 = 无界循环（靠 BREAK/EXIT 退出，见 §5.2），无迭代变量、无 concurrency。
func (p *parser) parseLoop(rs *rawStmt, depth int) (Stmt, error) {
	toks := rs.toks
	i := 1
	// 无参形式：`LOOP` 后无 token（行内 `#` 注释已由 lexer 剥离）
	if i >= len(toks) {
		p.i++
		block, err := p.parseBody(depth + 1)
		if err != nil {
			return nil, err
		}
		return &LoopStmt{baseStmt: baseStmt{Ln: rs.line}, Block: block}, nil
	}
	// 变量名
	if toks[i].kind != tokWord {
		return nil, lineErr(rs.line, "LOOP 需要 '变量名=数据源'（或省略数据源 = 无参 LOOP，一直循环）")
	}
	varName := toks[i].text
	// 无参 LOOP 不支持 concurrency（无数据源，谈不上并发；§5.2）。此处拒绝 `LOOP concurrency=N`，
	// 否则会被误读为「变量名 concurrency = 数据源 N」，在运行期报出难以理解的类型错误。
	if strings.EqualFold(varName, "concurrency") {
		return nil, lineErr(rs.line, "无参 LOOP 不支持 concurrency")
	}
	i++
	if i >= len(toks) || toks[i].kind != tokOp || toks[i].text != "=" {
		return nil, lineErr(rs.line, "LOOP 需要 '变量名=数据源'（或省略数据源 = 无参 LOOP，一直循环）")
	}
	i++
	src, ni, err := parseExpr(toks, i)
	if err != nil {
		return nil, wrapLn(err, rs.line)
	}
	i = ni
	conc := 1
	// 可选 concurrency=N
	for i < len(toks) {
		if toks[i].kind == tokWord && strings.EqualFold(toks[i].text, "concurrency") {
			if i+2 < len(toks) && toks[i+1].kind == tokOp && toks[i+1].text == "=" && toks[i+2].kind == tokNum {
				conc = int(toks[i+2].f)
				if conc < 1 {
					return nil, lineErr(rs.line, "concurrency 必须 >= 1")
				}
				i += 3
				continue
			}
			return nil, lineErr(rs.line, "LOOP concurrency 语法错误")
		}
		return nil, lineErr(rs.line, "LOOP 数据源后有多余内容")
	}
	p.i++
	block, err := p.parseBody(depth + 1)
	if err != nil {
		return nil, err
	}
	return &LoopStmt{baseStmt: baseStmt{Ln: rs.line}, Var: varName, Source: src, Concurrency: conc, Block: block}, nil
}

// findArrow 找顶层（引号/括号外不追踪，token 已无引号）的 => token 下标。
func findArrow(toks []tok) int {
	for i, t := range toks {
		if t.kind == tokOp && t.text == "=>" {
			return i
		}
	}
	return -1
}

// isReadOnlyTarget 目标是否为只读访问器结尾（.content/.lines/.array/.object/.rows/.query()）。
func isReadOnlyTarget(e *Expr) bool {
	if e == nil || len(e.Segs) == 0 {
		return false
	}
	last := e.Segs[len(e.Segs)-1]
	n := strings.ToLower(last.Name)
	if last.Call {
		return n == "query"
	}
	switch n {
	case "content", "lines", "array", "object", "rows", "path", "size", "count", "blocks", "tables":
		return true
	}
	return false
}

// exprText 表达式简要文本（错误提示）。
func exprText(e *Expr) string {
	var b strings.Builder
	switch e.Kind {
	case eLit:
		if s, ok := e.Lit.(string); ok {
			b.WriteString(quote(s))
		} else {
			b.WriteString(valueTextShort(e.Lit))
		}
	case eVar:
		b.WriteString(e.Name)
	case eHandle:
		b.WriteByte(e.Rune)
		b.WriteString(quote(e.Path))
	}
	if e.Idx != nil {
		b.WriteByte('[')
		b.WriteString(exprText(e.Idx))
		b.WriteByte(']')
	}
	for _, s := range e.Segs {
		b.WriteByte('.')
		b.WriteString(s.Name)
		if s.Call {
			b.WriteByte('(')
			for j, a := range s.Args {
				if j > 0 {
					b.WriteString(", ")
				}
				b.WriteString(exprText(a))
			}
			b.WriteByte(')')
		}
	}
	return b.String()
}

func valueTextShort(v any) string {
	s, err := valueToText(v)
	if err != nil {
		return ""
	}
	return s
}
