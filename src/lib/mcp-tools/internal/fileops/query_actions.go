// query_actions.go — fileops 域的只读查询类 DSL 动作（READ / DIFF / FIND）。
//
// 原 file_read / file_diff / file_find 三个独立工具已**收纳**为本域动作：
//   - filesys_run 以裸动词 READ / DIFF / FIND 暴露（与 RPL/APD/... 同域）；
//   - dsl_run 经 dslexec/runner.go 前缀化后为 FILE_READ / FILE_DIFF / FILE_FIND。
//
// 参数语法（Raw 原文透传，动作内自行 {{}} 插值后再解析）：
//
//	READ #"path" [start=N] [limit=N] [tail=N] [ranges=1:10,20:30] [info] [line_numbers]
//	DIFF #"a" #"b" [#"c" #"d" ...]          可多对（偶数个 #"path"，逐对产 diff）
//	FIND #"dir" [depth=N] [glob="*.go"] [grep="re"] [output=tree|file|summary]
//	            [skip_dirs="a,b"] [ignore_files="*.log"] [from=YYYY-MM-DD] [to=YYYY-MM-DD]
//
// 返回语义（Run 返回文本；无 => 目标时进作业汇总）：
//   - READ：**第一行为元数据**（path/md5/encoding/lines/size[ truncated=true][ total_lines=N]），
//     第二行起为文件内容（复用 readOne/readContent 的 start/limit/tail/ranges/line_numbers 语义）；
//     `info` 模式只返回元数据行。
//   - DIFF：统一 diff 文本（复用 renderUnifiedDiff；多对以空行分隔）。
//   - FIND：查找输出文本（复用 find 的 tree/file/summary 渲染）。
//
// 路径一律走 ResolvePath（R-11：绝对 / ~/ / !/；相对路径拒绝）与 agentbox 沙箱（sandboxErr）校验。
package fileops

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// queryVerbs 是只读查询类动作动词（filesys_run 裸动词；dsl_run 前缀化为 FILE_*）。
var queryVerbs = []string{"READ", "DIFF", "FIND"}

// queryVerbSet 便于 O(1) 判定（预校验使用）。
var queryVerbSet = func() map[string]bool {
	m := map[string]bool{}
	for _, v := range queryVerbs {
		m[v] = true
	}
	return m
}()

// queryActions 注册 READ / DIFF / FIND 三个 Raw 动作（参数原文透传）。
func queryActions(ec *editCtx) []dsl.Action {
	acts := make([]dsl.Action, 0, len(queryVerbs))
	for _, v := range queryVerbs {
		verb := v
		acts = append(acts, dsl.Action{
			Name: verb,
			Raw:  true,
			Run: func(sc *dsl.Scope, raw string) (string, error) {
				toks, err := tokenizeQueryArgs(raw)
				if err != nil {
					return "", fmt.Errorf("%s 参数错误：%s", verb, err)
				}
				// 动作内自行插值：引擎不对 Raw 参数做 {{}} 解析。
				for i := range toks {
					if toks[i].kind == qOpt {
						if strings.Contains(toks[i].val, "{{") {
							iv, e := sc.Interp(toks[i].val)
							if e != nil {
								return "", e
							}
							toks[i].val = iv
						}
						continue
					}
					if strings.Contains(toks[i].text, "{{") {
						iv, e := sc.Interp(toks[i].text)
						if e != nil {
							return "", e
						}
						toks[i].text = iv
					}
				}
				return ec.dispatchQuery(verb, toks)
			},
		})
	}
	return acts
}

// dispatchQuery 按动词分发查询动作。
func (ec *editCtx) dispatchQuery(verb string, toks []qToken) (string, error) {
	switch verb {
	case "READ":
		return ec.runReadTokens(toks)
	case "DIFF":
		return ec.runDiffTokens(toks)
	case "FIND":
		return ec.runFindTokens(toks)
	}
	return "", fmt.Errorf("未知查询动作 %s", verb)
}

// ─── READ ─────────────────────────────────────────────

// runReadTokens 解析 READ 动作参数（1 个 #"path" + 选项/标志）并执行。
func (ec *editCtx) runReadTokens(toks []qToken) (string, error) {
	a := splitQueryTokens(toks)
	if len(a.files) != 1 {
		return "", fmt.Errorf("用法：READ #\"路径\" [start=N] [limit=N] [tail=N] [ranges=1:10,20:30] [info] [line_numbers]")
	}
	disp := a.files[0]
	req := readReq{path: disp}
	for k, v := range a.opts {
		switch k {
		case "start":
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return "", fmt.Errorf("start 应为整数，got %q", v)
			}
			req.start = n
		case "limit":
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return "", fmt.Errorf("limit 应为整数，got %q", v)
			}
			req.limit = n
		case "tail":
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return "", fmt.Errorf("tail 应为整数，got %q", v)
			}
			req.tail = n
		case "ranges":
			rs, err := parseRangesOpt(v)
			if err != nil {
				return "", err
			}
			req.ranges = rs
		default:
			return "", fmt.Errorf("READ 未知选项 %q", k)
		}
	}
	for f := range a.flags {
		switch f {
		case "info":
			req.infoOnly = true
		case "line_numbers":
			req.lineNumbers = true
		default:
			return "", fmt.Errorf("READ 未知标志 %q", f)
		}
	}
	return ec.runRead(disp, req)
}

// runRead 读取单文件并返回「元数据行 +（非 info 模式）内容」文本。
// 复用 readOne（含 ResolvePath + agentbox 沙箱 + 截断/tail/ranges 语义）。
func (ec *editCtx) runRead(disp string, req readReq) (string, error) {
	resolved, errMsg := ec.resolve(disp)
	if errMsg != "" {
		return "", fmt.Errorf("%s", errMsg)
	}
	if err := ec.guard(disp, resolved, false); err != nil {
		return "", err
	}
	req.path = disp
	info, err := readOne(req, ec.workDir)
	if err != nil {
		return "", err
	}
	meta := formatReadMeta(disp, info)
	if req.infoOnly {
		return meta, nil
	}
	content, _ := info["content"].(string)
	if content == "" {
		return meta, nil
	}
	return meta + "\n" + content, nil
}

// formatReadMeta 组装 READ 的首行元数据（path / md5 / encoding / lines / size 等）。
func formatReadMeta(disp string, info map[string]interface{}) string {
	md5, _ := info["md5"].(string)
	enc, _ := info["encoding"].(string)
	parts := []string{
		"path=" + disp,
		"md5=" + md5,
		"encoding=" + enc,
		fmt.Sprintf("lines=%v", info["lines"]),
		fmt.Sprintf("size=%v", info["size"]),
	}
	if info["truncated"] == true {
		parts = append(parts, "truncated=true")
	}
	if tl, ok := info["total_lines"]; ok {
		parts = append(parts, fmt.Sprintf("total_lines=%v", tl))
	}
	return strings.Join(parts, " ")
}

// parseRangesOpt 解析 ranges 选项值（如 "1:10,20:30"）为行段列表。
func parseRangesOpt(v string) ([][2]int, error) {
	var out [][2]int
	for _, seg := range strings.Split(v, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		parts := strings.SplitN(seg, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("ranges 段 %q 应为 start:end", seg)
		}
		s, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		e, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("ranges 段 %q 两端应为整数", seg)
		}
		out = append(out, [2]int{s, e})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ranges 不能为空")
	}
	return out, nil
}

// ─── DIFF ─────────────────────────────────────────────

// runDiffTokens 解析 DIFF 动作参数（偶数个 #"path"）并逐对产 diff。
func (ec *editCtx) runDiffTokens(toks []qToken) (string, error) {
	a := splitQueryTokens(toks)
	if len(a.opts) > 0 || len(a.flags) > 0 {
		return "", fmt.Errorf("DIFF 不接受选项/标志")
	}
	if len(a.files) < 2 || len(a.files)%2 != 0 {
		return "", fmt.Errorf("用法：DIFF #\"a\" #\"b\" [#\"c\" #\"d\" ...]（须偶数个 #\"path\"）")
	}
	pairs := make([][2]string, 0, len(a.files)/2)
	for i := 0; i < len(a.files); i += 2 {
		pairs = append(pairs, [2]string{a.files[i], a.files[i+1]})
	}
	return ec.runDiff(pairs)
}

// runDiff 生成若干对文件间的统一 diff 文本（多对以空行分隔）。
func (ec *editCtx) runDiff(pairs [][2]string) (string, error) {
	dmp := diffmatchpatch.New()
	var outputs []string
	for _, pr := range pairs {
		ra, errMsg := ec.resolve(pr[0])
		if errMsg != "" {
			return "", fmt.Errorf("%s", errMsg)
		}
		rb, errMsg := ec.resolve(pr[1])
		if errMsg != "" {
			return "", fmt.Errorf("%s", errMsg)
		}
		if err := ec.guard(pr[0], ra, false); err != nil {
			return "", err
		}
		if err := ec.guard(pr[1], rb, false); err != nil {
			return "", err
		}
		// R7-C-3：diffmatchpatch 为 O(n²)，先按文件尺寸预检，超限直接拒绝（不整读、不 diff）。
		for _, sp := range []struct{ disp, abs string }{{pr[0], ra}, {pr[1], rb}} {
			if fi, serr := os.Stat(sp.abs); serr == nil && fi.Size() > maxDiffBytes {
				return "", fmt.Errorf("%s 超过 diff 上限（%d 字节），请分段比较", sp.disp, maxDiffBytes)
			}
		}
		da, err := os.ReadFile(ra)
		if err != nil {
			return "", fmt.Errorf("read %s: %s", pr[0], err)
		}
		db, err := os.ReadFile(rb)
		if err != nil {
			return "", fmt.Errorf("read %s: %s", pr[1], err)
		}
		diffs := dmp.DiffMain(DecodeText(da), DecodeText(db), false)
		dmp.DiffCleanupSemantic(diffs)
		outputs = append(outputs, renderUnifiedDiff(pr[0], pr[1], diffs))
	}
	return strings.Join(outputs, "\n"), nil
}

// ─── FIND ─────────────────────────────────────────────

// runFindTokens 解析 FIND 动作参数（1 个 #"dir" + 选项）并执行查找。
func (ec *editCtx) runFindTokens(toks []qToken) (string, error) {
	a := splitQueryTokens(toks)
	if len(a.files) != 1 {
		return "", fmt.Errorf("用法：FIND #\"目录\" [depth=N] [glob=\"*.go\"] [grep=\"re\"] [output=tree|file|summary] [skip_dirs=\"a,b\"] [ignore_files=\"*.log\"] [from=YYYY-MM-DD] [to=YYYY-MM-DD]")
	}
	disp := a.files[0]
	o := findOpts{}
	var from, to string
	var skipDirs, ignoreFiles []string
	for k, v := range a.opts {
		switch k {
		case "depth":
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n < 0 {
				return "", fmt.Errorf("depth 应为非负整数，got %q", v)
			}
			o.depth = n
		case "glob":
			o.glob = v
		case "grep":
			o.grep = v
		case "output":
			o.output = v
		case "skip_dirs":
			skipDirs = splitList(v)
		case "ignore_files":
			ignoreFiles = splitList(v)
		case "from":
			from = v
		case "to":
			to = v
		default:
			return "", fmt.Errorf("FIND 未知选项 %q", k)
		}
	}
	for f := range a.flags {
		return "", fmt.Errorf("FIND 未知标志 %q", f)
	}
	period, perr := parsePeriodStrings(from, to)
	if perr != "" {
		return "", fmt.Errorf("%s", perr)
	}
	o.period = period
	o.filter = newWalkFilter(skipDirs, ignoreFiles)
	return ec.runFindQuery(disp, o)
}

// splitList 拆分逗号分隔的列表（去空白、去空项）。
func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ─── 参数 token ───────────────────────────────────────

// qKind token 类型。
type qKind int

const (
	qFile qKind = iota // #"path" 句柄
	qOpt               // key=value 选项
	qFlag              // 裸标志（info / line_numbers）
)

// qToken 是一个查询动作参数 token。
type qToken struct {
	kind qKind
	text string // file 路径 / 裸标志名
	key  string // 选项键
	val  string // 选项值
}

// qArgs 是 token 序列的归类结果。
type qArgs struct {
	files []string
	opts  map[string]string
	flags map[string]bool
}

// splitQueryTokens 把 token 序列归类为文件/选项/标志。
func splitQueryTokens(toks []qToken) qArgs {
	a := qArgs{opts: map[string]string{}, flags: map[string]bool{}}
	for _, t := range toks {
		switch t.kind {
		case qFile:
			a.files = append(a.files, t.text)
		case qOpt:
			a.opts[t.key] = t.val
		case qFlag:
			a.flags[t.text] = true
		}
	}
	return a
}

// tokenizeQueryArgs 把查询动作参数原文拆分为 token 序列。
// 接受三类 token：#「路径」句柄、key=value 选项（值可为整数/裸标识符/带引号或裸串）、裸标志。
// 引号内支持 \" \\ \n \t \r 转义（复用 scanQuote）。
func tokenizeQueryArgs(raw string) ([]qToken, error) {
	var out []qToken
	i, n := 0, len(raw)
	for i < n {
		c := raw[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '#':
			if i+1 >= n || raw[i+1] != '"' {
				return nil, fmt.Errorf("文件句柄格式应为 #\"路径\"")
			}
			val, ni, err := scanQuote(raw, i+1, n)
			if err != nil {
				return nil, err
			}
			out = append(out, qToken{kind: qFile, text: val})
			i = ni
		default:
			start := i
			for i < n && isQueryKeyChar(raw[i]) {
				i++
			}
			if i == start {
				return nil, fmt.Errorf("意外字符 %q（参数须为 #\"路径\" 或 key=value）", string(c))
			}
			key := raw[start:i]
			if i < n && raw[i] == '=' {
				i++
				var val string
				if i < n && raw[i] == '"' {
					v, ni, err := scanQuote(raw, i, n)
					if err != nil {
						return nil, err
					}
					val = v
					i = ni
				} else {
					vs := i
					for i < n && raw[i] != ' ' && raw[i] != '\t' && raw[i] != '\n' && raw[i] != '\r' {
						i++
					}
					if i == vs {
						return nil, fmt.Errorf("选项 %q 缺少值", key)
					}
					val = raw[vs:i]
				}
				out = append(out, qToken{kind: qOpt, key: key, val: val})
			} else {
				out = append(out, qToken{kind: qFlag, text: key})
			}
		}
	}
	return out, nil
}

// isQueryKeyChar 报告字节是否为选项键/裸标志的合法字符。
func isQueryKeyChar(b byte) bool {
	return b == '_' || b == '-' || b == '.' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
