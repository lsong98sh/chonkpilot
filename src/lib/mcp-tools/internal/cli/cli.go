// Package cli 实现 executor exe 的统一调用契约：
//
//	<exe> <tool> --input=<参数 JSON 路径>   # 执行工具
//	<exe> --help                            # 列示本 exe 包含的工具（读磁盘契约）
//	<exe> --help <tool>                     # 显示指定工具的说明（磁盘契约 md 原文）
//	<exe>                                   # 无入参 = --help
//
// 契约根默认 = `<exeDir>/capability`（executor 位于 `<exeDir>/capability/executors/<cat>.exe`），
// 可用 `--root <dir>` 覆盖。
//
// 输入以 JSON 临时文件传入（大参数不走 argv）。handler 产物（Output/Error）**原样透出到 stdout**，
// 不做 JSON 探测/封装——是否 JSON、如何包装由上层（mcp-server）统一判定：JSON 字符串透传，
// 纯文本封装为 {output,status}。退出码表达成败（0 = 成功；1 = 失败，失败详情在 stdout）。
package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
	"github.com/chonkpilot/chonkpilot-lib/paths"
)

// maxOutputBytes content 模式输出上限（超限写临时文件）。
const maxOutputBytes = 200 * 1024

// Result 是工具执行的中间产物（handler 产出，由 Run 探测封装为最终 JSON）。
type Result struct {
	Success   bool
	Output    string
	Error     string
	Tool      string
	RawResult interface{}
}

// Ok 构造成功结果。
func Ok(tool, output string, raw interface{}) *Result {
	return &Result{Success: true, Output: output, Tool: tool, RawResult: raw}
}

// Err 构造失败结果。
func Err(tool, msg string) *Result {
	return &Result{Success: false, Error: msg, Tool: tool, Output: msg}
}

// Dispatch 是工具执行函数：workDir 为空表示工具不依赖项目目录（如 desktop）。
type Dispatch func(workDir, tool string, args map[string]interface{}) *Result

// Help 是 --help 的数据源：**磁盘契约目录**（executor 不再内嵌契约）。
// 契约根默认 = `<exeDir>/capability`（executor 自身位于 `<exeDir>/capability/executors/<cat>.exe`）；
// 可用 `--root <dir>` 覆盖（用户自建 / 非标准布局）。Dir = 根内分类子目录（如 tools/core）。
type Help struct {
	Root string // 契约根（空 = 由 exe 位置推导）
	Dir  string // 根内分类子目录（如 tools/core）
}

// Run 解析契约参数并执行，最终 JSON 输出到 stdout。返回进程退出码（0 = 成功）。
// help 模式（无入参 / --help / -h / --help <tool>）输出工具清单或工具说明，不走执行路径。
func Run(dispatch Dispatch, help Help) int {
	args := os.Args[1:]
	root := help.Root
	if root == "" {
		root = defaultContractRoot()
	}
	args = extractRoot(args, &root)
	// help 模式（--help <tool> 优先于 --help，避免被单独 --help 分支抢先）
	if len(args) >= 2 && (args[0] == "--help" || args[0] == "-h") {
		return helpShow(root, help.Dir, args[1])
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		return helpList(root, help.Dir)
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: <exe> <tool> --input=<params.json>")
		return 2
	}
	tool := args[0]
	inputPath := ""
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "--input=") {
			inputPath = strings.TrimPrefix(a, "--input=")
		}
	}
	if inputPath == "" {
		fmt.Fprintln(os.Stderr, "missing --input")
		return 2
	}

	raw, err := os.ReadFile(inputPath)
	if err != nil {
		return emit(tool, &Result{Success: false, Error: "read input: " + err.Error(), Tool: tool})
	}
	var args2 map[string]interface{}
	// 容忍 UTF-8 BOM（部分工具链写文件带 BOM）
	raw = []byte(strings.TrimPrefix(string(raw), "\uFEFF"))
	if err := json.Unmarshal(raw, &args2); err != nil {
		return emit(tool, &Result{Success: false, Error: "parse input json: " + err.Error(), Tool: tool})
	}
	if args2 == nil {
		args2 = map[string]interface{}{}
	}

	// 工作目录概念已移除（72-工具开发规范）：工具路径一律全路径，不消费 _work_dir/CHONK_WORK_DIR。
	workDir := ""

	// `!/` 前缀的落地根（R-11 二次升级）：宿主 spawn executor 时注入的子进程环境变量
	// CHONKPILOT_INSTANCE 决定 <系统 temp>/chonkpilot/<instance>/；在 dispatch 前统一设置，
	// 保证**所有工具**的 `!/` 都按同一 instance 目录解析（DSL 工具另有 BuildToolEnv，二者同源且幂等）。
	// 缺 instance 时不设置 → `!/` 由 paths.ResolvePath 报错（instance 为空即异常）。
	if inst := strings.TrimSpace(os.Getenv("CHONKPILOT_INSTANCE")); inst != "" {
		_, _ = paths.SetTempRoot(inst)
	}

	// agentbox 沙箱策略装载（决策 42 §2 (104)/(109)）：宿主 spawn executor 时经
	// CHONKPILOT_SANDBOX 注入「可读 / 可写目录（递归）」策略；未注入 → 不启用（默认兼容）。
	// 必须在 dispatch 之前完成：文件类工具的路径判定读进程级策略。
	if p := agentbox.InitFromEnv(); p != nil {
		fmt.Fprintf(os.Stderr, "[executor] agentbox 已启用：允许目录 %d 条\n", len(p.Rules()))
	}

	start := time.Now()
	res := dispatch(workDir, tool, args2)
	elapsed := time.Since(start)
	if res == nil {
		res = &Result{Success: false, Error: "unknown tool: " + tool, Tool: tool}
	}
	if res.Tool == "" {
		res.Tool = tool
	}
	// 轻量 stderr 日志（由上层 mcp-server/gateway 捕获）；stdout 只承载结果 JSON
	status := "success"
	if !res.Success {
		status = "fail"
	}
	fmt.Fprintf(os.Stderr, "[executor] tool=%s status=%s elapsed=%s\n", tool, status, elapsed)
	return emit(tool, res)
}

// defaultContractRoot 由 exe 位置推导契约根：executor 位于
// `<root>/capability/executors/<cat>.exe` → 契约根 = `<root>/capability`（exeDir 的父目录）。
func defaultContractRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(filepath.Dir(exe))
}

// extractRoot 从命令行参数中摘取 `--root=<dir>` / `--root <dir>`（返回去掉该选项后的参数）。
func extractRoot(args []string, root *string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--root="):
			if v := strings.TrimPrefix(a, "--root="); v != "" {
				*root = v
			}
		case a == "--root":
			if i+1 < len(args) {
				i++
				if v := args[i]; v != "" {
					*root = v
				}
			}
		default:
			out = append(out, a)
		}
	}
	return out
}

// helpList 列出契约根分类目录下的工具（文件名 = 工具名，附 H1 可读标题）。
func helpList(root, dir string) int {
	if root == "" {
		fmt.Fprintln(os.Stderr, "help: 无法解析契约根（请用 --root <dir> 指定）")
		return 2
	}
	fsys := os.DirFS(root)
	names, err := fs.Glob(fsys, path.Join(dir, "*.tool.md"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "help: "+err.Error())
		return 2
	}
	if len(names) == 0 {
		fmt.Println("(no tools)")
		return 0
	}
	sort.Strings(names)
	for _, n := range names {
		name := strings.TrimSuffix(path.Base(n), ".tool.md")
		title := ""
		if data, rerr := fs.ReadFile(fsys, n); rerr == nil {
			title = firstTitle(data)
		}
		if title != "" {
			fmt.Printf("%-20s %s\n", name, title)
		} else {
			fmt.Println(name)
		}
	}
	return 0
}

// helpShow 显示指定工具的契约说明（md 原文；从磁盘读取）。
func helpShow(root, dir, name string) int {
	if strings.ContainsAny(name, `/\`) {
		fmt.Fprintln(os.Stderr, "unknown tool: "+name)
		return 2
	}
	if root == "" {
		fmt.Fprintln(os.Stderr, "help: 无法解析契约根（请用 --root <dir> 指定）")
		return 2
	}
	if !strings.HasSuffix(name, ".tool.md") {
		name += ".tool.md"
	}
	data, err := fs.ReadFile(os.DirFS(root), path.Join(dir, name))
	if err != nil {
		fmt.Fprintln(os.Stderr, "unknown tool: "+name)
		return 2
	}
	fmt.Print(string(data))
	return 0
}

// firstTitle 取首行 `# xxx`（可读标题；无标题返回空）。
func firstTitle(data []byte) string {
	for _, ln := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(t, "# "))
		}
		return ""
	}
	return ""
}

// emit 原样透出 handler 结果到 stdout（executor 不做 JSON 探测/封装）：
//   - handler Output 为 JSON 字符串 → 原样输出（mcp-server 端判定 JSON 后透传）
//   - handler Output 为纯文本 → 原样输出（mcp-server 端判定非 JSON 后封装为 JSON）
//
// 超限保护仅作用于纯文本（JSON 原样透传保持结构，大 JSON 由工具自控分段）。
func emit(_ string, res *Result) int {
	data := res.Output
	if data == "" && res.Error != "" {
		data = res.Error
	}
	if int64(len(data)) > maxOutputBytes && !json.Valid([]byte(data)) {
		// 纯文本超限：写临时文件 + 提示（完整内容由 file_read 提取）
		if of, err := writeTempOutput(data); err == nil {
			data = fmt.Sprintf("输出超过 %d KB，已保存至 %s（用 file_read 读取）", maxOutputBytes/1024, of)
		} else {
			data = data[:maxOutputBytes] + "\n...（输出截断）"
		}
	}
	fmt.Println(data)
	if res.Success {
		return 0
	}
	return 1
}

// writeTempOutput 把完整输出写入系统 temp 临时文件（超限重定向）。
func writeTempOutput(content string) (string, error) {
	f, err := os.CreateTemp("", "ck-out-*.log")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return "", err
	}
	return f.Name(), nil
}
