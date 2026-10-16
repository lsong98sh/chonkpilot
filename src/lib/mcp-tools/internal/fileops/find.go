package fileops

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// maxHitsPerFile 是单文件 grep 命中行的封顶条数（防逐行无界 append，R7-C-2）。
const maxHitsPerFile = 200

// findItem 是 FIND 的单条目。
type findItem struct {
	rel   string
	isDir bool
	size  int64
	hits  []string // grep 命中行（summary+grep 模式）
}

// findOpts 是 FIND 查询动作的解析后参数。
type findOpts struct {
	depth  int
	glob   string
	grep   string
	output string // tree | file | summary
	period findPeriod
	filter *WalkFilter
}

// runFindQuery 在 disp 指定目录下查找并返回渲染文本（tree/file/summary）。
//
//	FIND #"dir" [depth=N] [glob="*.go"] [grep="re"] [output=tree|file|summary]
//	           [skip_dirs="a,b"] [ignore_files="*.log"] [from=YYYY-MM-DD] [to=YYYY-MM-DD]
//	  depth:  0=递归不限；1=当前目录；2=当前+下一级（tree 模式同样受限）
//	  glob:   文件名 glob 通配符（filepath.Match，','/'|' 多模式，支持 **）
//	  grep:   文件内容正则匹配（命中行输出，无命中不列出）
//	  output: tree=目录结构 / file=匹配文件路径 / summary=文件名+前/后各最多 200 字符摘录（≤200 行）
//
// 复用原 file_find 的遍历与渲染逻辑；路径解析（ResolvePath）与 agentbox 沙箱校验在此完成。
func (ec *editCtx) runFindQuery(disp string, o findOpts) (string, error) {
	if o.output == "" {
		o.output = "file"
	}
	switch o.output {
	case "tree", "file", "summary":
	default:
		return "", fmt.Errorf("invalid output %q (want tree|file|summary)", o.output)
	}
	var re *regexp.Regexp
	if o.grep != "" {
		var err error
		re, err = regexp.Compile(o.grep)
		if err != nil {
			return "", fmt.Errorf("invalid grep regex: %s", err)
		}
	}
	root, errMsg := ec.resolve(disp)
	if errMsg != "" {
		return "", fmt.Errorf("%s", errMsg)
	}
	if err := ec.guard(disp, root, false); err != nil {
		return "", err
	}
	filter := o.filter
	if filter == nil {
		filter = newWalkFilter(nil, nil)
	}

	// 单条目：rel 相对 root（tree 用）。
	var items []findItem
	lineBudget := 200 // summary 整体输出行数上限

	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// agentbox 沙箱：root 已在遍历前判定允许读；递归包含语义下其**真实子路径**（普通文件 /
		// 真实目录）必然允许，故只对**非普通类型**条目（符号链接 / Windows 目录联接 junction /
		// 设备等——junction 是 reparse point，Go 归为非普通类型而非 ModeDir）逐项解析判定，防链接
		// 指向允许目录外越界读；避免对每个普通条目 EvalSymlinks（大目录性能）。WalkDir 不跟随链接下钻。
		if t := d.Type(); t != 0 && t != fs.ModeDir && !sandboxAllowedRead(path) {
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
		if o.depth > 0 && strings.Count(rel, string(filepath.Separator)) >= o.depth {
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
			if o.output == "tree" {
				items = append(items, findItem{rel: rel, isDir: true})
			}
			return nil
		}

		if filter.IgnoreFile(d.Name()) {
			return nil
		}
		if !globMatch(d.Name(), o.glob) {
			return nil
		}

		fi, ferr := d.Info()
		// period 过滤：文件 mtime 须落在 [from, to]（含边界）；stat 失败无法判定 → 跳过
		if o.period.active() {
			if ferr != nil || !o.period.match(fi.ModTime()) {
				return nil
			}
		}
		it := findItem{rel: rel, isDir: false}
		if fi != nil {
			it.size = fi.Size()
		}

		if re != nil {
			// R7-C-2：整读改上限截断（maxReadBytes），避免超大文件整读入内存。
			data, rerr := readFileCapped(path)
			if rerr != nil || IsBinaryBytes(data) {
				return nil
			}
			content := DecodeText(data)
			lineNum := 0
			for _, ln := range strings.Split(content, "\n") {
				lineNum++
				if re.MatchString(ln) {
					s := strings.TrimSpace(ln)
					if ts, trunc := truncRunes(s, 200); trunc {
						s = ts
					}
					it.hits = append(it.hits, fmt.Sprintf("%d:%s", lineNum, s))
					// R7-C-2：单文件命中条数封顶，防逐行无界 append。
					if len(it.hits) >= maxHitsPerFile {
						break
					}
				}
			}
			if len(it.hits) == 0 {
				return nil // grep 无命中 → 不列出
			}
		}
		items = append(items, it)
		return nil
	})

	switch o.output {
	case "tree":
		return renderFindTree(items), nil
	case "file":
		var lines []string
		for _, it := range items {
			lines = append(lines, it.rel)
		}
		return renderFindFlat("file", lines), nil
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
		return renderFindFlat("summary", lines), nil
	}
}

// readSummaryHeadTail 读取文件前/后各最多 200 字符（单行摘录，换行折叠）。
// 按 rune 切片，避免按字节截断多字节字符（中文等）产生乱码（R7-C-8）。
func readSummaryHeadTail(absPath string) (string, string) {
	data, err := readFileCapped(absPath)
	if err != nil || IsBinaryBytes(data) {
		return "", ""
	}
	content := strings.TrimSpace(DecodeText(data))
	if content == "" {
		return "", ""
	}
	fold := func(s string) string {
		s = strings.Join(strings.Fields(s), " ")
		if ts, trunc := truncRunes(s, 200); trunc {
			return ts
		}
		return s
	}
	r := []rune(content)
	headN := 200
	if len(r) < headN {
		headN = len(r)
	}
	head := fold(string(r[:headN]))
	if len(r) > 400 {
		return head, fold(string(r[len(r)-200:]))
	}
	return head, ""
}

// readFileCapped 读取文件内容，最多 maxReadBytes 字节（防超大文件整读入内存，R7-C-2）。
func readFileCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxReadBytes))
}

// truncRunes 按 rune 将 s 截断到最多 n 个字符并补 "..."；未超长返回原串与 false。
// 避免按字节截断多字节字符（中文等）产生乱码（R7-C-8）。
func truncRunes(s string, n int) (string, bool) {
	r := []rune(s)
	if len(r) <= n {
		return s, false
	}
	return string(r[:n]) + "...", true
}

// renderFindFlat 渲染 file/summary 模式的平铺输出（无匹配给 no matches 文案）。
func renderFindFlat(mode string, lines []string) string {
	if len(lines) == 0 {
		return fmt.Sprintf("(%s: no matches)", mode)
	}
	return strings.Join(lines, "\n")
}

// renderFindTree 按路径层级输出目录树（受 depth 已裁剪的 items）。
func renderFindTree(items []findItem) string {
	if len(items) == 0 {
		return "(tree: empty)"
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
	return strings.Join(lines, "\n")
}

func sortStrings(items []findItem) {
	sort.Slice(items, func(i, j int) bool { return items[i].rel < items[j].rel })
}

// findPeriod 是 FIND 的修改时间范围过滤（仅文件 mtime，闭区间含边界）。
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

// parsePeriodStrings 解析 from/to 选项（可只给一端；值 = RFC3339 或 YYYY-MM-DD，
// date-only 的 from = 当日 00:00:00，to = 当日最后一纳秒，即含整天）。返回解析失败消息
// （空串 = 成功；两端均空 = 不过滤）。
func parsePeriodStrings(from, to string) (findPeriod, string) {
	var p findPeriod
	if from != "" {
		t, em := parsePeriodValue("from", from)
		if em != "" {
			return p, em
		}
		p.from, p.hasFrom = t, true
	}
	if to != "" {
		t, em := parsePeriodValue("to", to)
		if em != "" {
			return p, em
		}
		p.to, p.hasTo = t, true
	}
	if p.hasFrom && p.hasTo && p.from.After(p.to) {
		return p, "period.from 晚于 period.to（时间范围为空）"
	}
	return p, ""
}

// parsePeriodValue 解析单个时间值（RFC3339 或 YYYY-MM-DD）；失败返回错误消息。
func parsePeriodValue(key, s string) (time.Time, string) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, ""
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		if key == "to" { // date-only to = 当日整天含
			t = t.Add(24*time.Hour - time.Nanosecond)
		}
		return t, ""
	}
	return time.Time{}, fmt.Sprintf("period.%s %q 无法解析（需 RFC3339 或 YYYY-MM-DD）", key, s)
}
