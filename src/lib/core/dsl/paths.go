package dsl

// HandleRef 描述脚本中一处文件句柄引用（`#"路径"`，或 IF exist 的字符串路径字面量），
// 含所在行号与用途描述。供消费方（mcp-tools 的 filesys_run/desktop_run/browser_run）
// 在执行前做路径预校验（R-11：文件/目录引用须绝对或 `~/` 开头）。
type HandleRef struct {
	Line int    // 所在行号
	Desc string // 用途（如 "LOOP 数据源"、"=> 目标"、"IF exist"）
	Path string // 路径模板（可能含 {{}} 插值）
}

// CollectHandleRefs 遍历脚本收集全部文件句柄引用：IF 条件、LOOP 数据源、SET 值/目标、
// TYPEOF/ENTRY/SPLIT/JOIN/PUSH 的值与目标、动作行尾 `=> 目标`、以及访问器参数内的句柄。
// IF exist 的字符串字面量也按文件路径收集（与 evalExist 语义一致）。
//
// 纯只读遍历，**不改变引擎执行行为**：未调用本函数的消费方（如 llm_run）完全不受影响。
func CollectHandleRefs(s *Script) []HandleRef {
	if s == nil {
		return nil
	}
	var refs []HandleRef
	collectStmts(s.Stmts, &refs)
	return refs
}

func collectStmts(sts []Stmt, refs *[]HandleRef) {
	for _, st := range sts {
		switch t := st.(type) {
		case *ActionStmt:
			collectExprRefs(t.Target, "=> 目标", refs)
		case *SetStmt:
			collectExprRefs(t.Value, "SET 值", refs)
			collectExprRefs(t.Target, "SET 目标", refs)
		case *IfStmt:
			collectCondRefs(t.Cond, refs)
			collectStmts(t.Block, refs)
		case *LoopStmt:
			collectExprRefs(t.Source, "LOOP 数据源", refs)
			collectStmts(t.Block, refs)
		case *ParallelStmt:
			collectStmts(t.Block, refs)
		case *TypeofStmt:
			collectExprRefs(t.Value, "TYPEOF 值", refs)
		case *EntryStmt:
			collectExprRefs(t.Value, "ENTRY 值", refs)
		case *SplitStmt:
			collectExprRefs(t.Text, "SPLIT 值", refs)
			collectExprRefs(t.Sep, "SPLIT 值", refs)
		case *JoinStmt:
			collectExprRefs(t.Array, "JOIN 值", refs)
			collectExprRefs(t.Sep, "JOIN 值", refs)
		case *PushStmt:
			collectExprRefs(t.Value, "PUSH 值", refs)
		}
	}
}

// collectCondRefs 递归收集条件中的文件句柄引用；IF exist 的字符串字面量按路径收集。
func collectCondRefs(c Cond, refs *[]HandleRef) {
	switch t := c.(type) {
	case *condAndN:
		for _, it := range t.Items {
			collectCondRefs(it, refs)
		}
	case *condOrN:
		for _, it := range t.Items {
			collectCondRefs(it, refs)
		}
	case *condNotN:
		collectCondRefs(t.Sub, refs)
	case *condExistN:
		if t.Operand != nil {
			if t.Operand.Kind == eLit {
				if s, ok := t.Operand.Lit.(string); ok {
					*refs = append(*refs, HandleRef{Line: t.Ln, Desc: "IF exist", Path: s})
					return
				}
			}
			collectExprRefs(t.Operand, "IF exist", refs)
		}
	case *condCmpN:
		collectExprRefs(t.Left, "IF 条件", refs)
		collectExprRefs(t.Right, "IF 条件", refs)
	case *condPlainN:
		collectExprRefs(t.Operand, "IF 条件", refs)
	}
}

// collectExprRefs 收集单个表达式内的 `#"路径"` 句柄（含下标与访问器参数内的句柄）。
func collectExprRefs(e *Expr, desc string, refs *[]HandleRef) {
	if e == nil {
		return
	}
	if e.Kind == eHandle && e.Rune == '#' {
		*refs = append(*refs, HandleRef{Line: e.Ln, Desc: desc, Path: e.Path})
	}
	collectExprRefs(e.Idx, desc, refs)
	for _, seg := range e.Segs {
		for _, a := range seg.Args {
			collectExprRefs(a, desc, refs)
		}
	}
}
