// chonkpilot-cli-flash：flash 薄壳的**无窗口宿主**（console 版 flash 客户端；🆕 2026-10-10）。
//
// 与 GUI 版 flash（`chonkpilot-flash.exe`）**同一套打开语义**（复用 `src/lib/gui/flash` 的
// `DirResolver` / `AcquireProject` / `DataDirFor` + 自动拉起 server + `DialClaim`），差别仅在于
// **headless 远程**：无 WebView2、无窗口 —— 连 UDS、claim、发起一轮会话、把结果打印到 stdout。
//
// **零新增 MQ 主题/字段**：命令面与输出格式**逐字对齐** `src/desktop/cli/main.go`
// （`session-start` / `session-send` 发起；`session-receive` / `session-complete` / `task-done` 打印）。
//
// 用法：
//
//	chonkpilot-cli-flash.exe --prompt "帮我查一下" --work-dir .
//	echo "帮我查一下" | chonkpilot-cli-flash.exe --work-dir .
//	chonkpilot-cli-flash.exe --prompt "总结" --output final --work-dir .
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/chonkpilot/chonkpilot-gui/flash"
)

// cliOpts 是 flash-cli 解析后的参数集合（`parseFlags` 产物）。
type cliOpts struct {
	prompt     string
	promptFile string
	scenario   string
	workDir    string
	output     string
	llmModel   string
	think      string
	effort     string
	serverExe  string
}

// parseFlags 注册参数并解析 args（不含程序名）。仅「输出模式非法」在此返回错误；
// flag 语法错误由 fs 的 ErrorHandling 决定（生产 ExitOnError / 测试 ContinueOnError）。
func parseFlags(fs *flag.FlagSet, args []string) (*cliOpts, error) {
	o := &cliOpts{}
	fs.StringVar(&o.prompt, "prompt", "", "提示词（与 --prompt-file 二选一）")
	fs.StringVar(&o.promptFile, "prompt-file", "", "提示词文件路径（与 --prompt 二选一）")
	fs.StringVar(&o.scenario, "scenario", "", "场景目录名（空=默认场景）")
	fs.StringVar(&o.workDir, "work-dir", "", "工作目录；空 = 最近关闭→次近（无则报错，headless 无选目录 UI）")
	fs.StringVar(&o.output, "output", "sse", "输出模式：sse（流式）/ final（仅最终结果）/ verbose（全报文 dump）")
	fs.StringVar(&o.llmModel, "llm", "", "LLM 模型名（默认 server 配置）")
	fs.StringVar(&o.think, "think", "", "思考模式（high / medium / low）")
	fs.StringVar(&o.effort, "effort", "", "思考力度（high / medium / low）")
	fs.StringVar(&o.serverExe, "server-exe", "", "服务端 exe 路径（空 = flash 同目录 / 同级 server/ 推导 chonkpilot-server.exe）")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	switch o.output {
	case flash.OutputSSE, flash.OutputFinal, flash.OutputVerbose:
	default:
		return nil, fmt.Errorf("无效输出模式 %q，可选：sse / final / verbose", o.output)
	}
	return o, nil
}

func main() {
	opts, err := parseFlags(flag.CommandLine, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if code := run(opts); code != 0 {
		os.Exit(code)
	}
}

// run 执行一次 flash-cli 会话；返回进程退出码（0 成功）。
func run(opts *cliOpts) int {
	prompt, err := resolvePrompt(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cwd:", err)
		return 1
	}

	// ── 打开语义（与 GUI flash 共用 flash.DirResolver）──────────────────
	// headless 无选目录 UI（Picker=nil）→ 无显式/无可用最近项目即 ErrNoProject。
	resolver := flash.DirResolver{
		BaseDir: cwd,
		Exists:  flash.DirExists,
		Free:    flash.WorkDirFree,
	}
	workDir, err := resolver.Resolve(opts.workDir, flash.LoadRecents(flash.RecentsPath()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法确定项目目录：%v\n可用 --work-dir 指定，或先由 flash GUI 打开一次项目。\n", err)
		return 1
	}

	// ── 服务端探测 + 自动拉起（与 GUI flash 同一逻辑）─────────────────
	if _, err := (flash.ServerOptions{ServerExe: opts.serverExe}).Ensure(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// ── 项目占用锁（per-user；与 GUI flash 互斥：项目已打开则明确报错）──
	wdLock, busy, lerr := flash.AcquireProject(workDir)
	if busy {
		fmt.Fprintf(os.Stderr, "项目已被本用户打开：%s\n请先关闭对应的 flash 窗口后再运行 flash-cli。\n", workDir)
		return 1
	}
	if lerr != nil {
		fmt.Fprintln(os.Stderr, "warn: workdir lock unavailable:", lerr) // 仅告警：不因锁设施故障阻断
	} else {
		defer wdLock.Release()
	}

	// ── 连接 UDS + claim（本机信任免鉴权）→ 远程总线 ───────────────────
	bus, claim, err := flash.DialClaim(flash.ClaimOptions{WorkDir: workDir})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = bus.Close() }()

	// 打开成功即记「最近项目」（与 GUI flash 一致）。
	if err := flash.SaveRecent(flash.RecentsPath(), workDir); err != nil {
		fmt.Fprintln(os.Stderr, "warn: save recents failed:", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := flash.RunSession(ctx, bus, flash.SessionOptions{
		InstanceID: claim.InstanceID,
		Prompt:     prompt,
		Scenario:   opts.scenario,
		LLM:        opts.llmModel,
		Think:      opts.think,
		Effort:     opts.effort,
		Output:     opts.output,
		Out:        os.Stdout,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// resolvePrompt 取提示词：--prompt / --prompt-file / stdin 三选一（与 CLI 单体同序）。
func resolvePrompt(opts *cliOpts) (string, error) {
	if opts.prompt != "" {
		return opts.prompt, nil
	}
	if opts.promptFile != "" {
		b, err := os.ReadFile(opts.promptFile)
		if err != nil {
			return "", fmt.Errorf("读取提示词文件 %s: %w", opts.promptFile, err)
		}
		return string(b), nil
	}
	stat, statErr := os.Stdin.Stat()
	if statErr != nil || (stat.Mode()&os.ModeCharDevice) != 0 {
		return "", fmt.Errorf("请提供 --prompt 或 --prompt-file，或通过 stdin 传入提示词")
	}
	scanner := bufio.NewScanner(os.Stdin)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("读取 stdin 失败: %w", err)
	}
	return strings.Join(lines, "\n"), nil
}
