// dslrun.go — `dsl_run` 统一编排工具（决策 42 §2 (247)–(252)）。
//
// 定位：gateway 自持的 **in-memory 节点**（节点名 "dsl"，命名空间 "-" → 暴露名**恰为** `dsl_run`），
// 与 self / dir 节点并列注册进 registry，经 `routesFor` 出现在工具面。
//
// 形态：调用时 **spawn 独立受保护执行器** `chonkpilot-dsl-executor.exe`（一作业一进程），
// 与执行器之间用**自建 stdio 行协议**（非 MCP 协议）——文件/浏览器/桌面动作全在执行器进程内
// 完成（进程级 agentbox 沙箱随 spawn 环境 `CHONKPILOT_SANDBOX` 下发），LLM 步骤由执行器经
// stdout 发 `llm_call`，gateway 经 **MQ（llm-start + llm-send）** 执行子轮次后回写 `llm_result`。
//
// 行协议（冻结，与执行器逐字一致）：
//
//	下行（gateway → exe stdin）
//	  {"t":"run","job":"<jobid>","script":"<DSL>","file":"<脚本文件绝对路径，script 为空时>","instance":"...","work_dir":"...","data_dir":"...","return_file":"<绝对路径>"}
//	  {"t":"llm_result","call":"<callid>","text":"...","error":""}
//	上行（exe stdout → gateway；日志走 stderr）
//	  {"t":"llm_call","call":"<callid>","agent":"...","prompt":"...","purpose":"...","session":"<jobid>-N"}
//	  {"t":"tree","job":"<jobid>","nodes":[{"id":"...","parent":"","kind":"dsl_loop|dsl_parallel","label":"...","loop_current":N,"loop_total":N}]}
//	  {"t":"step","job":"<jobid>","no":N,"status":"running|done|error|cancelled","purpose":"...","elapsed_ms":N,"session":"<sid>","statement":"<静态语句 id 或空>"}
//	  {"t":"result","job":"<jobid>","ok":true,"text":"...","file":"","size":0,"error":"","overflow":false,"return_used":false}
//	  {"t":"log","level":"info","msg":"..."}
//
// 展示供数（DSL-3）：tree/step/result 经 `dslTreeReporter` 转成既有 **task-* 事件**
// （task-started / task-updated / task-done）发往任务层（tasktree 唯一写者）落库 → 前端渲染 DSL 面板
// （见 dsltree.go）。**零新增 MQ 主题**。
//
// 取消：调用方 ctx 取消（工具层取消 / turn 停止）→ kill 执行器进程；在飞 LLM 子轮次由
// runDSLLLM 在 ctx.Done 时级联发 `llm-cancel`。
//
// 契约单源：工具定义优先取自装配层注入的 `Params.ServerTools["dsl_run"]`（category=server 契约，
// 经 mcp-server `ServerTools(root,cfg)` 读取）——描述/schema/_meta 与 `capability/tools/core/dsl_run.tool.md`
// 一致，避免漂移；缺失（单测 / 未接线）→ 回落内置定义（见 registerDSLNode）。
package mcpgateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-lib/paths"
	"github.com/chonkpilot/chonkpilot-lib/winproc"
)

const (
	// dslRunToolName 是工具名（= 契约文件名）。
	dslRunToolName = "dsl_run"
	// dslNodeName 是 gateway 自持 in-memory 节点名。
	dslNodeName = "dsl"
	// dslExecutorExe 是执行器产物名。
	dslExecutorExe = "chonkpilot-dsl-executor.exe"
	// dslSandboxCategory 是 dsl 执行器取 agentbox 策略所用的 executor 类别
	// （复用既有 `tool_sandbox` 开关表；执行器覆盖文件/浏览器/桌面三域动作，此处取 core 档）。
	dslSandboxCategory = "core"
	// dslDataTimeout 是 dsl LLM 子轮次所需数据面请求（ensure-session / load-messages）超时。
	dslDataTimeout = 3 * time.Second
	// dslProcKillWait 是取消后等待执行器退出的宽限。
	dslProcKillWait = 5 * time.Second
	// dslReturnFileMaxAge 是 `$RETURN` file 态落盘文件的陈旧阈值：作业启动时清理临时根内
	// mtime 早于该阈值的 dsl-return-*.md（防常驻进程下按作业线性累积；保留近期结果，C-43）。
	dslReturnFileMaxAge = 24 * time.Hour
)

// ─── 行协议载荷 ─────────────────────────────────────────

// dslRunMsg 是下行 run（gateway → exe stdin）。
type dslRunMsg struct {
	T          string `json:"t"` // "run"
	Job        string `json:"job"`
	Script     string `json:"script"`
	File       string `json:"file,omitempty"` // 脚本文件绝对路径（script 为空时下发；由执行器受沙箱读盘）
	Instance   string `json:"instance"`
	WorkDir    string `json:"work_dir"`
	DataDir    string `json:"data_dir"`
	ReturnFile string `json:"return_file"`
}

// dslLLMResultMsg 是下行 llm_result（gateway → exe stdin）。
type dslLLMResultMsg struct {
	T     string `json:"t"` // "llm_result"
	Call  string `json:"call"`
	Text  string `json:"text"`
	Error string `json:"error"`
}

// dslLLMCallMsg 是上行 llm_call（exe stdout → gateway）。
type dslLLMCallMsg struct {
	T       string `json:"t"` // "llm_call"
	Call    string `json:"call"`
	Agent   string `json:"agent"`
	Prompt  string `json:"prompt"`
	Purpose string `json:"purpose"`
	Session string `json:"session"`
}

// dslResultMsg 是上行 result（exe stdout → gateway；一作业终态）。
type dslResultMsg struct {
	T        string `json:"t"` // "result"
	Job      string `json:"job"`
	OK       bool   `json:"ok"`
	Text     string `json:"text"`
	File     string `json:"file"`
	Size     int    `json:"size"`
	Error    string `json:"error"`
	Overflow bool   `json:"overflow"`
	// ReturnUsed = 脚本是否使用 `$RETURN` 通道（false → text/file 为回落汇总，不写 return_*）。
	ReturnUsed bool `json:"return_used"`
}

// dslLogMsg 是上行 log（exe stdout → gateway；仅诊断）。
type dslLogMsg struct {
	T     string `json:"t"` // "log"
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

// dslLLMRunner 执行一次 LLM 步骤（协议 llm_call → 文本/错误）。可注入以便单测协议泵。
type dslLLMRunner func(ctx context.Context, call dslLLMCallMsg) (string, error)

// ─── 工具定义 + 节点注册 ────────────────────────────────

// dslRunToolFallback 是契约缺失时的内置工具定义（字段与 dsl_run.tool.md 逐字一致）。
func dslRunToolFallback() *mcp.Tool {
	return &mcp.Tool{
		Name:        dslRunToolName,
		Description: "统一编排：把文件（FILE_*）、浏览器（WEB_*）、桌面（PC_*）与 LLM 委派四类动作写成一份 DSL 脚本，交独立受保护执行器执行（进程级沙箱）；LLM 步骤经 MQ 执行子轮次。script/file 二选一；结果经 $RETURN 通道两态返回（≤64K inline，超出落盘返回文件名+大小）。",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"script":  map[string]any{"type": "string", "description": "DSL 脚本文本（与 file 二选一）"},
				"file":    map[string]any{"type": "string", "description": "DSL 脚本文件路径（绝对 / ~/ / !/ 开头；与 script 二选一）"},
				"timeout": map[string]any{"type": "number", "description": "作业超时秒数（可选；<=0/缺省 = 不设作业级超时）"},
			},
		},
		Meta: mcp.Meta{"hot": true, "category": "server", "async": "always"},
	}
}

// registerDSLNode 注册 gateway 自持的 dsl in-memory 节点（暴露名 = dsl_run）。
// 工具定义**契约单源**：优先取装配层注入的 `Params.ServerTools["dsl_run"]`（category=server 契约，
// 描述/schema/_meta 与 dsl_run.tool.md 一致）；缺失（如单测 / 未接线）→ 回落内置定义。
// 兼容：旧装配若仍把该契约注册进 self（不再发生，mcp-server 已过滤 category=server）→ 摘除占位。
func (g *Gateway) registerDSLNode(ctx context.Context) error {
	tool := g.params.ServerTools[dslRunToolName]
	if tool == nil {
		if g.self != nil {
			if t, err := g.selfToolByName(ctx, dslRunToolName); err == nil && t != nil {
				tool = t // 旧装配回落（契约单源未注入时）
				if g.params.MCPServer != nil {
					g.params.MCPServer.RemoveTools(dslRunToolName)
					if rerr := g.reconcileSelf(); rerr != nil {
						g.logf("[gateway] dsl_run: 从 self 摘除占位工具后刷新失败: %v", rerr)
					}
				}
			}
		}
	}
	if tool == nil {
		tool = dslRunToolFallback()
	}
	ms := mcp.NewServer(&mcp.Implementation{Name: "chonkpilot-gateway-dsl", Version: "1.0.0"}, nil)
	ms.AddTool(tool, g.handleDSLRun)
	node, err := newMemNode(dslNodeName, ms)
	if err != nil {
		return fmt.Errorf("dsl node connect: %w", err)
	}
	tools, err := node.ListTools(ctx)
	if err != nil {
		node.Close()
		return fmt.Errorf("dsl node list tools: %w", err)
	}
	ps := &providerState{
		key: provKey("mem", scopeGlobal, dslNodeName),
		// Namespace "-" → applyPrefix 不改名 → 暴露名恰为 dsl_run。
		entry:  &ServerEntry{ID: dslNodeName, Name: "DSL 编排", Category: "server", Namespace: "-", Origin: OriginBuiltin},
		prov:   &memNodeProvider{node: node, inject: true}, // 内置来源：调用以协议 _meta 透传调用上下文
		status: "connected",
		cb:     newBreaker(g.params.CBThreshold, g.params.CBCooldown, true),
		scope:  scopeGlobal,
	}
	if err := g.reg.registerProvider(ps, tools, ""); err != nil {
		node.Close()
		return fmt.Errorf("dsl node register: %w", err)
	}
	g.logf("[gateway] dsl node connected: %d tools (%s)", len(tools), dslRunToolName)
	return nil
}

// selfToolByName 在 self 节点当前工具列表中按原名查一个工具（未命中 → nil）。
func (g *Gateway) selfToolByName(ctx context.Context, name string) (*mcp.Tool, error) {
	if g.self == nil {
		return nil, nil
	}
	tools, err := g.self.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range tools {
		if t != nil && t.Name == name {
			return t, nil
		}
	}
	return nil, nil
}

// ─── 工具 handler ──────────────────────────────────────

// handleDSLRun 是 dsl_run 工具 handler（挂在本节点官方 go-sdk server 上）：
// 经协议 _meta 重建调用 ctx（instance/work_dir/data_dir），再跑一次作业。
func (g *Gateway) handleDSLRun(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cctx := execCtxFromMeta(ctx, req.Params.Meta)
	args, err := argsFromRequest(req.Params.Arguments)
	if err != nil {
		return toolErrorText(err.Error()), nil
	}
	text, err := g.runDSLJob(cctx, args)
	if err != nil {
		return toolErrorText(err.Error()), nil
	}
	return toolTextResult(text), nil
}

// ─── 作业执行 ──────────────────────────────────────────

// runDSLJob 跑一次 dsl_run：解析参数 → 解析 exe → spawn → 写 run → 泵协议 → 返回结果文本。
func (g *Gateway) runDSLJob(ctx context.Context, args map[string]any) (string, error) {
	instance := strings.TrimSpace(InstanceFromContext(ctx))
	if instance == "" {
		return "", fmt.Errorf("%s: 缺少 instance 调用上下文", dslRunToolName)
	}
	workDir := WorkDirFromContext(ctx)
	dataDir := DataDirFromContext(ctx)

	script, _ := args["script"].(string)
	file, _ := args["file"].(string)
	scriptFile := ""
	if strings.TrimSpace(script) == "" {
		if strings.TrimSpace(file) == "" {
			return "", fmt.Errorf("%s: script/file 至少提供其一", dslRunToolName)
		}
		p, msg := paths.ResolvePathFor(instance, file, "")
		if msg != "" {
			return "", fmt.Errorf("%s: file：%s", dslRunToolName, msg)
		}
		// 脚本读取**下移执行器**（dslexec）：此处仅下发已校验的绝对路径，由携带
		// CHONKPILOT_SANDBOX 策略的执行器进程内读盘（受 agentbox 读校验），
		// 避免 gateway 进程直接读文件绕过沙箱（决策 42 §2 (250) 口径）。
		scriptFile = p
	}

	// 作业超时（可选）：>0 才设；<=0/缺省 = 不设作业级超时。
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(t)*time.Second)
		defer cancel()
	}

	tempRoot, err := paths.TempRootFor(instance)
	if err != nil {
		return "", fmt.Errorf("%s: %w", dslRunToolName, err)
	}
	// 作业启动兜底：清理临时根内陈旧的 `$RETURN` 落盘文件（常驻进程下防按作业线性累积；
	// 取消路径的残留也在此被下次作业清掉；仅删陈旧文件，保留近期结果，C-43）。
	if n := paths.SweepStaleReturnFiles(tempRoot, dslReturnFileMaxAge); n > 0 {
		g.logf("[gateway] dsl_run: swept %d stale $RETURN files under %s", n, tempRoot)
	}
	jobID := newJobID()
	returnFile := filepath.Join(tempRoot, "dsl-return-"+jobID+".md")

	exe, err := g.dslExecutorPath()
	if err != nil {
		return "", err
	}

	cmd := exec.Command(exe)
	cmd.SysProcAttr = winproc.SysProcAttr()
	cmd.Env = g.dslExecutorEnv(instance, workDir, dataDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("%s: stdin pipe: %w", dslRunToolName, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("%s: stdout pipe: %w", dslRunToolName, err)
	}
	cmd.Stderr = &dslStderrWriter{logf: g.logf}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("%s: 启动执行器: %w", dslRunToolName, err)
	}

	// 下行 run
	enc := json.NewEncoder(stdin)
	if err := enc.Encode(dslRunMsg{
		T: "run", Job: jobID, Script: script, File: scriptFile, Instance: instance,
		WorkDir: workDir, DataDir: dataDir, ReturnFile: returnFile,
	}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", fmt.Errorf("%s: 写 run: %w", dslRunToolName, err)
	}

	// LLM 步骤执行器（闭包携带作业上下文：instance/父会话/work_dir/data_dir）。
	runner := func(lctx context.Context, call dslLLMCallMsg) (string, error) {
		return g.runDSLLLM(lctx, call, instance, SessionFromContext(ctx), workDir, dataDir)
	}

	// 展示供数上报器（DSL-3）：tree/step/result → 既有 task-* 事件（任务层落库）。
	reporter := newDSLJobReporter(jobID, instance, TopSessionFromContext(ctx), ParentFromContext(ctx),
		SessionFromContext(ctx), TurnFromContext(ctx), workDir, ToolCallIDFromContext(ctx),
		func(subject string, payload map[string]any) { g.pub(subject, marshal(payload)) })

	type pumpOut struct {
		res dslResultMsg
		err error
	}
	outCh := make(chan pumpOut, 1)
	go func() {
		res, perr := dslPump(ctx, stdout, stdin, g.logf, runner, reporter)
		outCh <- pumpOut{res, perr}
	}()

	var po pumpOut
	select {
	case po = <-outCh:
	case <-ctx.Done():
		// 取消：kill 执行器（幂等）→ stdout 关闭 → 泵返回 → 收尾。
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(dslProcKillWait):
		}
		reporter.finish("cancelled", dslResultMsg{})
		return "", ctx.Err()
	}
	_ = stdin.Close()
	_ = cmd.Wait()
	if po.err != nil {
		reporter.finish("error", po.res)
		return "", fmt.Errorf("%s: %w", dslRunToolName, po.err)
	}
	if !po.res.OK {
		// result{ok:false}：终端态已由 reporter.OnResult 发（error）；此处回错误文本。
		if msg := strings.TrimSpace(po.res.Error); msg != "" {
			return "", errors.New(msg)
		}
		return "", fmt.Errorf("%s: 作业失败", dslRunToolName)
	}
	// $RETURN 两态：overflow/有文件 → 文件名+大小；否则 inline 文本。
	if po.res.Overflow || strings.TrimSpace(po.res.File) != "" {
		return fmt.Sprintf("作业完成（结果 %d 字节，已落盘）：%s", po.res.Size, po.res.File), nil
	}
	return po.res.Text, nil
}

// dslPump 泵读执行器 stdout 协议行并回写 stdin（**可测**：不含真实 exe；llm 为注入回调，
// rep 为注入的展示上报器——tree/step/result 转 tasktree 变更，nil = 不上报）。
// 返回 result 行（作业终态）或错误（stdout 关闭而未出 result / ctx 取消 / 写回失败）。
// 非协议行（应为空——执行器日志走 stderr）一律忽略。
func dslPump(ctx context.Context, stdout io.Reader, stdin io.Writer, logf func(string, ...any), llm dslLLMRunner, rep dslTreeReporter) (dslResultMsg, error) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 32*1024*1024) // 大返回值（$RETURN inline 上限 64K，留裕量）
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return dslResultMsg{}, err
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var head struct {
			T string `json:"t"`
		}
		if json.Unmarshal(line, &head) != nil {
			continue
		}
		switch head.T {
		case "llm_call":
			var call dslLLMCallMsg
			if err := json.Unmarshal(line, &call); err != nil {
				continue
			}
			text, err := llm(ctx, call)
			res := dslLLMResultMsg{T: "llm_result", Call: call.Call, Text: text}
			if err != nil {
				res.Error = err.Error()
			}
			if err := writeProtoLine(stdin, res); err != nil {
				return dslResultMsg{}, fmt.Errorf("写 llm_result: %w", err)
			}
		case "tree":
			var tm dslTreeMsg
			if err := json.Unmarshal(line, &tm); err == nil && rep != nil {
				rep.OnTree(tm.Nodes)
			}
		case "step":
			var sm dslStepMsg
			if err := json.Unmarshal(line, &sm); err == nil && rep != nil {
				rep.OnStep(sm)
			}
		case "log":
			var lg dslLogMsg
			if json.Unmarshal(line, &lg) == nil && logf != nil {
				logf("[dsl-executor] %s: %s", lg.Level, lg.Msg)
			}
		case "result":
			var r dslResultMsg
			if err := json.Unmarshal(line, &r); err != nil {
				return dslResultMsg{}, fmt.Errorf("解析 result: %w", err)
			}
			if rep != nil {
				rep.OnResult(r)
			}
			return r, nil
		}
	}
	if err := sc.Err(); err != nil {
		return dslResultMsg{}, err
	}
	return dslResultMsg{}, errors.New("执行器未返回 result（stdout 已关闭）")
}

// writeProtoLine 写一行协议 JSON（行分隔）。
func writeProtoLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// ─── exe 路径 / 环境（沙箱）────────────────────────────

// dslExecutorPath 解析执行器 exe 路径：优先 Params.DSLExecutor（显式覆盖），其次
// 「exeDir 同级」与「exeDir/capability/executors」候选（与内置 executor 同目录约定）。
func (g *Gateway) dslExecutorPath() (string, error) {
	if p := strings.TrimSpace(g.params.DSLExecutor); p != "" {
		return p, nil
	}
	var cands []string
	if d, err := exedir.Dir(); err == nil && strings.TrimSpace(d) != "" {
		cands = append(cands,
			filepath.Join(d, dslExecutorExe),
			filepath.Join(d, "capability", "executors", dslExecutorExe),
		)
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("%s: 未找到执行器 %s（候选：%s）", dslRunToolName, dslExecutorExe, strings.Join(cands, "; "))
}

// dslExecutorEnv 组装执行器子进程环境：继承宿主环境（剔除残留 CHONKPILOT_* 防泄漏）→ 注入
// CHONKPILOT_INSTANCE/WORKDIR/DATADIR + agentbox 策略（CHONKPILOT_SANDBOX，来源与既有 executor
// spawn 一致 = 共享执行配置 SandboxConfig.SandboxPolicyFor(category)；`""` = 不下发）。
func (g *Gateway) dslExecutorEnv(instance, workDir, dataDir string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+4)
	for _, kv := range base {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if strings.HasPrefix(name, "CHONKPILOT_") {
			continue
		}
		out = append(out, kv)
	}
	add := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			out = append(out, k+"="+v)
		}
	}
	add("CHONKPILOT_INSTANCE", instance)
	add("CHONKPILOT_WORKDIR", workDir)
	add("CHONKPILOT_DATADIR", dataDir)
	if policy := g.dslSandboxPolicy(); policy != "" {
		out = append(out, agentbox.EnvSandbox+"="+policy)
		g.logf("[gateway] dsl executor agentbox 隔离下发: dirs=%s", truncateStr(policy, 500))
	}
	return out
}

// dslSandboxPolicy 取 dsl 执行器的 agentbox 策略 JSON（来源 = 装配方注入的共享执行配置；
// 窄接口按需断言，未实现 / 未开启 → 空串 = 不下发）。类别取 dslSandboxCategory。
func (g *Gateway) dslSandboxPolicy() string {
	type sandboxPolicyProvider interface{ SandboxPolicyFor(category string) string }
	if p, ok := g.params.MCPConfig.(sandboxPolicyProvider); ok && p != nil {
		return p.SandboxPolicyFor(dslSandboxCategory)
	}
	return ""
}

// dslStderrWriter 把执行器 stderr 逐行转日志（避免 -H windowsgui 下 stderr 不可见）。
type dslStderrWriter struct {
	logf func(string, ...any)
	mu   sync.Mutex
	buf  []byte
}

func (w *dslStderrWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.buf[:i]), "\r")
		w.buf = w.buf[i+1:]
		if line != "" && w.logf != nil {
			w.logf("[dsl-executor] %s", line)
		}
	}
	return len(p), nil
}

// ─── LLM 步骤（经 MQ 执行子轮次）────────────────────────

// runDSLLLM 执行一次 LLM 步骤（协议 llm_call → 文本）：经 MQ 建子会话/轮次——
// ensure-session（parent = 主会话）、llm-start（agent persona + parents）、llm-send（text-user）→
// 等 session-complete → 读该轮消息取 assistant 文本。取消：ctx 取消时级联 `llm-cancel`。
//
// 说明（不确定点，2026-10-07 查证）：llm-start/llm-send 方法面**不同步返回文本**
// （onLLMStart 仅回 {accepted,session,turn}；onLLMSend 无结果），文本经流事件/落库产出 →
// 故本实现以「session-complete 终态 + data-session-load-messages 读该轮」取得结果。
func (g *Gateway) runDSLLLM(ctx context.Context, call dslLLMCallMsg, instance, parentSession, workDir, dataDir string) (string, error) {
	if g.bus == nil {
		return "", errors.New("llm 不可用（无总线）")
	}
	_ = workDir
	_ = dataDir
	session := strings.TrimSpace(call.Session)
	if session == "" {
		return "", errors.New("llm_call 缺少 session")
	}
	// 子会话（parent = 主会话）——与 llm_run 的 EnsureSessionWithParent 同口径。
	if _, err := g.dataReq(ctx, msgkeys.TopicDataSessionEnsureSession, instance, map[string]any{
		"session_id": session, "parent_session_id": parentSession,
	}); err != nil {
		g.logf("[gateway] dsl llm: ensure-session 失败（继续）: %v", err)
	}

	turn := newJobID()
	var comp struct {
		status, code, message string
	}
	done := make(chan struct{})
	var once sync.Once
	sub, err := g.bus.On(msgkeys.TopicSessionComplete, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		if s, _ := m["turn"].(string); s != turn {
			return nil
		}
		comp.status = strOf(m["status"])
		comp.code = strOf(m["code"])
		comp.message = strOf(m["message"])
		once.Do(func() { close(done) })
		return nil
	})
	if err != nil {
		return "", err
	}
	defer sub.Unsubscribe()

	start := g.bus.Emit(ctx, msgkeys.TopicLlmStart, map[string]any{
		"instance_id": instance,
		"session":     session,
		"turn":        turn,
		"agent":       call.Agent,
		"parents":     parentsOf(parentSession),
	}).Wait()
	if err := start.Err(); err != nil {
		return "", err
	}
	if m, ok := start.Result.(map[string]any); ok {
		if accepted, _ := m["accepted"].(bool); !accepted {
			return "", fmt.Errorf("llm-start 未受理: %s", strOf(m["error"]))
		}
	}

	send := g.bus.Emit(ctx, msgkeys.TopicLlmSend, map[string]any{
		"instance_id": instance,
		"session":     session,
		"turn":        turn,
		"type":        "text-user",
		"content":     call.Prompt,
	}).Wait()
	if err := send.Err(); err != nil {
		return "", err
	}

	select {
	case <-done:
	case <-ctx.Done():
		// 级联取消在飞子轮次（不依赖执行器配合）。
		g.bus.Emit(context.Background(), msgkeys.TopicLlmCancel, map[string]any{
			"instance_id": instance, "session": session, "turn": turn,
		})
		return "", ctx.Err()
	}
	if comp.status == "error" {
		msg := comp.message
		if msg == "" {
			msg = comp.code
		}
		if msg == "" {
			msg = "llm 轮次失败"
		}
		return "", errors.New(msg)
	}
	return g.loadTurnText(ctx, instance, turn), nil
}

// loadTurnText 读某轮消息并返回**最终回答正文**（子轮次结果），不含 reasoning / tool 相关内容。
//
// 口径（对齐内核 `data.BriefMessages` 的简化态语义，见 src/lib/data/brief.go）：
//   - 仅取 `role=="assistant"`；跳过 `role=="tool"`（工具结果）与 user/system；
//   - 跳过**带 `tool_calls` 的 assistant 行** —— 该类行的 content 属工具流中间文本（如"我先看下文件…"），
//     非最终回答（落库实现：同一轮 content/reasoning/tool_calls 写**同一行**，见 turn.go flushAssistant）；
//   - `reasoning` 字段**绝不**并入输出（思维链）；
//   - 剩余 assistant 行的 `content` 非空白 → 按换行拼接（多行多段的最终正文合并）。
func (g *Gateway) loadTurnText(ctx context.Context, instance, turn string) string {
	res, err := g.dataReq(ctx, msgkeys.TopicDataSessionLoadMessages, instance, map[string]any{"turn_id": turn})
	if err != nil {
		// 读回失败不再静默：记日志（返回空正文，调用方按「无最终正文」处理）（C-21）。
		if g.logf != nil {
			g.logf("[gateway] dsl_run loadTurnText failed (instance=%s turn=%s): %v", instance, turn, err)
		}
		return ""
	}
	return pickFinalAnswer(res["messages"])
}

// pickFinalAnswer 从 `data-session-load-messages` 的消息数组里挑出**最终回答正文**（纯函数，单测直调）。
// 规则见 loadTurnText 注释：仅 assistant、去 tool_calls 行、去 role=tool、去 reasoning，正文按换行拼接。
func pickFinalAnswer(messages any) string {
	raw, _ := messages.([]any)
	var parts []string
	for _, it := range raw {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if strOf(m["role"]) != "assistant" {
			continue
		}
		if hasToolCalls(m["tool_calls"]) {
			continue // 工具调用轮的中间正文：非最终回答
		}
		if c := strOf(m["content"]); strings.TrimSpace(c) != "" {
			parts = append(parts, c)
		}
	}
	return strings.Join(parts, "\n")
}

// hasToolCalls 报告消息的 tool_calls 字段是否非空（兼容 []any / nil）。
func hasToolCalls(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case []any:
		return len(t) > 0
	case []map[string]any:
		return len(t) > 0
	}
	return false
}

// dataReq 发一条 data-<domain> 请求并等应答（对齐 llm/server dataRequest 语义：带 req_id 同主题
// 应答匹配；超时 dslDataTimeout）。供 dsl LLM 子轮次建会话/读消息用。
func (g *Gateway) dataReq(ctx context.Context, subject, instanceID string, data map[string]any) (map[string]any, error) {
	reqID := newJobID()
	type reply struct {
		result map[string]any
		err    error
	}
	done := make(chan reply, 1)
	sub, err := g.bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m struct {
			ReqID  string         `json:"req_id"`
			OK     *bool          `json:"ok"`
			Result map[string]any `json:"result"`
			Error  string         `json:"error"`
		}
		if json.Unmarshal(v.Payload, &m) != nil || m.ReqID != reqID || m.OK == nil {
			return nil
		}
		if !*m.OK {
			msg := m.Error
			if msg == "" {
				msg = "persist error"
			}
			select {
			case done <- reply{err: errors.New(msg)}:
			default:
			}
			return nil
		}
		select {
		case done <- reply{result: m.Result}:
		default:
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()
	payload := map[string]any{"instance_id": instanceID, "data": data, "req_id": reqID}
	g.bus.Emit(ctx, subject, payload)
	select {
	case r := <-done:
		return r.result, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(dslDataTimeout):
		return nil, fmt.Errorf("%s timeout", subject)
	}
}

// ─── 小工具 ────────────────────────────────────────────

// newJobID 生成作业 id（随机 16 字节 hex；同时用作子轮次 turn id）。
func newJobID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// parentsOf 构造子会话父链（parentSession 空 = nil）。
func parentsOf(parentSession string) []string {
	if strings.TrimSpace(parentSession) == "" {
		return nil
	}
	return []string{parentSession}
}

// strOf 取 map 值的字符串形态（非字符串 → ""）。
func strOf(v any) string {
	s, _ := v.(string)
	return s
}
