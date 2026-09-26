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

// Script 是解析后的脚本。
type Script struct {
	Steps []Step
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

// ParseScript 逐行解析脚本。
// 规则：
//   - 空行与 `#`/`###` 开头为注释，跳过
//   - 指令后无参数（EVL 等），下一行若以 `---` 开头则进入多行块：`---<ID>---` 开始，
//     至相同 `---<ID>---` 完整界定行结束，块体作为 Step.JS
//   - 其余为单行指令：行内 token 化（双引号字符串整体为一个参数，-> 为独立 token）
func ParseScript(text string) (*Script, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var steps []Step
	i := 0
	for i < len(lines) {
		raw := strings.TrimRight(lines[i], "\r")
		trimmed := strings.TrimSpace(raw)
		lineNo := i + 1
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			i++
			continue
		}
		// 提取指令名与参数区
		toks, quoted := tokenize(trimmed)
		if len(toks) == 0 {
			i++
			continue
		}
		cmd := strings.ToUpper(toks[0])
		rest := toks[1:]
		qRest := quoted[1:]
		st := Step{Line: lineNo, Cmd: cmd, Args: rest, Quoted: qRest, Raw: trimmed}

		// 多行块：指令无内联参数，且下一行以 --- 开头
		if len(rest) == 0 && i+1 < len(lines) {
			next := strings.TrimSpace(lines[i+1])
			if strings.HasPrefix(next, "---") {
				openID := parseBlockID(next)
				if openID == "" {
					return nil, stepErr(lineNo, trimmed, "syntax", "块开始标记格式错误，期望 ---<ID>---")
				}
				i += 2
				var body []string
				closed := false
				for i < len(lines) {
					bl := strings.TrimRight(lines[i], "\r")
					if strings.TrimSpace(bl) == "---"+openID+"---" {
						closed = true
						i++
						break
					}
					body = append(body, bl)
					i++
				}
				if !closed {
					return nil, stepErr(lineNo, trimmed, "syntax", "多行块缺少结束标记 ---"+openID+"---")
				}
				st.JS = strings.Join(body, "\n")
			}
		}
		steps = append(steps, st)
		if st.JS == "" {
			i++
		}
	}
	return &Script{Steps: steps}, nil
}

// parseBlockID 解析 `---xxx---` 形式的块界定，返回中间 ID。
func parseBlockID(line string) string {
	line = strings.TrimSpace(line)
	if len(line) < 6 {
		return ""
	}
	if !strings.HasPrefix(line, "---") || !strings.HasSuffix(line, "---") {
		return ""
	}
	id := line[3 : len(line)-3]
	if id == "" {
		return ""
	}
	return id
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
