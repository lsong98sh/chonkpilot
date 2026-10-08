// runner.go — 作业执行：读首行 run → 建引擎执行 → 写 result。
//
// 统一方言：文件域 FILE_*、浏览器域 WEB_*、桌面域 PC_*（域前缀消歧三域重名动词），LLM 保持。
// 统一校验型 Files：引擎 Files 取 fileops 会话的 Files()（已接入 agentbox 沙箱）。
// $RETURN 接线（DSL-2 宿主侧）：return_file 注入 dsl.Options.ReturnFile；作业结束取
// eng.Return()，Used 时按 inline/file 两态回填，未 Used 时回落汇总。
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/paths"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/browser"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/desktop"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/fileops"
)

// maxLineBytes 是单条协议行上限（脚本可能较大；默认 Scanner 64K 不足以容纳大 DSL）。
const maxLineBytes = 16 << 20

// runProtocol 执行一作业（一作业一进程）：读首行 run → 建引擎执行 → 写 result。
// 读取首行失败（无输入/坏 JSON）返回 error（main 记 stderr 并退出）；run 之后的作业级失败
// （建会话/解析/执行）一律以 result{ok:false} 回填，函数正常返回。
func runProtocol(in io.Reader, out io.Writer) error {
	lw := newLineWriter(out)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)

	// 首行必为 run。
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return fmt.Errorf("读取 run 消息失败：%w", err)
		}
		return errors.New("未收到 run 消息（stdin 首行为空）")
	}
	var run inMessage
	if err := json.Unmarshal(sc.Bytes(), &run); err != nil {
		return fmt.Errorf("解析 run 消息失败：%w", err)
	}
	if run.T != "run" {
		return fmt.Errorf("首条消息类型应为 run，实际为 %q", run.T)
	}

	// instance → 临时根（!/ 前缀落地目录，按 instance 分桶）。
	if _, err := paths.SetTempRoot(run.Instance); err != nil {
		lw.sendResult(resultMsg{Job: run.Job, Error: err.Error()})
		return nil
	}

	// stdin 读协程：按 call 派发后续 llm_result。
	hub := newLLMHub(run.Job, lw)
	go hub.readLoop(sc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lw.sendResult(executeJob(ctx, run, hub, lw))
	return nil
}

// domainSession 是四域会话的统一门面（fileops/browser/desktop 均满足）。
type domainSession interface {
	Actions() []dsl.Action
	Files() dsl.FileSystem
	Summary() []string
	Close()
}

// executeJob 建四域会话 → 合并（前缀化）动作 → 解析执行 → 回填结果。
func executeJob(ctx context.Context, run inMessage, hub *llmHub, lw *lineWriter) resultMsg {
	res := resultMsg{Job: run.Job, Error: ""}

	fSess, err := fileops.NewSession(run.WorkDir, nil)
	if err != nil {
		res.Error = "初始化文件会话失败：" + err.Error()
		return res
	}
	wSess, err := browser.NewSession(browser.Options{})
	if err != nil {
		fSess.Close()
		res.Error = "初始化浏览器会话失败：" + err.Error()
		return res
	}
	pSess, err := desktop.NewSession()
	if err != nil {
		fSess.Close()
		wSess.Close()
		res.Error = "初始化桌面会话失败：" + err.Error()
		return res
	}
	defer fSess.Close()
	defer wSess.Close()
	defer pSess.Close()
	wSess.SetParent(ctx) // 作业级取消传播到浏览器生命周期

	actions := mergeActions(fSess, wSess, pSess)
	tree := newJobTree(run.Job, lw) // DSL 展示：静态容器树 + 步骤进度（预走 AST 前先建）
	actions = append(actions, llmAction(ctx, hub, tree))

	ast, err := dsl.Parse(run.Script, actions)
	if err != nil {
		res.Error = "解析脚本失败：" + err.Error()
		return res
	}

	// DSL-3：按 AST 预建静态容器树并发 tree（容器各建一次，不随迭代增长）。
	tree.buildStaticTree(ast.Stmts)
	tree.emitTree()

	tempDir, _ := paths.TempRootFor(run.Instance)
	exeDir := ""
	if d, derr := exedir.Dir(); derr == nil {
		exeDir = d
	}
	eng := dsl.NewEngine(dsl.Options{
		Files:      fSess.Files(), // 统一校验型 Files（含 agentbox 沙箱）
		Actions:    actions,
		Vars:       envVars(run.WorkDir, run.DataDir, tempDir, exeDir),
		ReturnFile: run.ReturnFile,
	})
	_ = eng.Execute(ast)

	rr := eng.Result()
	rv := eng.Return()
	// ReturnUsed：脚本是否使用 `$RETURN`（false → text/file 为回落汇总，gateway 不写 return_*）。
	res.ReturnUsed = rv.Used
	switch {
	case rv.Used && rv.Overflow:
		// file 态：回文件名 + 大小（内容已流式落盘）。
		res.File, res.Size, res.Overflow = rv.File, rv.Size, true
	case rv.Used:
		// inline 态：直接回内容。
		res.Text = rv.Text
	default:
		// 未使用 $RETURN：回落汇总（各域 Summary + 动作输出汇总 + DSL 运行时错误）。
		res.Text = fallbackSummary(rr, fSess, wSess, pSess)
	}
	if len(rr.Errors) > 0 {
		res.Error = joinErrors(rr.Errors)
		return res
	}
	res.OK = true
	return res
}

// mergeActions 汇总三域动作并把动词改为「域前缀_原动词」（LLM 单独追加）。
func mergeActions(sessions ...domainSession) []dsl.Action {
	prefixes := []string{"FILE_", "WEB_", "PC_"}
	var out []dsl.Action
	for i, s := range sessions {
		out = append(out, prefixed(prefixes[i], s.Actions())...)
	}
	return out
}

// prefixed 复制动作切片并把动词名改为 prefix+原名。dsl.Action 为值类型，复制后改 Name 即可；
// Run 闭包仍按原动词分发（动词→实现的映射在动作构造处），故改名只影响脚本侧方言。
// 绝不在 internal 包内改名：容器工具（filesys_run/browser_run/desktop_run）的裸动词方言不变。
func prefixed(prefix string, acts []dsl.Action) []dsl.Action {
	out := make([]dsl.Action, len(acts))
	for i, a := range acts {
		a.Name = prefix + a.Name
		out[i] = a
	}
	return out
}

// envVars 构造 DSL 只读 env（与 mcp-tools / llm_run 同名同值）：根作用域只注入 env 一个对象，
// 脚本以 {{env.<NAME>}} 引用。PROJECT 与 WORKDIR 同值。
func envVars(workDir, dataDir, tempDir, exeDir string) map[string]any {
	return map[string]any{"env": map[string]any{
		"CHONKPILOT_WORKDIR": workDir,
		"CHONKPILOT_DATADIR": dataDir,
		"CHONKPILOT_TEMPDIR": tempDir,
		"CHONKPILOT_EXEDIR":  exeDir,
		"CHONKPILOT_PROJECT": workDir,
	}}
}

// fallbackSummary 是脚本未用 `=> $RETURN` 时的回落汇总：各域 Summary() + 引擎动作输出汇总
// （无 => 目标的动作输出，含未重定向的 LLM 结果）+ DSL 运行时错误文本。
func fallbackSummary(rr dsl.RunResult, sessions ...domainSession) string {
	var lines []string
	for _, s := range sessions {
		lines = append(lines, s.Summary()...)
	}
	lines = append(lines, rr.Summary...)
	for _, e := range rr.Errors {
		lines = append(lines, fmt.Sprintf("【DSL 错误】第 %d 行：%s", e.Line, e.Msg))
	}
	return strings.Join(lines, "\n")
}

// joinErrors 把运行时错误拼为可诊断文本（result.error）。
func joinErrors(errs []dsl.RunError) string {
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, fmt.Sprintf("第 %d 行：%s", e.Line, e.Msg))
	}
	return strings.Join(parts, "; ")
}
