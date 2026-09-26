package fileops

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"
)

// findItem 是 file_find 的单条目。
type findItem struct {
	rel   string
	isDir bool
	size  int64
	hits  []string // grep 命中行（summary+grep 模式）
}

// HandleFind 实现 file_find 工具（文件查找 DSL）：
//
//	file_find { depth, glob, grep, output }
//	  depth:  0=递归不限；1=当前目录；2=当前+下一级（tree 模式同样受限）
//	  glob:   文件名 glob 通配符（filepath.Match，','/'|' 多模式，支持 **）
//	  grep:   文件内容正则匹配（命中行输出，无命中不列出）
//	  output: tree | file | summary
//	    tree    只输出目录结构（受 depth 限制）
//	    file    只输出匹配文件路径
//	    summary 文件名 + 前/后各最多 200 字符摘录；带 grep 时输出命中行；整体 ≤200 行
func HandleFind(workDir string, args map[string]interface{}) *cli.Result {
	output, _ := args["output"].(string)
	if output == "" {
		output = "file"
	}
	switch output {
	case "tree", "file", "summary":
	default:
		return cli.Err("file_find", fmt.Sprintf("invalid output %q (want tree|file|summary)", output))
	}
	period, perr := parseFindPeriod(args)
	if perr != "" {
		return cli.Err("file_find", perr)
	}

	globPattern, _ := args["glob"].(string)
	grepPattern, _ := args["grep"].(string)
	depth := 0
	if v, ok := args["depth"].(float64); ok && v > 0 {
		depth = int(v)
	}
	subPath, _ := args["path"].(string)

	var re *regexp.Regexp
	if grepPattern != "" {
		var err error
		re, err = regexp.Compile(grepPattern)
		if err != nil {
			return cli.Err("file_find", fmt.Sprintf("invalid grep regex: %s", err))
		}
	}

	filter := ParseWalkFilter(args)

	root := workDir
	if subPath != "" {
		resolved, errMsg := ResolvePath(subPath, workDir)
		if errMsg != "" {
			// 参数级路径违规（R-11）→ 顶层错误
			return cli.Err("file_find", ValidateField("path", subPath))
		}
		root = resolved
	}
	if root == "" {
		root = "."
	}
	// agentbox 沙箱（仅隔离开启时生效）：显式 root 须在允许读目录内；
	// root 缺省 "." 时不预判（下方逐条目过滤，越界条目一律剪枝/跳过，不泄漏）。
	if root != "." {
		if err := sandboxErr(root, false); err != nil {
			return cli.Err("file_find", err.Error())
		}
	}

	// 单条目：rel 相对 root（tree 用）；relFull 相对 workDir（file/summary 用）。
	var items []findItem
	lineBudget := 200 // summary 整体输出行数上限

	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// agentbox 沙箱：递归遍历剪枝——允许目录外的目录/文件一律不列出、不深入
		if !sandboxAllowedRead(path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		// depth 限制：超过层数即剪枝（目录/文件都不再深入）
		if depth > 0 && strings.Count(rel, string(filepath.Separator)) >= depth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			if filter.SkipDir(d.Name()) {
				return filepath.SkipDir
			}
			// tree 模式列出目录；file/summary 不列目录但继续深入
			if output == "tree" {
				items = append(items, findItem{rel: rel, isDir: true})
			}
			return nil
		}

		if filter.IgnoreFile(d.Name()) {
			return nil
		}
		if !globMatch(d.Name(), globPattern) {
			return nil
		}

		fi, ferr := d.Info()
		// period 过滤：文件 mtime 须落在 [from, to]（含边界）；stat 失败无法判定 → 跳过
		if period.active() {
			if ferr != nil || !period.match(fi.ModTime()) {
				return nil
			}
		}
		it := findItem{rel: rel, isDir: false}
		if fi != nil {
			it.size = fi.Size()
		}

		if re != nil {
			data, rerr := os.ReadFile(path)
			if rerr != nil || IsBinaryBytes(data) {
				return nil
			}
			content := DecodeText(data)
			lineNum := 0
			for _, ln := range strings.Split(content, "\n") {
				lineNum++
				if re.MatchString(ln) {
					s := strings.TrimSpace(ln)
					if len(s) > 200 {
						s = s[:200] + "..."
					}
					it.hits = append(it.hits, fmt.Sprintf("%d:%s", lineNum, s))
				}
			}
			if len(it.hits) == 0 {
				return nil // grep 无命中 → 不列出
			}
		}
		items = append(items, it)
		return nil
	})

	switch output {
	case "tree":
		return renderFindTree(root, items)
	case "file":
		var lines []string
		for _, it := range items {
			lines = append(lines, it.rel)
		}
		return findResult("file", lines)
	default: // summary
		var lines []string
		for _, it := range items {
			if len(lines) >= lineBudget {
				break
			}
			lines = append(lines, "▍"+it.rel)
			if re != nil {
				for _, h := range it.hits {
					lines = append(lines, "   "+h)
					if len(lines) >= lineBudget {
						break
					}
				}
				continue
			}
			head, tail := readSummaryHeadTail(filepath.Join(root, filepath.FromSlash(it.rel)))
			if head != "" {
				lines = append(lines, "   "+head)
			}
			if tail != "" {
				lines = append(lines, "   "+tail)
			}
		}
		if len(lines) > lineBudget {
			lines = lines[:lineBudget]
		}
		return findResult("summary", lines)
	}
}

// readSummaryHeadTail 读取文件前/后各最多 200 字符（单行摘录，换行折叠）。
func readSummaryHeadTail(absPath string) (string, string) {
	data, err := os.ReadFile(absPath)
	if err != nil || IsBinaryBytes(data) {
		return "", ""
	}
	content := strings.TrimSpace(DecodeText(data))
	if content == "" {
		return "", ""
	}
	fold := func(s string) string {
		s = strings.Join(strings.Fields(s), " ")
		if len(s) > 200 {
			s = s[:200] + "..."
		}
		return s
	}
	head := fold(content[:minInt(len(content), 200)])
	var tail string
	if len(content) > 400 {
		tail = fold(content[len(content)-200:])
	} else {
		return head, ""
	}
	return head, tail
}

func findResult(mode string, lines []string) *cli.Result {
	if len(lines) == 0 {
		return cli.Ok("file_find", fmt.Sprintf("(%s: no matches)", mode), map[string]interface{}{"mode": mode, "entries": []string{}, "total": 0})
	}
	return cli.Ok("file_find", strings.Join(lines, "\n"), map[string]interface{}{"mode": mode, "entries": lines, "total": len(lines)})
}

// renderFindTree 按路径层级输出目录树（受 depth 已裁剪的 items）。
func renderFindTree(root string, items []findItem) *cli.Result {
	if len(items) == 0 {
		return cli.Ok("file_find", "(tree: empty)", map[string]interface{}{"mode": "tree", "entries": []string{}, "total": 0})
	}
	// 按路径排序保证层级连续
	sortStrings(items)
	var lines []string
	lines = append(lines, ".")
	for _, it := range items {
		segs := strings.Split(it.rel, string(filepath.Separator))
		indent := strings.Repeat("  ", len(segs)-1)
		name := segs[len(segs)-1]
		if it.isDir {
			lines = append(lines, indent+"├── "+name+"/")
		} else {
			lines = append(lines, indent+"├── "+name)
		}
	}
	return cli.Ok("file_find", strings.Join(lines, "\n"), map[string]interface{}{"mode": "tree", "entries": lines, "total": len(items)})
}

func sortStrings(items []findItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].rel < items[j-1].rel; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// findPeriod 是 file_find 的修改时间范围过滤（仅文件 mtime，闭区间含边界）。
type findPeriod struct {
	from    time.Time
	to      time.Time
	hasFrom bool
	hasTo   bool
}

// active 报告是否启用了时间过滤。
func (p findPeriod) active() bool { return p.hasFrom || p.hasTo }

// match 判定 mtime 是否落在 [from, to]（含边界；单端时只约束对应侧）。
func (p findPeriod) match(m time.Time) bool {
	if p.hasFrom && m.Before(p.from) {
		return false
	}
	if p.hasTo && m.After(p.to) {
		return false
	}
	return true
}

// parseFindPeriod 解析 period 参数：{from?, to?}，可只给一端；值 = RFC3339
// 或 YYYY-MM-DD（date-only 的 from = 当日 00:00:00，to = 当日最后一纳秒，即含整天）。
// 返回解析失败消息（空串 = 成功；period 缺省/空对象 = 不过滤）。
func parseFindPeriod(args map[string]interface{}) (findPeriod, string) {
	raw, ok := args["period"].(map[string]interface{})
	if !ok || len(raw) == 0 {
		return findPeriod{}, ""
	}
	parse := func(key string) (time.Time, bool, string) {
		v, has := raw[key]
		if !has || v == nil {
			return time.Time{}, false, ""
		}
		s, strOK := v.(string)
		if !strOK {
			return time.Time{}, false, fmt.Sprintf("period.%s 必须是字符串（RFC3339 或 YYYY-MM-DD）", key)
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t, true, ""
		}
		if t, err := time.Parse("2006-01-02", s); err == nil {
			if key == "to" { // date-only to = 当日整天含
				t = t.Add(24*time.Hour - time.Nanosecond)
			}
			return t, true, ""
		}
		return time.Time{}, false, fmt.Sprintf("period.%s %q 无法解析（需 RFC3339 或 YYYY-MM-DD）", key, s)
	}

	var p findPeriod
	if t, has, em := parse("from"); em != "" {
		return p, em
	} else if has {
		p.from, p.hasFrom = t, true
	}
	if t, has, em := parse("to"); em != "" {
		return p, em
	} else if has {
		p.to, p.hasTo = t, true
	}
	if p.hasFrom && p.hasTo && p.from.After(p.to) {
		return p, "period.from 晚于 period.to（时间范围为空）"
	}
	return p, ""
}
