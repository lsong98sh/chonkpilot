// Package browser 实现 browser_run 编排 DSL：解析 + 执行（chromedp）。
package browser

import (
	"fmt"
	"strings"
)

// Step 是一条已解析的 DSL 指令。
type Step struct {
	Line   int      // 原始行号（1 起）
	Cmd    string   // 大写指令名
	Args   []string // 参数（双引号已解）
	Quoted []bool   // 与 Args 对齐：该参数是否被双引号包裹（自定义错误消息等需识别）
	Raw    string   // 原文（报错定位）
	JS     string   // 多行块内容（EVL 等指令独立行 + ---ID--- 块）
}

// StepError 定位到指令的错误（fail-fast 统一返回）。
type StepError struct {
	Line int
	Raw  string
	Cat  string // syntax | not_found | timeout | js | unsupported ...
	Msg  string
}

func (e *StepError) Error() string {
	return fmt.Sprintf("第 %d 行 %s: %s - %s", e.Line, e.Raw, e.Cat, e.Msg)
}

func stepErr(line int, raw, cat, msg string) *StepError {
	return &StepError{Line: line, Raw: raw, Cat: cat, Msg: msg}
}

// tokenize 把一行拆成 token：双引号字符串整体保留（可含空格/转义 \"），
// 其余按空白分割，`->` 单独成 token。返回 token 与"是否整体被双引号包裹"标记。
func tokenize(line string) ([]string, []bool) {
	var toks []string
	var quotedFlags []bool
	var cur strings.Builder
	inQuote := false
	tokQuoted := false
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			quotedFlags = append(quotedFlags, tokQuoted)
			cur.Reset()
			tokQuoted = false
		}
	}
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case inQuote:
			if c == '\\' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
				cur.WriteRune(runes[i+1])
				i++
			} else if c == '"' {
				inQuote = false
			} else {
				cur.WriteRune(c)
			}
		case c == '"':
			inQuote = true
			tokQuoted = true
		case c == '-' && i+1 < len(runes) && runes[i+1] == '>':
			flush()
			toks = append(toks, "->")
			quotedFlags = append(quotedFlags, false)
			i++
		case c == ' ' || c == '\t':
			flush()
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return toks, quotedFlags
}
