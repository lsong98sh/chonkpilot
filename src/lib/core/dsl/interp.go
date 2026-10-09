package dsl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Action 动作动词（LLM/CLK/OPN...由消费方注入）。
// Raw=true：该动词行参数按**原文**整体捕获（ActionStmt.RawArgs），不再按核心 token 语法
// 解析参数（供坐标/按键等自由文法的执行器动词使用，如 desktop/browser）。
type Action struct {
	Name string
	Raw  bool
	Run  func(s *Scope, args string) (string, error)
}

// Options 执行选项。
type Options struct {
	Files   FileSystem
	DBs     DBSystem
	Actions []Action
	// StopOnError 首个步骤错误即中止（错误已记入 Result.Errors），默认 false（记错继续）。
	StopOnError bool
	// MaxLoopIterations = **无参 LOOP**（无数据源、无界循环）的安全迭代上限；<=0 → 默认 50。
	// 到达上限 → 记错并终止本层循环（不静默停止，见 §5.2）。
	MaxLoopIterations int
	// Vars 宿主注入的**只读**保留变量（引擎启动时预置到根作用域）：
	// 如 Vars["env"] = map[string]any{"CHONKPILOT_WORKDIR": ...} → 脚本用 {{env.CHONKPILOT_WORKDIR}} 引用。
	// 脚本对其它键的赋值不受限（见 Scope.assignVar）。
	Vars map[string]any
	// ReturnFile 是 `$RETURN` 累计超过 ReturnInlineLimit 后的落盘目标路径
	// （宿主注入，通常为 `!/` 临时根下的 dsl-return-<作业id>.md，由 FileSystem 解析）。
	// 空串 = 无落盘目标：超阈值仍留内存（仅供测试/无持久化场景）。
	ReturnFile string
}

// RunResult 执行结果。
type RunResult struct {
	Summary []string // 无 => 目标的动作输出汇总
	Errors  []RunError
}

// defaultMaxLoopIterations 是无参 LOOP（无数据源、无界循环）的缺省安全迭代上限
// （Options.MaxLoopIterations <= 0 时生效；见 §5.2）。
const defaultMaxLoopIterations = 50

// Engine 执行器。
type Engine struct {
	files FileSystem
	dbs   DBSystem
	act   map[string]Action // 动词（大写）→ 动作
	stop  bool              // StopOnError

	loopLimit int // 无参 LOOP 迭代上限（MaxLoopIterations，<=0 时取 defaultMaxLoopIterations）

	// 宿主注入的保留变量：initVars 启动时预置根作用域；injected 标记其名字为只读。
	initVars map[string]any
	injected map[string]bool

	// returnFile 是 `$RETURN` 超阈值落盘目标（Options.ReturnFile）；ret 是累计状态。
	returnFile string
	ret        returnState

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	res    RunResult
}

// Scope 变量作用域（自内向外查找）。
type Scope struct {
	eng    *Engine
	parent *Scope
	vars   map[string]any
	// isolated 标记**并发边界作用域**（PARALLEL 分支 / 并发 LOOP 迭代）：其赋值只写本
	// 作用域、不回写祖先，避免多 goroutine 并发改写共享 map（§6.1「分支捕获互不可见」）。
	isolated bool
}

func newScope(eng *Engine, parent *Scope) *Scope {
	return &Scope{eng: eng, parent: parent, vars: map[string]any{}}
}

// newBranchScope 创建并发分支/迭代的边界作用域（父为共享作用域，赋值不回写祖先）。
func newBranchScope(eng *Engine, parent *Scope) *Scope {
	return &Scope{eng: eng, parent: parent, vars: map[string]any{}, isolated: true}
}

// concurrent 报告本作用域是否位于并发分支内（作用域链上存在并发边界）。
func (s *Scope) concurrent() bool {
	for sc := s; sc != nil; sc = sc.parent {
		if sc.isolated {
			return true
		}
	}
	return false
}

// NewEngine 构造执行器（Files/DBs 缺省注入错误实现，避免 nil 解引用）。
// Options.Vars 预置到根作用域且标记只读（脚本 SET 赋值 → 报错）。
func NewEngine(o Options) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	if o.Files == nil {
		o.Files = nilFS{}
	}
	if o.DBs == nil {
		o.DBs = nilDB{}
	}
	act := map[string]Action{}
	for _, a := range o.Actions {
		act[strings.ToUpper(a.Name)] = a
	}
	eng := &Engine{files: o.Files, dbs: o.DBs, act: act, stop: o.StopOnError, ctx: ctx, cancel: cancel,
		loopLimit: o.MaxLoopIterations, returnFile: o.ReturnFile}
	if eng.loopLimit <= 0 {
		eng.loopLimit = defaultMaxLoopIterations
	}
	if len(o.Vars) > 0 {
		eng.initVars = o.Vars
		eng.injected = make(map[string]bool, len(o.Vars))
		for k := range o.Vars {
			eng.injected[k] = true
		}
	}
	return eng
}

// Cancel 请求终止整个脚本（供宿主在取消时调用；等价 EXIT 的跨 goroutine 终止）。
func (e *Engine) Cancel() { e.cancel() }

// Result 返回执行结果。
func (e *Engine) Result() RunResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.res
}

// Return 返回 `$RETURN` 结果通道的最终值（两态：inline 内容 / file 路径）。
// 脚本未使用 `=> $RETURN` 时 Used=false（宿主据此回落既有汇总逻辑，见 42 §2 (249)⑥）。
func (e *Engine) Return() ReturnValue { return e.ret.snapshot() }

// appendReturn 把一段值累计写入 `$RETURN`（值 → 文本的序列化与 SET 一致：字符串直拼、
// 数字/布尔 → 文本、数组/对象 → JSON 文本）。
func (e *Engine) appendReturn(val any, ln int) error {
	text, err := valueToText(val)
	if err != nil {
		return lineErr(ln, "写入 $RETURN 失败：%s", err.Error())
	}
	if err := e.ret.append(text, e.files, e.returnFile); err != nil {
		return lineErr(ln, "写入 $RETURN 失败：%s", err.Error())
	}
	return nil
}

func (e *Engine) addSummary(s string) {
	e.mu.Lock()
	e.res.Summary = append(e.res.Summary, s)
	e.mu.Unlock()
}

func (e *Engine) addErr(ln int, msg string) {
	e.mu.Lock()
	e.res.Errors = append(e.res.Errors, RunError{Line: ln, Msg: msg})
	e.mu.Unlock()
}

// Execute 执行脚本；EXIT/StopOnError 正常返回，错误已入 Result.Errors。
func (e *Engine) Execute(script *Script) error {
	root := newScope(e, nil)
	// 宿主注入的只读保留变量预置到根作用域（如 env）
	for k, v := range e.initVars {
		root.vars[k] = v
	}
	err := e.execSeq(root, script.Stmts)
	switch {
	case err == nil, errors.Is(err, ErrExit):
		return nil
	case errors.Is(err, ErrBreak), errors.Is(err, ErrContinue):
		e.addErr(0, "BREAK/CONTINUE 必须在 LOOP 块内使用")
		return nil
	default:
		return nil // 已由 execSeq 记入 Result.Errors
	}
}

// Lookup 自内向外查变量。
func (s *Scope) Lookup(name string) (any, bool) {
	for sc := s; sc != nil; sc = sc.parent {
		if v, ok := sc.vars[name]; ok {
			return v, true
		}
	}
	return nil, false
}

// setVar 赋值：就近（找到已定义变量原位更新，否则写当前作用域）。
// 并发边界作用域（isolated）为回写边界：不回写共享祖先，只在本作用域内定义/更新
// （§6.1「分支捕获互不可见」），避免多 goroutine 并发写同一作用域 map。
func (s *Scope) setVar(name string, v any) {
	for sc := s; sc != nil; sc = sc.parent {
		if sc.isolated {
			sc.vars[name] = v
			return
		}
		if _, ok := sc.vars[name]; ok {
			sc.vars[name] = v
			return
		}
	}
	s.vars[name] = v
}

// reservedVars 是**保留标识符**：不得作为变量名/绑定目标（SET/动作 => 目标、TYPEOF/ENTRY/
// SPLIT/JOIN/PUSH 目标、下标写、LOOP 绑定、任何遮蔽）。当前 = `env`（宿主注入的**只读**上下文，
// 唯一受支持用法 = 读取 `{{env.*}}`）+ `$RETURN`（宿主注入的**只写**结果通道，唯一受支持用法 =
// `SET 值 => $RETURN` / `动作 … => $RETURN`，见 return.go）。
var reservedVars = map[string]bool{"env": true, returnVar: true}

// isReservedVar 是否保留标识符（大小写敏感，与变量名规则一致）。
func isReservedVar(name string) bool { return reservedVars[name] }

// errReservedVar 构造保留字错误（统一文案；$RETURN 为只写通道，与只读 env 分别说明）。
func errReservedVar(name string) error {
	if name == returnVar {
		return fmt.Errorf("%s 是宿主注入的只写结果通道，不能作为变量名/绑定目标（唯一用法：SET 值 => $RETURN）", returnVar)
	}
	return fmt.Errorf("%s 是保留字（宿主注入的只读上下文），不能作为变量名", name)
}

// assignVar 是**脚本驱动**的赋值入口（SET/动作 => 目标/TYPEOF/ENTRY/SPLIT/JOIN/PUSH）：
// 保留标识符（env）不可作为绑定目标；宿主经 Options.Vars 注入的保留变量只读，赋值报错。
func (s *Scope) assignVar(name string, v any) error {
	if isReservedVar(name) {
		return errReservedVar(name)
	}
	if s.eng != nil && s.eng.injected[name] {
		return fmt.Errorf("%s 是宿主注入的保留变量，只读", name)
	}
	s.setVar(name, v)
	return nil
}

// Set 向当前作用域写入变量（供动作实现暴露运行态值，如 $X/$WIN）；
// 动作内部写不受只读限制（仅脚本 SET 赋值受限）。
func (s *Scope) Set(name string, v any) { s.setVar(name, v) }

// Interp 插值 {{路径[:N]}}：路径自内向外解析（句柄→描述串），N 截断补 …[截断]。
func (s *Scope) Interp(text string) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	var b strings.Builder
	i := 0
	for i < len(text) {
		idx := strings.Index(text[i:], "{{")
		if idx < 0 {
			b.WriteString(text[i:])
			break
		}
		b.WriteString(text[i : i+idx])
		start := i + idx + 2
		end := strings.Index(text[start:], "}}")
		if end < 0 {
			return "", errors.New("插值 {{ 未闭合")
		}
		expr := text[start : start+end]
		out, err := s.interpOne(expr)
		if err != nil {
			return "", err
		}
		b.WriteString(out)
		i = start + end + 2
	}
	return b.String(), nil
}

func (s *Scope) interpOne(expr string) (string, error) {
	path := expr
	limit := -1
	if k := strings.LastIndex(expr, ":"); k > 0 {
		if n, err := strconv.Atoi(expr[k+1:]); err == nil {
			path = expr[:k]
			limit = n
		}
	}
	parts := strings.Split(path, ".")
	if parts[0] == returnVar {
		return "", errors.New(returnNoRead) // $RETURN 只写不可读
	}
	v, ok := s.Lookup(parts[0])
	if !ok {
		return "", nil // 缺失变量插值为空
	}
	for _, p := range parts[1:] {
		m, ok := asMap(v)
		if !ok {
			return "", nil
		}
		v, ok = m[p]
		if !ok {
			return "", nil
		}
	}
	text, err := interpText(v)
	if err != nil {
		return "", err
	}
	if limit >= 0 && len([]rune(text)) > limit {
		text = string([]rune(text)[:limit]) + "…[截断]"
	}
	return text, nil
}

// ─── 语句执行 ───

// stopErr 标记"已记录 + 停止"的错误（防止逐层重复记入 Result.Errors）。
type stopErr struct{ err error }

func (e *stopErr) Error() string { return e.err.Error() }
func (e *stopErr) Unwrap() error { return e.err }

func (e *Engine) execSeq(sc *Scope, stmts []Stmt) error {
	for _, st := range stmts {
		select {
		case <-e.ctx.Done():
			return ErrExit
		default:
		}
		err := e.execStmt(sc, st)
		if err == nil {
			continue
		}
		switch {
		case errors.Is(err, ErrExit), errors.Is(err, ErrBreak), errors.Is(err, ErrContinue):
			return err
		default:
			if _, ok := err.(*stopErr); ok {
				return err // 内层已记录并停止
			}
			e.addErr(st.Line(), err.Error()) // 失败记错
			if e.stop {
				return &stopErr{err}
			}
		}
	}
	return nil
}

func (e *Engine) execStmt(sc *Scope, st Stmt) error {
	switch t := st.(type) {
	case *ActionStmt:
		return e.execAction(sc, t)
	case *SetStmt:
		return e.execSet(sc, t)
	case *IfStmt:
		ok, err := evalCond(sc, t.Cond)
		if err != nil {
			return err
		}
		if ok {
			return e.execSeq(newScope(e, sc), t.Block)
		}
		return nil
	case *LoopStmt:
		return e.execLoop(sc, t)
	case *ParallelStmt:
		return e.execParallel(sc, t)
	case *BreakStmt:
		return ErrBreak
	case *ContinueStmt:
		return ErrContinue
	case *ExitStmt:
		e.cancel()
		return ErrExit
	case *TypeofStmt:
		return e.execTypeof(sc, t)
	case *EntryStmt:
		return e.execEntry(sc, t)
	case *SplitStmt:
		return e.execSplit(sc, t)
	case *JoinStmt:
		return e.execJoin(sc, t)
	case *PushStmt:
		return e.execPush(sc, t)
	}
	return nil
}

// execAction 动作：执行返回文本；有 => 目标则写目标，无则进汇总。
func (e *Engine) execAction(sc *Scope, st *ActionStmt) error {
	a, ok := e.act[strings.ToUpper(st.Verb)]
	if !ok {
		return &LineError{Line: st.Ln, Msg: "未知动作 " + st.Verb}
	}
	args := st.Args
	if st.RawArgsMode {
		args = st.RawArgs // Raw 动作：参数原文透传（工具动词坐标/按键等自由文法）
	}
	text, err := a.Run(sc, args)
	if err != nil {
		return err
	}
	if st.Target != nil {
		return e.writeTarget(sc, st.Target, text, st.Ln)
	}
	if strings.TrimSpace(text) != "" {
		e.addSummary(text)
	}
	return nil
}

func (e *Engine) execSet(sc *Scope, st *SetStmt) error {
	v, err := evalExprValue(sc, st.Value)
	if err != nil {
		return err
	}
	return e.writeTarget(sc, st.Target, v, st.Ln)
}

// ─── 写入（SET / 动作 => 目标）───

func (e *Engine) writeTarget(sc *Scope, tgt *Expr, val any, ln int) error {
	// $RETURN：宿主注入的只写结果通道 —— 累计追加（超阈值转文件），不走变量赋值。
	// 必须置于下方「保留标识符拒绝」之前（$RETURN 属保留标识符，但写目标是其唯一合法用法）。
	if tgt != nil && tgt.Kind == eVar && tgt.Name == returnVar && len(tgt.Segs) == 0 && tgt.Idx == nil {
		return e.appendReturn(val, ln)
	}
	// 保留标识符（env）不可作为写入目标：裸名/字段链/下标写一律拒绝（唯一合法用法是 {{env.*}} 读取）。
	if tgt.Kind == eVar && isReservedVar(tgt.Name) {
		return lineErr(ln, "%s", errReservedVar(tgt.Name).Error())
	}
	// 下标写：SET 5 => arr[0]
	if tgt.Idx != nil {
		return e.evalSubscriptWrite(sc, tgt, val, ln)
	}
	if len(tgt.Segs) == 0 {
		switch tgt.Kind {
		case eVar:
			if v, ok := sc.Lookup(tgt.Name); ok && isHandleVal(v) {
				return e.handleOverwrite(v, val, ln)
			}
			if err := sc.assignVar(tgt.Name, val); err != nil {
				return lineErr(ln, "%s", err.Error())
			}
			return nil
		case eHandle:
			v, err := evalExprValue(sc, tgt)
			if err != nil {
				return err
			}
			return e.handleOverwrite(v, val, ln)
		default:
			return lineErr(ln, "SET 目标不能是字面量")
		}
	}
	// 带访问器链
	if tgt.Kind == eVar {
		// 宿主注入的保留变量只读：字段链写同样拒绝（与裸名/下标写口径一致）。
		if sc.eng != nil && sc.eng.injected[tgt.Name] {
			return lineErr(ln, "%s 是宿主注入的保留变量，只读", tgt.Name)
		}
		base, ok := sc.Lookup(tgt.Name)
		if !ok {
			return lineErr(ln, "SET 目标变量 %s 未定义", tgt.Name)
		}
		if isHandleVal(base) {
			return e.handleWriteFrom(base, tgt.Segs, val, ln)
		}
		if m, ok := asMap(base); ok {
			fields := make([]string, 0, len(tgt.Segs))
			for _, s := range tgt.Segs {
				if s.Call {
					return lineErr(ln, "SET 目标路径不允许调用")
				}
				fields = append(fields, s.Name)
			}
			if r, ok := base.(*Rec); ok && r.Src != nil {
				// 循环记录字段写：字段更新 + 整源写回在同一把锁内（并发安全）
				return r.Src.setRecord(m, fields, val)
			}
			if sc.concurrent() {
				// 并发分支：共享 map 就地改写有竞态 → 先克隆为分支内副本再写
				cp := cloneValue(m).(map[string]any)
				if err := deepSetFields(cp, fields, val); err != nil {
					return err
				}
				return sc.assignVar(tgt.Name, cp)
			}
			return deepSetFields(m, fields, val)
		}
		return lineErr(ln, "SET 目标 %s 不是可写对象", tgt.Name)
	}
	if tgt.Kind == eHandle {
		base, err := evalHandleBase(sc, tgt)
		if err != nil {
			return err
		}
		return e.handleWriteFrom(base, tgt.Segs, val, ln)
	}
	return lineErr(ln, "SET 目标不合法")
}

// evalHandleBase 只求句柄字面量的基础句柄（不含访问器链）。
func evalHandleBase(sc *Scope, e *Expr) (any, error) {
	p, err := sc.Interp(e.Path)
	if err != nil {
		return nil, lineErr(e.Ln, "%s", err.Error())
	}
	if e.Rune == '#' {
		return &VFile{FS: sc.eng.files, Path: p}, nil
	}
	return &VDB{DB: sc.eng.dbs, Path: p}, nil
}

// handleWriteFrom 对句柄基础值按剩余链写入（.eof 追加 / .range 替换 / 裸覆盖）。
func (e *Engine) handleWriteFrom(base any, segs []Seg, val any, ln int) error {
	last := segs[len(segs)-1]
	if !last.Call && strings.EqualFold(last.Name, "eof") {
		h, err := applyPrefix(base, segs[:len(segs)-1])
		if err != nil {
			return err
		}
		return e.handleAppend(h, val, ln)
	}
	if last.Call && strings.EqualFold(last.Name, "range") {
		h, err := applyPrefix(base, segs[:len(segs)-1])
		if err != nil {
			return err
		}
		args, err := rangeArgs(last, ln)
		if err != nil {
			return err
		}
		return e.handleReplace(h, args[0], args[1], val, ln)
	}
	h, err := applyPrefix(base, segs)
	if err != nil {
		return err
	}
	return e.handleOverwrite(h, val, ln)
}

// applyPrefix 在基础值上应用访问器链（读语义，用于定位写入句柄）。
func applyPrefix(base any, segs []Seg) (any, error) {
	cur := base
	for _, s := range segs {
		v, err := applySeg(cur, s, 0)
		if err != nil {
			return nil, err
		}
		cur = v
	}
	if _, ok := cur.(*dbTablesList); ok {
		// db.tables 单独作写入目标不合法（需要表名）
		return nil, errors.New("db.tables 不是可写目标，需要 db.tables.<表名>")
	}
	return cur, nil
}

func (e *Engine) handleOverwrite(h any, val any, ln int) error {
	switch t := h.(type) {
	case *VFile:
		text, err := valueToText(val)
		if err != nil {
			return err
		}
		return t.handle().WriteAll(text)
	case *VTable:
		return t.Th.WriteRecords(toRecords(val))
	case *Rec:
		return e.handleOverwrite(t.V, val, ln)
	default:
		return lineErr(ln, "该目标不支持覆盖写")
	}
}

func (e *Engine) handleAppend(h any, val any, ln int) error {
	switch t := h.(type) {
	case *VFile:
		text, err := valueToText(val)
		if err != nil {
			return err
		}
		return t.handle().Append(text)
	case *VTable:
		for _, r := range toRecords(val) {
			if err := t.Th.AppendRecord(r); err != nil {
				return err
			}
		}
		return nil
	case *Rec:
		return e.handleAppend(t.V, val, ln)
	default:
		return lineErr(ln, "该目标不支持追加（.eof）")
	}
}

func (e *Engine) handleReplace(h any, n, m int, val any, ln int) error {
	switch t := h.(type) {
	case *VFile:
		return t.handle().ReplaceLines(n, m, toLines(val))
	case *VTable:
		return t.Th.ReplaceRange(n, m, toRecords(val))
	case *Rec:
		return e.handleReplace(t.V, n, m, val, ln)
	default:
	}
	return lineErr(ln, "该目标不支持 range 替换")
}

// evalSubscriptWrite 下标写：SET 5 => arr[0]（纯内存，不回源）。
func (e *Engine) evalSubscriptWrite(sc *Scope, tgt *Expr, val any, ln int) error {
	if tgt.Kind != eVar || len(tgt.Segs) > 0 {
		return lineErr(ln, "SET 下标目标必须是裸变量")
	}
	base, ok := sc.Lookup(tgt.Name)
	if !ok {
		return lineErr(ln, "SET 目标变量 %s 未定义", tgt.Name)
	}
	if isReservedVar(tgt.Name) {
		return lineErr(ln, "%s", errReservedVar(tgt.Name).Error())
	}
	if sc.eng != nil && sc.eng.injected[tgt.Name] {
		return lineErr(ln, "%s 是宿主注入的保留变量，只读", tgt.Name)
	}
	list, ok := base.([]any)
	if !ok {
		return lineErr(ln, "SET 目标 %s 不是列表", tgt.Name)
	}
	// 并发分支：共享 slice 就地写有竞态 → 先克隆为分支内副本，写完回写本作用域
	concurrent := sc.concurrent()
	if concurrent {
		list = cloneValue(list).([]any)
	}
	idxVal, err := evalExprValue(sc, tgt.Idx)
	if err != nil {
		return err
	}
	idx, err := toIntIndex(idxVal, ln)
	if err != nil {
		return err
	}
	if idx < 0 {
		idx += len(list)
	}
	if idx < 0 || idx >= len(list) {
		return lineErr(ln, "下标 %d 越界（列表长度 %d）", idx, len(list))
	}
	list[idx] = val
	if concurrent {
		return sc.assignVar(tgt.Name, list)
	}
	return nil
}

// ─── 新指令执行 ───

func (e *Engine) execTypeof(sc *Scope, st *TypeofStmt) error {
	v, err := evalExprValue(sc, st.Value)
	if err != nil {
		return err
	}
	return sc.assignVar(st.Target.Name, typeName(v))
}

func typeName(v any) string {
	if v == nil {
		return "null"
	}
	switch t := v.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case []any:
		return "list"
	case map[string]any:
		return "object"
	case *VFile:
		return "file"
	case *VDB:
		return "db"
	case *VTable:
		return "table"
	case *Rec:
		return typeName(t.V)
	}
	return "unknown"
}

func (e *Engine) execEntry(sc *Scope, st *EntryStmt) error {
	v, err := evalExprValue(sc, st.Value)
	if err != nil {
		return err
	}
	m, ok := asMap(v)
	if !ok {
		return lineErr(st.Ln, "ENTRY 只能用于对象，得到 %s", typeName(v))
	}
	var entries []any
	for k, val := range m {
		entries = append(entries, map[string]any{"key": k, "value": val})
	}
	return sc.assignVar(st.Target.Name, entries)
}

func (e *Engine) execSplit(sc *Scope, st *SplitStmt) error {
	text, err := evalExprValue(sc, st.Text)
	if err != nil {
		return err
	}
	text = unwrap(text)
	txt, ok := text.(string)
	if !ok {
		return lineErr(st.Ln, "SPLIT 第一个参数必须为字符串，得到 %s", typeName(text))
	}
	sep, err := evalExprValue(sc, st.Sep)
	if err != nil {
		return err
	}
	s, ok := sep.(string)
	if !ok {
		return lineErr(st.Ln, "SPLIT 分隔符必须为字符串，得到 %s", typeName(sep))
	}
	if s == "" {
		return lineErr(st.Ln, "SPLIT 分隔符不能为空")
	}
	parts := strings.Split(txt, s)
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = p
	}
	return sc.assignVar(st.Target.Name, out)
}

func (e *Engine) execJoin(sc *Scope, st *JoinStmt) error {
	v, err := evalExprValue(sc, st.Array)
	if err != nil {
		return err
	}
	v = unwrap(v)
	list, ok := v.([]any)
	if !ok {
		return lineErr(st.Ln, "JOIN 第一个参数必须为列表，得到 %s", typeName(v))
	}
	sep, err := evalExprValue(sc, st.Sep)
	if err != nil {
		return err
	}
	sep = unwrap(sep)
	s, ok := sep.(string)
	if !ok {
		return lineErr(st.Ln, "JOIN 分隔符必须为字符串，得到 %s", typeName(sep))
	}
	parts := make([]string, 0, len(list))
	for _, item := range list {
		item = unwrap(item)
		switch t := item.(type) {
		case string, bool, float64:
			text, err := valueToText(t)
			if err != nil {
				return lineErr(st.Ln, "JOIN 元素序列化失败：%s", err.Error())
			}
			parts = append(parts, text)
		case nil:
			parts = append(parts, "")
		default:
			return lineErr(st.Ln, "JOIN 元素必须为标量（string/number/bool），得到 %s", typeName(item))
		}
	}
	return sc.assignVar(st.Target.Name, strings.Join(parts, s))
}

func (e *Engine) execPush(sc *Scope, st *PushStmt) error {
	v, err := evalExprValue(sc, st.Value)
	if err != nil {
		return err
	}
	v = unwrap(v)
	if isReservedVar(st.Target.Name) {
		return lineErr(st.Ln, "%s", errReservedVar(st.Target.Name).Error())
	}
	base, ok := sc.Lookup(st.Target.Name)
	if !ok {
		return lineErr(st.Ln, "PUSH 目标 %s 未定义（先用 SET [] => %s 初始化）", st.Target.Name, st.Target.Name)
	}
	list, ok := base.([]any)
	if !ok {
		return lineErr(st.Ln, "PUSH 目标 %s 不是列表", st.Target.Name)
	}
	if sc.concurrent() {
		// 并发分支：append 可能就地写共享底层数组 → 先复制为分支内副本
		list = append([]any{}, list...)
	}
	return sc.assignVar(st.Target.Name, append(list, v))
}

func isHandleVal(v any) bool {
	switch t := v.(type) {
	case *VFile, *VDB, *VTable:
		return true
	case *Rec:
		return isHandleVal(t.V)
	}
	return false
}

// toRecords 值 → 表记录列表（文本按 JSON 探测）。
func toRecords(v any) []any {
	v = unwrap(v)
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		return t
	case string:
		j := parseJSONValue(t)
		if l, ok := j.([]any); ok {
			return l
		}
		return []any{j}
	default:
		return []any{v}
	}
}

func toLines(v any) []string {
	v = unwrap(v)
	if l, ok := v.([]any); ok {
		out := make([]string, 0, len(l))
		for _, it := range l {
			s, err := valueToText(it)
			if err == nil {
				out = append(out, s)
			}
		}
		return out
	}
	s, err := valueToText(v)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// deepSetFields 在 map 上按字段路径写入。
func deepSetFields(m map[string]any, fields []string, val any) error {
	if len(fields) == 0 {
		return errors.New("字段路径为空")
	}
	cur := m
	for i := 0; i < len(fields)-1; i++ {
		next, ok := cur[fields[i]].(map[string]any)
		if !ok {
			return errors.New("中间字段 " + fields[i] + " 不是对象")
		}
		cur = next
	}
	cur[fields[len(fields)-1]] = val
	return nil
}

// cloneValue 深拷贝对象/数组（其它值按引用原样保留），供**并发分支**的字段/下标写走
// 「分支内副本」，避免就地改写共享 map/slice（§6.1「分支捕获互不可见」）。
func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = cloneValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = cloneValue(val)
		}
		return out
	}
	return v
}

// Rec 循环记录包装：携带来源，SET 字段写可即时整源写回（幂等断点续跑基础）。
type Rec struct {
	V   any
	Src *iterSource
}

// ─── LOOP ───

// iterSource 迭代来源（支持 SET 字段写回）。
type iterSource struct {
	mu    sync.Mutex
	items []any
	file  *VFile  // 文件源（整写回 json）
	table *VTable // 表源
}

func (s *iterSource) flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushUnlocked()
}

// setRecord 加锁更新一条记录字段并即时整源写回（并发安全）。
func (s *iterSource) setRecord(m map[string]any, fields []string, val any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := deepSetFields(m, fields, val); err != nil {
		return err
	}
	return s.flushUnlocked()
}

func (s *iterSource) flushUnlocked() error {
	if s.file != nil {
		data, err := json.Marshal(unwrapList(s.items))
		if err != nil {
			return err
		}
		return s.file.handle().WriteAll(string(data))
	}
	if s.table != nil {
		return s.table.Th.WriteRecords(unwrapList(s.items))
	}
	return nil
}

func unwrapList(items []any) []any {
	out := make([]any, len(items))
	for i, it := range items {
		out[i] = unwrap(it)
	}
	return out
}

// execLoopUnbounded 执行**无参 LOOP**（无数据源 = 无界循环，见 §5.2）：
// 无迭代变量、无 concurrency；靠 `BREAK`（本层）/ `EXIT`（整脚本）退出，
// `CONTINUE` 进入下一次迭代。**不静默停止** —— 两种非正常终止均记错：
//   - 单步失败 → 终止本层循环（步骤错误已由 execSeq 记入 Result.Errors，此处补一条说明）；
//     （`StopOnError=true` 时 execSeq 已把错误上抛 → 直接返回 → 终止整个脚本。）
//   - 到达迭代上限 loopLimit → 记「超过迭代上限」并终止本层循环。
func (e *Engine) execLoopUnbounded(sc *Scope, st *LoopStmt) error {
	for n := 1; n <= e.loopLimit; n++ {
		select {
		case <-e.ctx.Done():
			return ErrExit
		default:
		}
		errsBefore := e.errLen()
		err := e.execSeq(newScope(e, sc), st.Block)
		switch {
		case errors.Is(err, ErrBreak):
			return nil
		case errors.Is(err, ErrContinue):
			continue
		case errors.Is(err, ErrExit):
			return ErrExit
		case err != nil:
			return err
		}
		if e.errLen() > errsBefore {
			e.addErr(st.Ln, fmt.Sprintf("无参 LOOP 第 %d 次迭代内有步骤失败，终止本层循环", n))
			return nil
		}
	}
	e.addErr(st.Ln, fmt.Sprintf("无参 LOOP 超过迭代上限 %d，已终止（循环未自然退出）", e.loopLimit))
	return nil
}

// errLen 返回当前已记录的错误条数（并发安全）。
func (e *Engine) errLen() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.res.Errors)
}

func (e *Engine) execLoop(sc *Scope, st *LoopStmt) error {
	// 无参 LOOP（无数据源）= 无界循环，走独立路径（§5.2）。
	if st.Source == nil {
		return e.execLoopUnbounded(sc, st)
	}
	// 保留标识符（env）不可作为迭代绑定变量（LOOP env=... 曾可遮蔽宿主注入上下文）。
	if isReservedVar(st.Var) {
		return lineErr(st.Ln, "%s", errReservedVar(st.Var).Error())
	}
	iter, err := e.buildIter(sc, st.Source, st.Ln)
	if err != nil {
		return err
	}
	if iter == nil || len(iter.items) == 0 {
		return nil
	}
	loopScope := newScope(e, sc)
	if st.Concurrency <= 1 {
		for idx := range iter.items {
			select {
			case <-e.ctx.Done():
				return ErrExit
			default:
			}
			it := iter.item(idx)
			itScope := newScope(e, loopScope)
			itScope.vars[st.Var] = it
			err := e.execSeq(itScope, st.Block)
			switch {
			case errors.Is(err, ErrBreak):
				return nil
			case errors.Is(err, ErrContinue):
				continue
			case errors.Is(err, ErrExit):
				return ErrExit
			case err != nil:
				return err // StopOnError（已记入 errors）→ 逐层上抛终止
			}
		}
		return nil
	}
	return e.execLoopConcurrent(st, loopScope, iter)
}

func (e *Engine) execLoopConcurrent(st *LoopStmt, loopScope *Scope, iter *iterSource) error {
	var wg sync.WaitGroup
	var stopMu sync.Mutex
	stopped := false
	canRun := func() bool {
		stopMu.Lock()
		defer stopMu.Unlock()
		return !stopped
	}
	markStop := func() {
		stopMu.Lock()
		stopped = true
		stopMu.Unlock()
	}
	sem := make(chan struct{}, st.Concurrency)
loop:
	for idx := range iter.items {
		if e.ctx.Err() != nil || !canRun() {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-e.ctx.Done():
			markStop()
			break loop // 跳出调度循环，不再 wg.Add/派生无意义 goroutine
		}
		// 二次检查（B-27）：canRun() 通过到拿到信号量之间，已派发迭代可能已 markStop
		// （StopOnError 语义）——不复查会多派发 1-2 个迭代；不通过则归还信号量并停止调度。
		if e.ctx.Err() != nil || !canRun() {
			<-sem
			break loop
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			if e.ctx.Err() != nil {
				return
			}
			itScope := newBranchScope(e, loopScope)
			itScope.vars[st.Var] = iter.item(i)
			err := e.execSeq(itScope, st.Block)
			switch {
			case errors.Is(err, ErrBreak):
				markStop()
			case errors.Is(err, ErrContinue):
				// 并发迭代的 CONTINUE 只跳过本次（goroutine 自然结束），不影响其他迭代
			case errors.Is(err, ErrExit):
				markStop()
			case err != nil:
				markStop() // StopOnError：停止调度其余条目
				// B-29：与顺序 LOOP（L1022 return err 逐层上抛终止）及 PARALLEL（L1230 e.cancel）
				// 语义对齐——StopOnError 命中（err 为 *stopErr，已由 execSeq 记入 Errors）时取消
				// ctx，使循环之后的语句不再执行（cancel 幂等；不重复上抛，避免重记错误）。
				// 非 StopOnError 时 execSeq 已消化错误返回 nil → 不进入本分支，既定「继续执行」不变。
				if _, ok := err.(*stopErr); ok {
					e.cancel()
				}
			}
		}(idx)
	}
	wg.Wait()
	select {
	case <-e.ctx.Done():
		return ErrExit
	default:
	}
	return nil
}

// item 返回第 i 个元素（包 Rec：元素为 map 时 SET 字段可写回源）。
func (s *iterSource) item(i int) any {
	return &Rec{V: s.items[i], Src: s}
}

// buildIter 解析数据源为迭代列表（0 个 = 不迭代）。
func (e *Engine) buildIter(sc *Scope, src *Expr, ln int) (*iterSource, error) {
	v, err := evalExprValue(sc, src)
	if err != nil {
		return nil, err
	}
	// 末尾 .range 的写回禁用（部分切片写回不安全）
	hasRange := len(src.Segs) > 0 && src.Segs[len(src.Segs)-1].Call &&
		strings.EqualFold(src.Segs[len(src.Segs)-1].Name, "range")

	switch t := v.(type) {
	case []any:
		iter := &iterSource{items: t}
		if !hasRange {
			iter = e.attachFileOrigin(sc, src, iter)
		}
		return iter, nil
	case *VFile:
		// 裸文件句柄：解析为 JSON 数组（否则要求显式访问器）
		s, err := t.handle().ReadText()
		if err != nil {
			return nil, lineErr(ln, "读取数据源失败：%s", err.Error())
		}
		var list []any
		if err := json.Unmarshal([]byte(s), &list); err != nil {
			return nil, lineErr(ln, "LOOP 数据源需显式访问器（.lines/.array）或 JSON 数组文件")
		}
		return &iterSource{items: list, file: t}, nil
	case *VTable:
		recs, err := t.Th.Records()
		if err != nil {
			return nil, lineErr(ln, "读取表失败：%s", err.Error())
		}
		return &iterSource{items: recs, table: t}, nil
	case map[string]any:
		// 单对象：一次迭代
		return &iterSource{items: []any{t}}, nil
	case *Rec:
		return e.buildIterFromRec(t, hasRange)
	case nil:
		return &iterSource{items: nil}, nil
	default:
		return nil, lineErr(ln, "LOOP 数据源不支持该类型")
	}
}

// buildIterFromRec 迭代变量/句柄包装的值（继承来源写回）。
func (e *Engine) buildIterFromRec(r *Rec, hasRange bool) (*iterSource, error) {
	switch u := r.V.(type) {
	case []any:
		src := &iterSource{items: u}
		if r.Src != nil {
			src.file = r.Src.file
			src.table = r.Src.table
		}
		return src, nil
	case *VFile:
		s, err := u.handle().ReadText()
		if err != nil {
			return nil, err
		}
		var list []any
		if err := json.Unmarshal([]byte(s), &list); err != nil {
			return nil, errors.New("LOOP 数据源需显式访问器或 JSON 数组文件")
		}
		return &iterSource{items: list, file: u}, nil
	case *VTable:
		recs, err := u.Th.Records()
		if err != nil {
			return nil, err
		}
		return &iterSource{items: recs, table: u}, nil
	case map[string]any:
		return &iterSource{items: []any{u}}, nil
	default:
		return nil, errors.New("LOOP 数据源不支持该类型")
	}
}

func (e *Engine) attachFileOrigin(sc *Scope, src *Expr, iter *iterSource) *iterSource {
	// 形如 #"f".array/.object/.lines（或经变量指向文件句柄）的来源具备文件写回
	var f *VFile
	switch src.Kind {
	case eHandle:
		if src.Rune == '#' {
			p, err := sc.Interp(src.Path)
			if err != nil {
				return iter
			}
			f = &VFile{FS: e.files, Path: p}
		}
	case eVar:
		if v, ok := sc.Lookup(src.Name); ok {
			if vf, ok := v.(*VFile); ok {
				f = vf
			} else if r, ok := v.(*Rec); ok {
				if vf, ok2 := r.V.(*VFile); ok2 {
					f = vf
				}
			}
		}
	}
	if f != nil {
		iter.file = f
	}
	return iter
}

// ─── PARALLEL ───

func (e *Engine) execParallel(sc *Scope, st *ParallelStmt) error {
	if len(st.Block) == 0 {
		return nil
	}
	var wg sync.WaitGroup
	for _, b := range st.Block {
		if e.ctx.Err() != nil {
			break
		}
		branch := b
		wg.Add(1)
		go func() {
			defer wg.Done()
			branchScope := newBranchScope(e, sc) // 分支捕获互不可见
			err := e.execStmt(branchScope, branch)
			switch {
			case errors.Is(err, ErrExit):
				// cancel 已由 EXIT 触发
			case errors.Is(err, ErrBreak), errors.Is(err, ErrContinue):
				e.addErr(branch.Line(), "BREAK/CONTINUE 必须用在 LOOP 块内")
			case err != nil:
				if _, ok := err.(*stopErr); ok {
					e.cancel() // 内层已记录；终止其余分支
					return
				}
				e.addErr(branch.Line(), err.Error())
				if e.stop {
					e.cancel()
				}
			}
		}()
	}
	wg.Wait()
	return nil
}
