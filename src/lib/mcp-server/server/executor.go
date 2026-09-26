// 执行链路（25-mcp-server）：tools/call → 按契约 meta 组装命令
// （<runtime>[ <entry>] <args 替换>）→ spawn → 按 output 模式（stdout/code/file）返回结果。
// 结果统一 JSON {output, status, ...} 输出到 stdout（对齐 cli.go 契约）。
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chonkpilot/chonkpilot-lib/winproc"
)

// fileThreshold 是 output=file 模式的内容直接返回阈值（超过返回路径提示）。
const fileThreshold = 200 * 1024

// ─── 统一输出（LLM 面永远收到 JSON 文本）───
// executor stdout 是合法 JSON（对象/数组）→ 原样透传（保留结构化字段，如 file_read 的 files/md5）；
// 否则封装为 {status, output}（文本工具）。失败统一 {status:fail, error}。isError 依退出码。

// isJSONDoc 判断输出是否为 JSON 对象或数组（去首尾空白后首字节）。
func isJSONDoc(out []byte) bool {
	s := strings.TrimLeft(string(out), " \t\r\n")
	if s == "" {
		return false
	}
	return (s[0] == '{' || s[0] == '[') && json.Valid([]byte(s))
}

// wrapSuccess 把文本封装为成功 JSON。
func wrapSuccess(text string) string {
	b, _ := json.Marshal(map[string]any{"status": "success", "output": text})
	return string(b)
}

// wrapFail 构造失败 JSON（error 为 fail 原因）。
func wrapFail(msg string) string {
	b, _ := json.Marshal(map[string]any{"status": "fail", "error": msg})
	return string(b)
}

// callTool 按工具契约执行：组装命令 → spawn → 按 output 模式返回。
// defaults 为注入的默认参数（skip_dirs/fileext/ignore_files），客户端显式参数优先。
// cx 为调用上下文（协议 _meta 透传的 instance/workdir/datadir）：仅用于 spawn executor 时注入
// 子进程环境变量 CHONKPILOT_*（不进 args）。
// 注意：使用独立 context.Background() + 超时，不继承请求 ctx——
// 异步任务模式下 HTTP 响应返回后请求 ctx 会被取消，继承会导致 executor 被杀。
func callTool(_ context.Context, cfg *Config, td *ToolDoc, args map[string]any, defaults map[string]any, cx CallContext) (string, error) {
	if td.Runtime == "" {
		return "", fmt.Errorf("tool %s: runtime required", td.Name)
	}
	// 合并默认参数：客户端已显式传的同名参数优先
	payload := map[string]any{}
	for k, v := range defaults {
		payload[k] = v
	}
	for k, v := range args {
		payload[k] = v
	}

	// 参数 JSON 临时文件（仅模板引用 {RAW-INPUT-FILE} 时生成；大内容走文件，无 argv 限制）
	inPath := ""
	if strings.Contains(td.Args, "{RAW-INPUT-FILE}") {
		f, err := os.CreateTemp("", "ck-call-*.json")
		if err != nil {
			return "", fmt.Errorf("create input tmp: %w", err)
		}
		inPath = f.Name()
		defer os.Remove(inPath)
		if err := json.NewEncoder(f).Encode(payload); err != nil {
			f.Close()
			return "", fmt.Errorf("write input json: %w", err)
		}
		f.Close()
	}

	// 结果输出临时文件（仅 output=file 且模板引用 {RESULT-OUTPUT-FILE}）
	resultFile := ""
	if td.Output == "file" {
		if !strings.Contains(td.Args, "{RESULT-OUTPUT-FILE}") {
			return "", fmt.Errorf("tool %s: output=file requires {RESULT-OUTPUT-FILE} in args", td.Name)
		}
		f, err := os.CreateTemp("", "ck-result-*")
		if err != nil {
			return "", fmt.Errorf("create result tmp: %w", err)
		}
		resultFile = f.Name()
		f.Close()
		defer os.Remove(resultFile)
	}

	// 组装 argv
	argv, err := buildArgv(td, payload, inPath, resultFile)
	if err != nil {
		return "", err
	}

	timeout := resolveExecTimeout(cfg, td)
	ctx2, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx2, argv[0], argv[1:]...)
	// 隐藏子进程控制台窗口（executor 等 console 子系统 exe 被 spawn 时不闪黑窗）；
	// 不改变 stdio 管道，stdout/stderr 捕获逻辑不受影响。
	cmd.SysProcAttr = winproc.SysProcAttr()
	// 子进程环境：CHONKPILOT_*（调用上下文 + 解释器配置）+ CHONK_CHROME（浏览器路径）
	// + CHONKPILOT_SANDBOX（**仅该工具在 tool_sandbox 开关中启用隔离时**注入的 agentbox
	// 策略；未配置 = 不注入 = 默认兼容）。仅本仓 spawn 的内部 executor 进程可见。
	sandboxPolicy := cfg.SandboxPolicyFor(td.Name)
	if sandboxPolicy != "" {
		log.Printf("[mcp-server] agentbox 隔离生效：tool=%s dirs=%s", td.Name, truncate(sandboxPolicy, 500))
	}
	cmd.Env = cfg.executorEnv(cx, sandboxPolicy)
	out, runErr := cmd.Output()

	if ctx2.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("tool %s timeout after %ds", td.Name, timeout)
	}
	if ctx2.Err() == context.Canceled {
		return "", fmt.Errorf("tool %s cancelled", td.Name)
	}

	switch td.Output {
	case "code":
		return handleCode(td, out, runErr)
	case "file":
		return handleFile(td, resultFile)
	default: // stdout
		return handleStdout(td, out, runErr)
	}
}

// resolveExecTimeout 返回 tools/call 的**执行硬上限**（秒）——即子进程被杀的时间点。
// 优先级（用户口径，2026-09-17）：
//  1. usr 键 `tool_async` 的 hard_timeout（**仅用户显式设置时**生效；工具页「执行硬上限」）；
//  2. 契约 meta: timeout（td.Timeout，现状口径）；
//  3. cfg.execTimeout()（prj timeout_sec / 内置默认 300s）。
//
// 注：未配置 hard_timeout 时**完全维持**「契约 timeout 优先于 timeout_sec」的既有语义
// （见 [41 I-79]：prj timeout_sec 仅对未声明 timeout 的契约生效），本函数不改该口径。
func resolveExecTimeout(cfg *Config, td *ToolDoc) int {
	timeout := cfg.execTimeout()
	if td.Timeout > 0 {
		timeout = td.Timeout
	}
	if ov, ok := cfg.toolAsyncOverride(td.Name); ok && ov.HardTimeout > 0 {
		timeout = ov.HardTimeout
	}
	return timeout
}

// buildArgv 按契约组装命令：argv[0] = runtime（解释器拼 entry / exe），后续 = args 模板替换。
func buildArgv(td *ToolDoc, payload map[string]any, inPath, resultFile string) ([]string, error) {
	tokens, err := renderArgs(td.Args, payload, inPath, resultFile)
	if err != nil {
		return nil, fmt.Errorf("tool %s: render args: %w", td.Name, err)
	}
	// runtime 为解释器：<interp>[ <entry>] <tokens...>
	if prefix, ok := interpreterArgv(td.Runtime); ok {
		entry, err := resolveEntry(td)
		if err != nil {
			return nil, err
		}
		argv := append(append([]string{}, prefix...), entry)
		return append(argv, tokens...), nil
	}
	// runtime 为 exe：按契约文件目录相对解析
	exe, err := resolveRuntime(td)
	if err != nil {
		return nil, err
	}
	argv := []string{exe}
	return append(argv, tokens...), nil
}

// interpreterArgv 返回解释器前缀（运行时名 → argv 前缀）；非解释器返回 ok=false（视为 exe）。
func interpreterArgv(runtime string) ([]string, bool) {
	switch runtime {
	case "python", "python3", "node", "bash", "sh", "ruby", "perl":
		return []string{runtime}, true
	case "cmd":
		return []string{"cmd", "/c"}, true
	case "powershell", "pwsh":
		return []string{runtime, "-ExecutionPolicy", "Bypass", "-File"}, true
	case "cscript":
		return []string{"cscript", "//nologo"}, true
	case "java":
		return []string{"java", "-jar"}, true
	}
	return nil, false
}

// resolveEntry 解析 entry 路径（25-mcp-server：绝对 → ~ 展开 → 相对契约文件目录）。
func resolveEntry(td *ToolDoc) (string, error) {
	e := td.Entry
	if e == "" {
		return "", fmt.Errorf("tool %s: entry required for runtime %q", td.Name, td.Runtime)
	}
	if p := resolveAbsOrHome(e); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	p := filepath.Join(td.Dir, e)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("tool %s: entry not found: %s (searched %s)", td.Name, e, p)
}

// resolveRuntime 解析 exe 型 runtime（绝对路径/~ → 相对契约文件目录）。
// 契约与执行器同目录部署（capability/tools/<cat>/ 下 *.tool.md 与 executor 并存）；
// 解释器类 runtime 走 interpreterArgv（PATH），不进入本函数。
func resolveRuntime(td *ToolDoc) (string, error) {
	r := td.Runtime
	if p := resolveAbsOrHome(r); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	p := filepath.Join(td.Dir, r)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("tool %s: runtime not found: %s (relative to contract dir %s)", td.Name, r, td.Dir)
}

// resolveAbsOrHome 绝对路径原样；~ 开头展开用户目录；否则返回空。
func resolveAbsOrHome(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, strings.TrimLeft(strings.TrimPrefix(p, "~"), `/\`))
	}
	return ""
}

// renderArgs 令牌化 args 模板并替换 {param} / {RAW-INPUT-FILE} / {RESULT-OUTPUT-FILE}。
func renderArgs(template string, payload map[string]any, inPath, resultFile string) ([]string, error) {
	var out []string
	for _, tok := range tokenizeArgs(template) {
		out = append(out, renderToken(tok, payload, inPath, resultFile))
	}
	return out, nil
}

// renderToken 替换单令牌内的 {xxx} 占位符。
func renderToken(tok string, payload map[string]any, inPath, resultFile string) string {
	var sb strings.Builder
	for {
		i := strings.IndexByte(tok, '{')
		if i < 0 {
			sb.WriteString(tok)
			break
		}
		sb.WriteString(tok[:i])
		j := strings.IndexByte(tok[i:], '}')
		if j < 0 {
			sb.WriteString(tok[i:])
			break
		}
		name := tok[i+1 : i+j]
		switch name {
		case "RAW-INPUT-FILE":
			sb.WriteString(inPath)
		case "RESULT-OUTPUT-FILE":
			sb.WriteString(resultFile)
		default:
			sb.WriteString(renderParam(payload, name))
		}
		tok = tok[i+j+1:]
	}
	return sb.String()
}

// renderParam 渲染参数值（字符串原样；数字/布尔/对象/数组 JSON 化；缺省空串）。
func renderParam(payload map[string]any, name string) string {
	v, ok := payload[name]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// tokenizeArgs 令牌化命令行模板（支持 `"` 分组，引号剥离）。
func tokenizeArgs(s string) []string {
	var toks []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQuote = !inQuote
		case (c == ' ' || c == '\t') && !inQuote:
			if cur.Len() > 0 {
				toks = append(toks, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		toks = append(toks, cur.String())
	}
	return toks
}

// ─── output 模式 ─────────────────────────

// handleStdout 统一输出处理：JSON（对象/数组）→ 原样透传；纯文本 → 封装 success/fail JSON。
// 返回 (text, err)：err 非 nil 表示执行失败（text 仍是 JSON 文本，供上层置 isError）。
func handleStdout(td *ToolDoc, out []byte, runErr error) (string, error) {
	if isJSONDoc(out) {
		// JSON 原样透传（结构字段保留；成败由退出码决定 isError）
		if runErr != nil {
			return strings.TrimSpace(string(out)), fmt.Errorf("tool %s failed", td.Name)
		}
		return strings.TrimSpace(string(out)), nil
	}
	s := strings.TrimSpace(string(out))
	if runErr != nil {
		msg := s
		if msg == "" {
			msg = fmt.Sprintf("exit code %s", exitCodeStr(runErr))
		}
		return wrapFail(msg), fmt.Errorf("tool %s failed: %s", td.Name, msg)
	}
	if s == "" {
		return wrapSuccess(""), nil
	}
	return wrapSuccess(s), nil
}

// handleCode 退出码判定：0 = 成功（stdout 原文封装 success）；非 0 = 失败（fail JSON）。
func handleCode(td *ToolDoc, out []byte, runErr error) (string, error) {
	if runErr != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = fmt.Sprintf("exit code %s", exitCodeStr(runErr))
		}
		return wrapFail(msg), fmt.Errorf("tool %s failed: %s", td.Name, msg)
	}
	return wrapSuccess(strings.TrimSpace(string(out))), nil
}

// handleFile 读取 {RESULT-OUTPUT-FILE}：≤ 阈值且文本 → 内容封装；否则返回路径提示。
func handleFile(td *ToolDoc, resultFile string) (string, error) {
	data, err := os.ReadFile(resultFile)
	if err != nil {
		msg := fmt.Sprintf("tool %s: read result file: %v", td.Name, err)
		return wrapFail(msg), fmt.Errorf("%s", msg)
	}
	if len(data) <= fileThreshold && utf8.Valid(data) && !bytes.ContainsRune(data, 0) {
		return wrapSuccess(string(data)), nil
	}
	return wrapSuccess(fmt.Sprintf("结果已保存至 %s（%d 字节），用 file_read 提取", resultFile, len(data))), nil
}

// exitCodeStr 取退出码（*exec.ExitError）。
func exitCodeStr(err error) string {
	if ee, ok := err.(*exec.ExitError); ok {
		return strconv.Itoa(ee.ExitCode())
	}
	return err.Error()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "..."
}
