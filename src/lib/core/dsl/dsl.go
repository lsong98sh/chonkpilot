package dsl

import (
	"strings"
)

// Parse 解析脚本并校验动词（保留字之外的动词必须存在于 actions）。
func Parse(input string, actions []Action) (*Script, error) {
	rawVerbs := map[string]bool{}
	for _, a := range actions {
		if a.Raw {
			rawVerbs[strings.ToUpper(a.Name)] = true
		}
	}
	script, err := parseWithRaw(input, rawVerbs)
	if err != nil {
		return nil, err
	}
	reg := map[string]bool{}
	for _, a := range actions {
		reg[strings.ToUpper(a.Name)] = true
	}
	var walk func(sts []Stmt) error
	walk = func(sts []Stmt) error {
		for _, st := range sts {
			switch t := st.(type) {
			case *ActionStmt:
				v := strings.ToUpper(t.Verb)
				if reservedVerbs[v] {
					return lineErr(t.Ln, "%s 是保留字，不能作为动作动词", t.Verb)
				}
				if !reg[v] {
					return lineErr(t.Ln, "未知指令 %q（未注册的动作）", t.Verb)
				}
			case *IfStmt:
				if err := walk(t.Block); err != nil {
					return err
				}
			case *LoopStmt:
				if err := walk(t.Block); err != nil {
					return err
				}
			case *ParallelStmt:
				for _, b := range t.Block {
					if err := walk([]Stmt{b}); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := walk(script.Stmts); err != nil {
		return nil, err
	}
	return script, nil
}

// Run 便捷执行：解析 + 执行，返回结果。
func Run(input string, o Options) (RunResult, error) {
	script, err := Parse(input, o.Actions)
	if err != nil {
		return RunResult{}, err
	}
	eng := NewEngine(o)
	if err := eng.Execute(script); err != nil {
		return RunResult{}, err
	}
	return eng.Result(), nil
}

// SplitArgs 解析动作参数串为顶层双引号字符串 token 的**内部文本**（已解码转义）。
// 供动作实现（如 LLM 的 agent/提示词）使用；空白与逗号均为分隔符（兼容
// `LLM "a" "b"` 与 `LLM "a", "b"` 两种写法）；其它非双引号内容（数字/符号）报错。
func SplitArgs(args string) ([]string, error) {
	var out []string
	i := 0
	for i < len(args) {
		c := args[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == ',':
			i++
		case c == '"':
			// 扫描到配对的结束引号
			j := i + 1
			var b strings.Builder
			closed := false
			for j < len(args) {
				ch := args[j]
				if ch == '\\' && j+1 < len(args) {
					nx := args[j+1]
					switch nx {
					case '"', '\\':
						b.WriteByte(nx)
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte('\\')
						b.WriteByte(nx)
					}
					j += 2
					continue
				}
				if ch == '"' {
					closed = true
					j++
					break
				}
				b.WriteByte(ch)
				j++
			}
			if !closed {
				return nil, &LineError{Msg: "动作参数中的字符串未闭合"}
			}
			out = append(out, b.String())
			i = j
		default:
			return nil, &LineError{Msg: "动作参数须为双引号字符串（位置 " + string(c) + "）"}
		}
	}
	return out, nil
}
