// llm_run 委派/编排统一 DSL 执行器：接入 chonkpilot-lib/dsl 核心包（docs/dsl-core.md +
// 3A-工具与编排）。阶段 1/2 收敛后 llm 域工具唯一 llm 型 = llm_run——旧单次/批量
// 委派工具已删除，其语义并入本 DSL：脚本/file 由作业级参数承接，单次/批量委派 =
// 一行/多行 LLM 指令。
//
// llm_run 脚本使用 dsl-core 语法：核心保留字 SET/IF/LOOP/PARALLEL/BREAK/CONTINUE/EXIT/END
// + 动作动词 LLM（本文件注入）。LLM 指令：LLM "agent" "prompt" ["目的"] [=> 目标]，
// agent=委派对象标识（必填，当前仅非空校验，资产化检索后续阶段接入）、prompt=委派提示词
// （必填，{{}} 插值）、目的=本次子 LLM 的运行目的（可选，即子任务节点展示名；缺省回退提示词截断）；
// 三参均可 {{}} 插值；参数间空白或逗号分隔均兼容；参数超过 3 个 = 顶层失败。文件路径须绝对 / ~/ 开头 / !/ 开头，
// 项目内路径用宿主注入的 {{env.CHONKPILOT_WORKDIR}} 显式拼接（相对路径拒绝）。示例：
//
//	SET #"{{env.CHONKPILOT_WORKDIR}}/tasks.json" => src
//	LOOP item=src.array concurrency=3
//	   IF item.done != true
//	      LLM "worker" "实现 {{item.name}}" => #"{{env.CHONKPILOT_WORKDIR}}/out/{{item.name}}.py"
//	      SET item.done => true
//	   END
//	END
//
// 执行：每个 LLM 步骤 = 独立 stateless 子轮次（runChildTurn，父会话链 + 任务树）；
// 子任务节点（kind=llm，ParentID=根节点）展示名 = 目的（第三参，缺省回退 prompt）截断；
// => 目标（变量捕获 / 文件句柄 / .eof 追加）由 dsl 引擎写入；SET 字段写即时整源写回
// （断点续跑）；失败记错不阻塞；EXIT/取消（引擎 Cancel）跨分支终止。
package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/paths"
)

// ─── 真实文件系统（严格路径解析；写入自动建父目录；记录写日志供汇总）───

// jobFileSys 是作业文件系统视图；inst = 归属 instance（`!/` 临时根按 instance 解析，缺口 4）。
type jobFileSys struct {
	log  *writeLog
	inst string
}

func (f *jobFileSys) Open(path string) dsl.FileHandle {
	return &jobFile{fs: f, path: path}
}

type jobFile struct {
	fs   *jobFileSys
	path string
	mu   sync.Mutex
}

// abs 严格解析句柄路径为绝对路径（R-11 二次升级：绝对 / ~/ 开头用户目录 / !/ 开头临时目录；
// 相对路径报错）。不再按 workDir 兜底 Join——防止路径写错位置产生垃圾文件；
// DSL 内请用 {{env.CHONKPILOT_WORKDIR}} 显式拼接项目内绝对路径。
// `!/` 落到**本作业归属 instance** 的临时根（ResolvePathFor；缺口 4：多 instance 互不覆盖）。
func (f *jobFile) abs() (string, error) {
	abs, msg := paths.ResolvePathFor(f.fs.inst, f.path, "")
	if msg != "" {
		return "", errors.New(msg)
	}
	if abs == "" {
		return "", errors.New("路径不能为空")
	}
	return abs, nil
}

func (f *jobFile) Path() string { return f.path }

func (f *jobFile) Exists() bool {
	abs, err := f.abs()
	if err != nil {
		return false
	}
	_, serr := os.Stat(abs)
	return serr == nil
}

func (f *jobFile) Stat() (dsl.FileInfo, error) {
	abs, err := f.abs()
	if err != nil {
		return dsl.FileInfo{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return dsl.FileInfo{}, err
	}
	content, _ := f.ReadText()
	lines := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") && content != "" {
		lines++
	}
	blocks := 0
	if content != "" {
		blocks = strings.Count(content, "\n\n") + 1
	}
	return dsl.FileInfo{Path: f.path, Size: fi.Size(), Lines: int64(lines), Blocks: int64(blocks)}, nil
}

func (f *jobFile) ReadText() (string, error) {
	abs, err := f.abs()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (f *jobFile) ReadLines() ([]string, error) {
	c, err := f.ReadText()
	if err != nil {
		return nil, err
	}
	ls := strings.Split(c, "\n")
	if len(ls) > 0 && ls[len(ls)-1] == "" {
		ls = ls[:len(ls)-1]
	}
	return ls, nil
}

func (f *jobFile) ReadRange(n, m int) ([]string, error) {
	ls, err := f.ReadLines()
	if err != nil {
		return nil, err
	}
	L := len(ls)
	if L == 0 {
		if n == 0 && (m == 0 || m == -1) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("range 越界（空文件）")
	}
	n, m = normIdx(n, m, L)
	if n < 0 || n >= L || m < n || m >= L {
		return nil, fmt.Errorf("range(%d,%d) 越界（共 %d 行）", n, m, L)
	}
	return ls[n : m+1], nil
}

func (f *jobFile) WriteAll(text string) error {
	p, err := f.abs()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		return err
	}
	f.fs.log.add("已写入", f.path)
	return nil
}

func (f *jobFile) Append(text string) error {
	if text == "" {
		return nil
	}
	p, err := f.abs()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	fd, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer fd.Close()
	if _, err := fd.WriteString(text); err != nil {
		return err
	}
	f.fs.log.add("已追加", f.path)
	return nil
}

func (f *jobFile) ReplaceLines(n, m int, lines []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, err := f.ReadText()
	if err != nil {
		return err
	}
	ls := strings.Split(cur, "\n")
	if len(ls) > 0 && ls[len(ls)-1] == "" {
		ls = ls[:len(ls)-1]
	}
	L := len(ls)
	n, m = normIdx(n, m, L)
	if n >= L || n < 0 {
		return nil // 超界静默忽略
	}
	if m >= L {
		m = L - 1
	}
	head := append([]string{}, ls[:n]...)
	mid := append([]string{}, lines...)
	tail := []string{}
	if m+1 < L {
		tail = append([]string{}, ls[m+1:]...)
	}
	out := append(head, append(mid, tail...)...)
	p, err := f.abs()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f.fs.log.add("已替换", f.path)
	return os.WriteFile(p, []byte(strings.Join(out, "\n")), 0o644)
}

func normIdx(n, m, L int) (int, int) {
	if n < 0 {
		n += L
	}
	if m < 0 {
		m += L
	}
	return n, m
}

// writeLog 记录本作业的所有文件写（LLM 落盘 / SET 写回），顺序追加、线程安全。
type writeLog struct {
	mu    sync.Mutex
	lines []string
}

func (w *writeLog) add(verb, path string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.lines = append(w.lines, fmt.Sprintf("%s %s", verb, path))
	w.mu.Unlock()
}

func (w *writeLog) snapshot() []string {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string{}, w.lines...)
}

// ─── 作业执行器 ───

type jobEnv struct {
	s       *Server
	parent  *turnCtx
	node    *TaskNode
	workDir string
	fs      *jobFileSys

	jobSession string
	mu         sync.Mutex
	failed     int
	step       int
}

// runSubJob 执行 llm_run：读 script/file → dsl 解析 → 引擎执行（LLM 动词 = 子轮次）→ 汇总回填。
func (s *Server) runSubJob(parent *turnCtx, toolCallID string, node *TaskNode, args map[string]any) {
	script, err := jobScriptOf(parent.req.InstanceID, args)
	if err != nil {
		s.tasks.done(node.TaskID, TaskStateError, "", err.Error())
		parent.FeedToolResultStatus(toolCallID, "错误: "+err.Error(), "failed")
		return
	}
	// agent 不取作业顶层/父轮兜底：由每条 LLM 指令第一段（委派对象标识）携带（llmAction）
	jr := &jobEnv{
		s: s, parent: parent, node: node,
		workDir: parent.req.WorkDir,
		// fs 绑定轮次 instance：作业内 `!/` 路径按该 instance 的临时根解析（缺口 4：
		// 多 instance 同进程各自独立根，互不覆盖）。
		fs: &jobFileSys{log: &writeLog{}, inst: parent.req.InstanceID},
	}
	jr.jobSession = "job-" + newID()
	_ = newSessionStore(s.bus, parent.req.InstanceID).EnsureSessionWithParent(jr.jobSession, parent.req.Session)

	actions := []dsl.Action{jr.llmAction()}
	ast, err := dsl.Parse(script, actions)
	if err != nil {
		s.tasks.done(node.TaskID, TaskStateError, "", err.Error())
		parent.FeedToolResultStatus(toolCallID, "错误: "+err.Error(), "failed")
		return
	}
	// 参数级路径违规（R-11 二次升级）：DSL 内**字面** `#"path"` 相对路径在执行前即拒绝
	// （顶层失败，带行号/用途）；含 {{}} 插值的路径跳过（执行时由 jobFile.abs 严格解析兜底）。
	for _, ref := range dsl.CollectHandleRefs(ast) {
		if strings.Contains(ref.Path, "{{") {
			continue
		}
		if _, msg := paths.ResolvePathFor(parent.req.InstanceID, ref.Path, ""); msg != "" {
			perr := fmt.Errorf("第 %d 行 %s：%s", ref.Line, ref.Desc, msg)
			s.tasks.done(node.TaskID, TaskStateError, "", perr.Error())
			parent.FeedToolResultStatus(toolCallID, "错误: "+perr.Error(), "failed")
			return
		}
	}
	// 参数级违规：LLM 指令参数超过 3 个（agent / 提示词 / [目的]）→ 顶层失败（不执行任何步骤）。
	if aerr := checkLLMArgs(ast.Stmts); aerr != nil {
		s.tasks.done(node.TaskID, TaskStateError, "", aerr.Error())
		parent.FeedToolResultStatus(toolCallID, "错误: "+aerr.Error(), "failed")
		return
	}
	// 宿主只读 env（与 mcp-tools 同名同值）：tempdir 按 instance 分目录，供 {{env.CHONKPILOT_WORKDIR}} 等显式拼绝对路径。
	// instance 为空 = 异常（R-11 二次升级：不再回落 default）。2026-09-19 缺口 4：改为**按 instance 取**
	// （TempRootFor，不覆盖进程「当前根」）——多 instance 同进程各自独立根，互不覆盖。
	tempDir, tempErr := paths.TempRootFor(parent.req.InstanceID)
	if tempErr != nil {
		s.tasks.done(node.TaskID, TaskStateError, "", tempErr.Error())
		parent.FeedToolResultStatus(toolCallID, "错误: "+tempErr.Error(), "failed")
		return
	}
	exeDir := ""
	if d, derr := exedir.Dir(); derr == nil {
		exeDir = d
	}
	eng := dsl.NewEngine(dsl.Options{Files: jr.fs, Actions: actions, Vars: jobEnvVars(jr.workDir, parent.req.DataDir, tempDir, exeDir)})
	stopWatch := jr.watchCancel(eng)
	_ = eng.Execute(ast)
	close(stopWatch)

	// DSL 运行时错误（SET/表达式/句柄等非 LLM 步骤失败，已入 Result.Errors）必须上报——
	// 否则作业回填文本为空/误导（如"全部跳过"），LLM 与用户均无从感知（I-19）。
	runErrs := eng.Result().Errors
	if len(runErrs) > 0 {
		logf("[llm_run] DSL 运行时错误（作业 %s）：共 %d 条，首条 第 %d 行：%s\n",
			node.TaskID, len(runErrs), runErrs[0].Line, runErrs[0].Msg)
	}

	summary := jr.buildSummary(runErrs)
	s.tasks.done(node.TaskID, TaskStateDone, summary, "")
	parent.FeedToolResult(toolCallID, summary)
}

// jobEnvVars 构造 llm_run 的 DSL 只读 env（与 mcp-tools 的 fileops 同名同值）：
// 根作用域只注入 env 一个对象，脚本只能以 {{env.<NAME>}} 引用（无裸名别名）。
// CHONKPILOT_WORKDIR/DATADIR/TEMPDIR/EXEDIR/PROJECT（PROJECT 与 WORKDIR 同值）。
func jobEnvVars(workDir, dataDir, tempDir, exeDir string) map[string]any {
	return map[string]any{"env": map[string]any{
		"CHONKPILOT_WORKDIR": workDir,
		"CHONKPILOT_DATADIR": dataDir,
		"CHONKPILOT_TEMPDIR": tempDir,
		"CHONKPILOT_EXEDIR":  exeDir,
		"CHONKPILOT_PROJECT": workDir,
	}}
}

// jobScriptOf 取作业脚本：优先 args.script，否则读 args.file（file 须绝对 / ~/ / !/，相对路径拒绝）。
// `!/` 按作业归属 instance 的临时根解析（ResolvePathFor；缺口 4）。
func jobScriptOf(instanceID string, args map[string]any) (string, error) {
	if script := str(args["script"]); script != "" {
		return script, nil
	}
	file := str(args["file"])
	if file == "" {
		return "", fmt.Errorf("llm_run: script/file 至少提供其一")
	}
	p, msg := paths.ResolvePathFor(instanceID, file, "")
	if msg != "" {
		return "", fmt.Errorf("llm_run: file：%s", msg)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("llm_run: 读脚本 %s: %w", file, err)
	}
	return string(raw), nil
}

// llmAction 返回 LLM 动作：agent/提示词/目的插值（agent 必填=委派对象标识，须为域 agent 或
// **当前场景内同名 agent**（AG-1）；提示词必填；
// 目的可选 = 本次子 LLM 的运行目的/展示名，缺省回退提示词截断）→ 建子任务节点（kind=llm，
// ParentID=根节点；Purpose/Name/Simplified = 展示名）→ runChildTurn（agent 传递入子轮 persona；
// 子轮次在 newTurnCtx 按该 agent 定义执行：提示词 / 工具白名单 / llmRef / 委派条件，AG-1）
// → 节点 done/error。
// 返回文本交给 dsl 引擎：无 => 目标进引擎汇总；有 => 目标（变量/文件/表）由引擎写入。
func (jr *jobEnv) llmAction() dsl.Action {
	return dsl.Action{
		Name: "LLM",
		Run: func(sc *dsl.Scope, args string) (string, error) {
			if jr.cancelled() {
				return "", dsl.ErrExit
			}
			parts, err := dsl.SplitArgs(args)
			if err != nil {
				return "", err
			}
			if len(parts) < 2 {
				return "", fmt.Errorf("LLM 需要 \"agent\" \"prompt\"（语法：LLM \"agent\" \"prompt\" 或 LLM \"agent\", \"prompt\" => #\"path\"）")
			}
			agent, _ := sc.Interp(parts[0])
			prompt, _ := sc.Interp(parts[1])
			agent = strings.TrimSpace(agent)
			if agent == "" {
				return "", fmt.Errorf("LLM agent 不能为空（语法：LLM \"agent\" \"prompt\" 或 LLM \"agent\", \"prompt\" => #\"path\"）")
			}
			if !jr.s.agentDelegable(jr.node.InstanceID, jr.parent.req.ScenarioID, agent) {
				return "", fmt.Errorf("LLM agent %q 不可委派（域 agent 注册表与当前场景内均无此 agent）；可委派成员见系统提示词的团队成员段", agent)
			}
			if strings.TrimSpace(prompt) == "" {
				return "", fmt.Errorf("LLM 提示词不能为空（语法：LLM \"agent\" \"prompt\" 或 LLM \"agent\", \"prompt\" => #\"path\"）")
			}
			session := fmt.Sprintf("%s-%d", jr.jobSession, jr.nextStep())
			label := promptLabel(prompt) // 缺省展示名 = 提示词截断
			if len(parts) == 3 {
				// 第三参 = 目的（运行目的/展示名）：插值后非空才覆盖，否则回退提示词截断。
				if purpose, _ := sc.Interp(parts[2]); strings.TrimSpace(purpose) != "" {
					label = promptLabel(purpose)
				}
			}
			child := &TaskNode{
				Tool: jr.node.Tool, ToolCallID: jr.node.ToolCallID, ParentID: jr.node.TaskID,
				TopSession: jr.node.TopSession, Kind: TaskKindLLM, SessionID: session,
				InstanceID: jr.node.InstanceID, Purpose: label,
			}
			child.Name, child.Simplified = label, label
			childNode, serr := jr.s.tasks.start(child)
			if serr != nil {
				jr.markFail()
				return "", nil
			}

			// 子轮次 ctx 由**发起它的本 turn 的 ctx** 派生（G-18 gapA）：父轮次取消/关闭
			// （用户停止、级联取消）时取消信号沿链下传，子步不再跑满。
			text, subTurn, runErr := jr.s.runChildTurn(jr.parent.ctx, jr.parent.req, session, prompt, agent)
			jr.s.tasks.update(childNode.TaskID, func(n *TaskNode) { n.TurnID = subTurn })
			if runErr != nil {
				jr.s.tasks.done(childNode.TaskID, TaskStateError, "", runErr.Error())
				jr.markFail()
				return "", nil // 失败记错不阻塞（后续步骤继续）
			}
			jr.s.tasks.done(childNode.TaskID, TaskStateDone, text, "")
			return text, nil
		},
	}
}

// promptLabel 展示名文本（提示词 / LLM 目的共用）→ 子任务节点展示名（≤24 字符，超出截断加 …）。
func promptLabel(prompt string) string {
	r := []rune(strings.TrimSpace(prompt))
	if len(r) <= 24 {
		return string(r)
	}
	return string(r[:24]) + "…"
}

// errLLMTooManyArgs LLM 指令参数过多（>3）的统一错误（严格模式；含正确语法示例）。
var errLLMTooManyArgs = errors.New(`LLM 参数过多（最多 3 个：agent、提示词、目的）（语法：LLM "agent" "prompt" ["目的"] [=> 目标]）`)

// checkLLMArgs 执行前静态校验脚本内所有 LLM 指令的参数个数（参数级违规：>3 → 顶层失败，
// 与字面相对路径预校验同层）：参数个数与插值无关，故可在执行前判定；参数不足 / 分词错误
// 交由执行期 llmAction 报出。
func checkLLMArgs(sts []dsl.Stmt) error {
	for _, st := range sts {
		switch t := st.(type) {
		case *dsl.ActionStmt:
			if !strings.EqualFold(t.Verb, "LLM") {
				continue
			}
			parts, err := dsl.SplitArgs(t.Args)
			if err != nil || len(parts) <= 3 {
				continue
			}
			return fmt.Errorf("第 %d 行 %s", t.Line(), errLLMTooManyArgs)
		case *dsl.IfStmt:
			if err := checkLLMArgs(t.Block); err != nil {
				return err
			}
		case *dsl.LoopStmt:
			if err := checkLLMArgs(t.Block); err != nil {
				return err
			}
		case *dsl.ParallelStmt:
			if err := checkLLMArgs(t.Block); err != nil {
				return err
			}
		}
	}
	return nil
}

// watchCancel 监视作业根节点：被级联取消（tool_stop/会话终止）时中止 dsl 引擎（跨分支）。
func (jr *jobEnv) watchCancel(eng *dsl.Engine) chan struct{} {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				n := jr.s.tasks.nodeIn(jr.parent.req.InstanceID, jr.node.TaskID)
				if n != nil && n.State == TaskStateCancelled {
					eng.Cancel()
					return
				}
			}
		}
	}()
	return done
}

func (jr *jobEnv) cancelled() bool {
	n := jr.s.tasks.nodeIn(jr.parent.req.InstanceID, jr.node.TaskID)
	return n != nil && n.State == TaskStateCancelled
}

func (jr *jobEnv) markFail() {
	jr.mu.Lock()
	jr.failed++
	jr.mu.Unlock()
}

func (jr *jobEnv) nextStep() int {
	jr.mu.Lock()
	jr.step++
	n := jr.step
	jr.mu.Unlock()
	return n
}

// buildSummary 汇总：LLM 子节点步骤行 + 文件写日志 + 失败统计 / 全跳过兜底 +
// DSL 运行时错误（runErrs，非 LLM 步骤失败；I-19：必须可见，不得静默丢弃）。
func (jr *jobEnv) buildSummary(runErrs []dsl.RunError) string {
	var lines []string
	llmSteps := 0
	for _, n := range jr.s.tasks.subtree(jr.parent.req.InstanceID, jr.node.TaskID) {
		if n.ParentID != jr.node.TaskID || n.Kind != TaskKindLLM {
			continue
		}
		llmSteps++
		label := n.Purpose
		if label == "" {
			label = n.Name
		}
		switch n.State {
		case TaskStateDone:
			if n.ResultSummary != "" {
				lines = append(lines, fmt.Sprintf("【%s】%s", label, n.ResultSummary))
			}
		case TaskStateError:
			lines = append(lines, fmt.Sprintf("【%s】失败: %s", label, n.Error))
		}
	}
	lines = append(lines, jr.fs.log.snapshot()...)
	for _, e := range runErrs {
		lines = append(lines, fmt.Sprintf("【DSL 错误】第 %d 行：%s", e.Line, e.Msg))
	}
	if len(lines) == 0 {
		if llmSteps > 0 {
			return "作业执行完成（无文本产出）"
		}
		return "本次全部跳过：断点续跑命中（或条件未命中），无新产出"
	}
	return strings.Join(lines, "\n")
}
