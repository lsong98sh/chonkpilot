package dsl

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ─── token ───

type tokKind int

const (
	tokWord tokKind = iota // 标识符/关键字/表名（含中文）
	tokStr                 // 双引号字符串（已解码，可含换行；内含 {{}} 插值原样保留）
	tokNum                 // 数字
	tokJSON                // JSON 字面量（数组/对象），val 已解析
	tokSub                 // 下标 [N]：val 为 []tok（下标内容的 token 列表）
	tokOp                  // 符号
)

type tok struct {
	kind tokKind
	text string
	f    float64
	val  any // tokJSON
	line int // 1-based 物理行
}

func (t tok) String() string {
	switch t.kind {
	case tokStr:
		return quote(t.text)
	case tokNum:
		return fmtNumber(t.f)
	case tokJSON:
		s, _ := toJSON(t.val)
		return s
	default:
		return t.text
	}
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tokensText 把语句 token 流重编码为规范文本（仅非 Raw 动作参数使用）。
func tokensText(toks []tok) string {
	var b strings.Builder
	for i, t := range toks {
		s := t.String()
		prevWord := i > 0 && (toks[i-1].kind == tokStr || toks[i-1].kind == tokWord || toks[i-1].kind == tokNum)
		compact := t.kind == tokOp && (s == "." || s == "," || s == "(" || s == ")" || s == ":")
		if i > 0 {
			switch {
			case compact:
				b.WriteString(s)
				continue
			case s == "#" || s == "@":
				b.WriteByte(' ')
				b.WriteString(s)
				continue
			case prevWord || toks[i-1].text == ")":
				b.WriteByte(' ')
			}
		}
		b.WriteString(s)
	}
	return b.String()
}

// rawStmt 一条逻辑语句。
type rawStmt struct {
	toks       []tok
	line       int
	isRaw      bool   // Raw 动作（参数原文，不按核心语法 token 化）
	argsRaw    string // Raw 动作参数原文
	targetRaw  string // Raw 动作行尾 "=> 目标" 原文（无则空）
	targetToks []tok  // 目标表达式 token（targetRaw 词法化）
}

// lineCursor 逐行扫描游标。
type lineCursor struct {
	lines []string
	li    int
	off   int
}

func (lc *lineCursor) lineNo() int { return lc.li + 1 }

// lexToStmts 词法化脚本为语句列表。
// rawVerbs：参数原文透传的动词（Action.Raw=true）；这些动词行剩余部分不再 token 化。
func lexToStmts(input string, rawVerbs map[string]bool) ([]rawStmt, error) {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	lc := &lineCursor{lines: strings.Split(input, "\n")}
	var stmts []rawStmt

	for lc.li < len(lc.lines) {
		trim := strings.TrimSpace(lc.lines[lc.li])
		if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "//") {
			lc.li++
			lc.off = 0
			continue
		}
		toks, line, raw, err := scanStatement(lc, rawVerbs)
		if err != nil {
			return nil, err
		}
		if len(toks) > 0 {
			rs := rawStmt{toks: toks, line: line, isRaw: raw != nil, argsRaw: or(raw, "")}
			if raw != nil {
				// Raw 动作行尾可能带 "=> 目标"（如 `WIN list => #"out.txt"`）：
				// 顶层箭头由 lex 分离，参数原文与目标表达式各自保存。
				if args, target, ok := splitRawTarget(rs.argsRaw); ok {
					rs.argsRaw = args
					rs.targetRaw = target
					tks, err := tokenizeStandalone(target)
					if err != nil {
						return nil, lineErr(line, "Raw 动作 => 目标 %q 词法错误：%s", target, err.Error())
					}
					rs.targetToks = tks
				}
			}
			stmts = append(stmts, rs)
		}
		for lc.li < len(lc.lines) && lc.off >= len(lc.lines[lc.li]) {
			lc.li++
			lc.off = 0
		}
	}
	return stmts, nil
}

func or(s *string, def string) string {
	if s == nil {
		return def
	}
	return *s
}

// splitRawTarget 分离 Raw 动作行尾的 "=> 目标"（如 `WIN list => #"out.txt"`）。
// 双引号字符串与 <<<...>>> 多行体内容不参与箭头匹配。
// 返回参数原文与目标原文；无顶层箭头时 ok=false。
func splitRawTarget(raw string) (args, target string, ok bool) {
	n := len(raw)
	multi := false // <<<...>>> 多行体
	for i := 0; i < n; {
		c := raw[i]
		switch {
		case c == '"':
			i++
			for i < n {
				if raw[i] == '\\' {
					i += 2
					continue
				}
				if raw[i] == '"' {
					i++
					break
				}
				i++
			}
		case !multi && strings.HasPrefix(raw[i:], "<<<"):
			multi = true
			i += 3
		case multi && strings.HasPrefix(raw[i:], ">>>"):
			multi = false
			i += 3
		case !multi && strings.HasPrefix(raw[i:], "=>"):
			args = strings.TrimSpace(raw[:i])
			target = strings.TrimSpace(raw[i+2:])
			if args == "" || target == "" {
				return "", "", false
			}
			return args, target, true
		default:
			i++
		}
	}
	return "", "", false
}

// tokenizeStandalone 对单表达式文本（Raw 动作 => 目标）做词法化。
func tokenizeStandalone(text string) ([]tok, error) {
	lc := &lineCursor{lines: []string{text}, li: 0, off: 0}
	toks, _, _, err := scanStatement(lc, nil)
	return toks, err
}

// scanStatement 扫描一条逻辑语句。非 Raw 动词返回 tokens；Raw 动词返回 verb token + 参数原文。
func scanStatement(lc *lineCursor, rawVerbs map[string]bool) ([]tok, int, *string, error) {
	startLine := lc.lineNo()
	var toks []tok
	for {
		// 行内跳过空白
		for lc.li < len(lc.lines) {
			ln := lc.lines[lc.li]
			for lc.off < len(ln) && (ln[lc.off] == ' ' || ln[lc.off] == '\t') {
				lc.off++
			}
			if lc.off < len(ln) {
				break
			}
			return toks, startLine, nil, nil // 行尾：语句结束
		}
		if lc.li >= len(lc.lines) {
			return toks, startLine, nil, nil
		}
		ln := lc.lines[lc.li]
		c := ln[lc.off]
		cline := lc.lineNo()

		// 行内注释 '#'（不构成 #" 句柄时）→ 语句结束（注释不进参数）
		if c == '#' {
			if lc.off+1 >= len(ln) || ln[lc.off+1] != '"' {
				return toks, startLine, nil, nil
			}
		}
		// Raw 动作动词：动词 token 后整行原文透传
		if len(toks) == 1 && toks[0].kind == tokWord && rawVerbs[strings.ToUpper(toks[0].text)] {
			raw, err := captureRawRest(lc)
			if err != nil {
				return nil, 0, nil, err
			}
			trimmed := strings.TrimSpace(raw)
			return toks, startLine, &trimmed, nil
		}
		// 句柄前缀 #/@（# 后随引号）
		if c == '#' || c == '@' {
			lc.off++
			toks = append(toks, tok{kind: tokOp, text: string(c), line: cline})
			continue
		}
		// JSON 字面量 或 下标
		if c == '[' || c == '{' {
			if c == '[' && len(toks) > 0 {
				last := toks[len(toks)-1]
				isSub := false
				switch last.kind {
				case tokWord:
					// 前一词不是保留字动词 → 下标
					if !isStatementVerb(last.text) {
						isSub = true
					}
				case tokNum, tokStr, tokJSON:
					isSub = true
				case tokOp:
					// ] 或 ) 后 → 下标
					if last.text == "]" || last.text == ")" {
						isSub = true
					}
				}
				if isSub {
					subToks, err := scanSubscript(lc, cline)
					if err != nil {
						return nil, 0, nil, err
					}
					toks = append(toks, tok{kind: tokSub, val: subToks, line: cline})
					continue
				}
			}
			// JSON 字面量
			end, err := scanBalanced(ln, lc.off)
			if err != nil {
				return nil, 0, nil, lineErr(cline, "%s", err.Error())
			}
			var v any
			if err := jsonUnmarshal([]byte(ln[lc.off:end]), &v); err != nil {
				return nil, 0, nil, lineErr(cline, "JSON 字面量解析失败：%s", err.Error())
			}
			toks = append(toks, tok{kind: tokJSON, val: v, line: cline})
			lc.off = end
			continue
		}
		// 数字
		if isDigit(c) || (c == '-' && lc.off+1 < len(ln) && isDigit(ln[lc.off+1])) {
			j := lc.off
			if c == '-' {
				j++
			}
			for j < len(ln) && (isDigit(ln[j]) || ln[j] == '.') {
				j++
			}
			txt := ln[lc.off:j]
			f, ok := parseNumber(txt)
			if !ok {
				return nil, 0, nil, lineErr(cline, "非法数字 %q", txt)
			}
			toks = append(toks, tok{kind: tokNum, f: f, text: txt, line: cline})
			lc.off = j
			continue
		}
		// 标识符
		if isIdentStart(c) {
			j := lc.off + 1
			for j < len(ln) && isIdentPart(ln[j]) {
				j++
			}
			word := ln[lc.off:j]
			lc.off = j
			// Raw 动词：以首词判定后透传原文
			if len(toks) == 0 && rawVerbs[strings.ToUpper(word)] {
				raw, err := captureRawRest(lc)
				if err != nil {
					return nil, 0, nil, err
				}
				toks = append(toks, tok{kind: tokWord, text: word, line: cline})
				trimmed := strings.TrimSpace(raw)
				return toks, startLine, &trimmed, nil
			}
			toks = append(toks, tok{kind: tokWord, text: word, line: cline})
			continue
		}
		// 字符串
		if c == '"' {
			lc.off++
			content, err := readQuoted(lc, cline)
			if err != nil {
				return nil, 0, nil, err
			}
			toks = append(toks, tok{kind: tokStr, text: content, line: cline})
			continue
		}
		// 符号
		if op, n := matchOp(ln[lc.off:]); op != "" {
			toks = append(toks, tok{kind: tokOp, text: op, line: cline})
			lc.off += n
			continue
		}
		return nil, 0, nil, lineErr(cline, "无法识别的字符 %q", string(c))
	}
}

// captureRawRest 捕获 Raw 动作动词之后的行剩余原文（含可跨行的 <<<...>>> 多行体）。
func captureRawRest(lc *lineCursor) (string, error) {
	var b strings.Builder
	for lc.li < len(lc.lines) {
		ln := lc.lines[lc.li]
		for lc.off < len(ln) {
			b.WriteByte(ln[lc.off])
			lc.off++
		}
		text := b.String()
		if quoteOpenRaw(text) && strings.HasSuffix(strings.TrimRight(text, " \t"), "<<<") {
			// 打开的多行体：继续后续物理行，直到行首 >>>
			lc.li++
			lc.off = 0
			found := false
			for lc.li < len(lc.lines) {
				l := lc.lines[lc.li]
				if strings.HasPrefix(strings.TrimSpace(l), ">>>") {
					idx := strings.Index(l, ">>>")
					b.WriteByte('\n')
					b.WriteString(l[idx+3:])
					lc.off = len(l)
					found = true
					break
				}
				b.WriteByte('\n')
				b.WriteString(l)
				lc.li++
				lc.off = 0
			}
			if !found {
				return "", lineErr(lc.lineNo(), "多行文本未以行首 >>> 结束")
			}
		}
		break
	}
	return b.String(), nil
}

// quoteOpenRaw 统计未转义双引号是否成对（处于引号内则 true）。
func quoteOpenRaw(s string) bool {
	inQ := false
	esc := false
	for _, r := range s {
		if esc {
			esc = false
			continue
		}
		if r == '\\' {
			esc = true
			continue
		}
		if r == '"' {
			inQ = !inQ
		}
	}
	return inQ
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '$' || c >= 0x80
}

func isIdentPart(c byte) bool { return isIdentStart(c) || isDigit(c) }

// matchOp 贪婪匹配符号（含工具动作参数字符集：-> % + * /，避免 raw 行在 lex 层报错）。
func matchOp(s string) (string, int) {
	for _, op := range []string{">>=", "=>", "==", "!=", ">=", "<=", "->", ">", "<", "=", ".", "(", ")", ",", ":", "!", "[", "]", "+", "*", "/", "%", "#", "@"} {
		if strings.HasPrefix(s, op) {
			return op, len(op)
		}
	}
	return "", 0
}

// isStatementVerb 判断是否为行首保留字动词（其后 `[` 视为 JSON 数组字面量而非下标）。
func isStatementVerb(word string) bool {
	switch strings.ToUpper(word) {
	case "SET", "IF", "LOOP", "PARALLEL", "BREAK", "CONTINUE", "EXIT", "END",
		"TYPEOF", "ENTRY", "SPLIT", "JOIN", "PUSH":
		return true
	}
	return false
}

// scanSubscript 扫描下标 [N] 内容为 token 流（不做 JSON unmarshal）。
// 调用前 lc.off 指向 `[`，调用后 lc.off 指向 `]` 之后。
func scanSubscript(lc *lineCursor, line int) ([]tok, error) {
	// 跳过 `[`
	lc.off++
	// 收集 [ 和 ] 之间的子 token 流
	depth := 1
	startOff := lc.off
	for lc.li < len(lc.lines) {
		ln := lc.lines[lc.li]
		for lc.off < len(ln) {
			c := ln[lc.off]
			if c == '[' {
				depth++
				lc.off++
				continue
			}
			if c == ']' {
				depth--
				if depth == 0 {
					// 子 token 流
					if lc.off > startOff {
						sub := ln[startOff:lc.off]
						// 用临时 lineCursor 对子文本做 token 化
						lc2 := &lineCursor{lines: []string{sub}, li: 0, off: 0}
						toks, _, _, err := scanStatement(lc2, nil)
						if err != nil {
							return nil, err
						}
						lc.off++ // 跳过 ]
						return toks, nil
					}
					lc.off++ // 跳过 ]
					return nil, nil
				}
				lc.off++
				continue
			}
			if c == '"' {
				lc.off++
				_, err := readQuoted(lc, line)
				if err != nil {
					return nil, err
				}
				continue
			}
			lc.off++
		}
		if depth == 0 {
			break
		}
		lc.li++
		lc.off = 0
	}
	return nil, lineErr(line, "下标未闭合")
}

// scanBalanced 扫描 JSON 字面量到匹配结束。
func scanBalanced(s string, start int) (int, error) {
	open := s[start]
	closeC := byte(']')
	if open == '{' {
		closeC = '}'
	}
	depth := 0
	inQ := false
	esc := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inQ {
			if esc {
				esc = false
				continue
			}
			if ch == '\\' {
				esc = true
				continue
			}
			if ch == '"' {
				inQ = false
			}
			continue
		}
		switch ch {
		case '"':
			inQ = true
		case open:
			depth++
		case closeC:
			depth--
			if depth == 0 {
				return i + 1, nil
			}
		}
	}
	return 0, fmt.Errorf("JSON 字面量未闭合")
}

func jsonUnmarshal(b []byte, v *any) error { return json.Unmarshal(b, v) }

// readQuoted 读取引号内内容：支持转义；行尾 <<< 开始多行（行首 >>> 结束，之后内容继续字符串）。
func readQuoted(lc *lineCursor, line int) (string, error) {
	var b strings.Builder
	for {
		if lc.li >= len(lc.lines) {
			return "", lineErr(line, "字符串未闭合")
		}
		ln := lc.lines[lc.li]
		if lc.off >= len(ln) {
			content := strings.TrimRight(b.String(), " \t")
			if !strings.HasSuffix(content, "<<<") {
				return "", lineErr(lc.lineNo(), "字符串未闭合（多行文本须以行末 <<< 开始）")
			}
			b.Reset()
			b.WriteString(strings.TrimRight(strings.TrimSuffix(content, "<<<"), " \t"))
			lc.li++
			lc.off = 0
			found := false
			for lc.li < len(lc.lines) {
				t := strings.TrimSpace(lc.lines[lc.li])
				if strings.HasPrefix(t, ">>>") {
					idx := strings.Index(lc.lines[lc.li], ">>>")
					rawRest := lc.lines[lc.li][idx+3:]
					lead := len(rawRest) - len(strings.TrimLeft(rawRest, " "))
					b.WriteByte('\n')
					lc.off = idx + 3 + lead
					found = true
					break
				}
				b.WriteByte('\n')
				b.WriteString(lc.lines[lc.li])
				lc.li++
				lc.off = 0
			}
			if !found {
				return "", lineErr(line, "多行文本未以行首 >>> 结束")
			}
			continue
		}
		ch := ln[lc.off]
		lc.off++
		if ch == '\\' && lc.off < len(ln) {
			nx := ln[lc.off]
			lc.off++
			switch nx {
			case '"', '\\':
				b.WriteByte(nx)
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte('\\')
				b.WriteByte(nx)
			}
			continue
		}
		if ch == '"' {
			return b.String(), nil
		}
		b.WriteByte(ch)
	}
}
