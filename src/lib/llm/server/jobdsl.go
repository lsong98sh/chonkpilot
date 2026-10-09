// llm_run 委派/编排统一 DSL 执行器：接入 chonkpilot-lib/dsl 核心包（docs/dsl-core.md +
// 3A-工具与编排）。阶段 1/2 收敛后 llm 域工具唯一 llm 型 = llm_run——旧单次/批量
// 委派工具已删除，其语义并入本 DSL：脚本/file 由作业级参数承接，单次/批量委派 =
// 一行/多行 LLM 指令。
//
// llm_run 脚本使用 dsl-core 语法：核心保留字 SET/IF/LOOP/PARALLEL/BREAK/CONTINUE/EXIT/END
// + 动作动词 LLM（本文件注入）。LLM 指令：LLM "agent" "prompt" "目的" [=> 目标]，
// agent=委派对象名（必填，须**可委派** —— 当前场景内成员，或 app 级场景内唯一同名 agent；
// 见 agentDelegable）、prompt=委派提示词（必填，{{}} 插值）、
// 目的=本次子 LLM 的运行目的（**必填且非空**，即子任务节点展示名；软约束 10–20 字 ——
// 超长截断为 20 字并记录、不足仅记录，均不报错）；
// 三参均可 {{}} 插值；参数间空白或逗号分隔均兼容；参数个数 ≠ 3 = 顶层失败
// （缺参 / 空串 / 超过 3 个均顶层失败）。文件路径须绝对 / ~/ 开头 / !/ 开头，
// 项目内路径用宿主注入的 {{env.CHONKPILOT_WORKDIR}} 显式拼接（相对路径拒绝）。示例：
//
//	SET #"{{env.CHONKPILOT_WORKDIR}}/tasks.json" => src
//	LOOP item=src.array concurrency=3
//	   IF item.done != true
//	      LLM "后端开发" "实现 {{item.name}}" "实现 {{item.name}} 模块" => #"{{env.CHONKPILOT_WORKDIR}}/out/{{item.name}}.py"
//	      SET item.done => true
//	   END
//	END
//
// 执行：每个 LLM 步骤 = 独立 stateless 子轮次（runChildTurn，父会话链 + 任务树）；
// 子任务节点（kind=llm，ParentID=根节点）展示名 = 目的（第三参，必填）按 10–20 字软约束处理；
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

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
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

// absRead 解析路径 + 沙箱读校验（越界 → 错误；决策 42 §2 (250)：修复 llm 引擎句柄层裸写缺口）。
func (f *jobFile) absRead() (string, error) {
	abs, err := f.abs()
	if err != nil {
		return "", err
	}
	if err := agentbox.Check(abs, false); err != nil {
		return "", err
	}
	return abs, nil
}

// absWrite 解析路径 + 沙箱写校验（越界 → 错误）。
func (f *jobFile) absWrite() (string, error) {
	abs, err := f.abs()
	if err != nil {
		return "", err
	}
	if err := agentbox.Check(abs, true); err != nil {
		return "", err
	}
	return abs, nil
}

func (f *jobFile) Exists() bool {
	abs, err := f.absRead()
	if err != nil {
		return false
	}
	_, serr := os.Stat(abs)
	return serr == nil
}

func (f *jobFile) Stat() (dsl.FileInfo, error) {
	abs, err := f.absRead()
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
	abs, err := f.absRead()
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
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.absWrite()
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
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.absWrite()
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
	p, err := f.absWrite()
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

	// ── DSL-3 展示（不随迭代增长）──
	// execSteps 是步骤执行记录（跨迭代累计，扁平）；stepContainer / containerLead 由
	// buildStaticTree 预走 AST 得到（LLM 原始参数 → 所属静态节点 id / 容器首步原始参数）；
	// containerIDs 是静态容器节点 id（作业结束标记终态）；sessions 是各步骤子会话 id
	// （作业结束清理 subParents 映射）。
	execSteps     []dslExecStep
	stepContainer map[string]string
	containerLead map[string]string
	leadCount     map[string]int
	containerIDs  []string
	sessions      []string
}

// dslExecStep 是一次 LLM 步骤执行的内部记录（对外投影为 DslStepRecord + 结果文本供汇总）。
type dslExecStep struct {
	rec    DslStepRecord
	text   string // 成功产出的子轮文本（供回落汇总）
	errMsg string // 失败信息（供回落汇总）
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
		fs:            &jobFileSys{log: &writeLog{}, inst: parent.req.InstanceID},
		stepContainer: map[string]string{},
		containerLead: map[string]string{},
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
	// 作业启动兜底：清理临时根内陈旧的 `$RETURN` 落盘文件（防常驻进程下按作业线性累积；
	// 取消路径的残留也在此被下次作业清掉；仅删陈旧文件，保留近期结果，C-43）。
	if n := paths.SweepStaleReturnFiles(tempDir, 24*time.Hour); n > 0 {
		logf("[llm_run] swept %d stale $RETURN files under %s\n", n, tempDir)
	}
	exeDir := ""
	if d, derr := exedir.Dir(); derr == nil {
		exeDir = d
	}
	// DSL-3：按 AST 预建**静态语句树**（容器节点，不随迭代增长；LLM 步骤登记到所属静态节点）。
	jr.buildStaticTree(ast.Stmts)
	// 存在 $RETURN 时，超 64K 累计落盘到本 instance 临时根（DSL-2 宿主接线）。
	returnFile := filepath.Join(tempDir, "dsl-return-"+node.TaskID+".md")
	eng := dsl.NewEngine(dsl.Options{
		Files: jr.fs, Actions: actions,
		Vars:       jobEnvVars(jr.workDir, parent.req.DataDir, tempDir, exeDir),
		ReturnFile: returnFile,
	})
	stopWatch := jr.watchCancel(eng)
	_ = eng.Execute(ast)
	close(stopWatch)

	// DSL 运行时错误必须上报——否则作业回填文本为空/误导（如"全部跳过"），LLM 与用户均无从感知（I-19）。
	// 来源有二：① SET/表达式/句柄等**非 LLM 步骤**失败；② **LLM 步骤的参数预检失败**
	// （agent 空 / 不可委派 / 提示词空，见本文件 llmAction）与无参 LOOP 的上限/失败终止——
	// 二者同走 execSeq → addErr → Result.Errors（引擎 StopOnError 缺省 false：记错并继续后续步骤）。
	runErrs := eng.Result().Errors
	// $RETURN 结果通道（DSL-2）：Used → 取代汇总（inline 内容 / file 文件名+大小）；未用 → 回落既有汇总。
	summary := jr.buildSummary(runErrs, eng.Return())
	jr.finishStatic(runErrs)
	jr.markReturn(eng.Return())
	if len(runErrs) > 0 {
		logf("[llm_run] DSL 运行时错误（作业 %s）：共 %d 条，首条 第 %d 行：%s\n",
			node.TaskID, len(runErrs), runErrs[0].Line, runErrs[0].Msg)
		// B-19：存在运行时错误 → 作业节点落**失败终态**、结果按 failed 回填（不再误报 success）。
		s.tasks.done(node.TaskID, TaskStateError, summary, runErrs[0].Msg)
		parent.FeedToolResultStatus(toolCallID, summary, "failed")
		return
	}
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

// llmAction 返回 LLM 动作：agent/提示词/目的插值（三参均必填 —— agent=委派对象标识，须为域 agent 或
// **当前场景内同名 agent**（AG-1）；提示词必填；目的必填且非空 = 本次子 LLM 的运行目的/展示名，
// 软约束 10–20 字）→ runChildTurn（agent 传递入子轮 persona；子轮次在 newTurnCtx 按该 agent 定义
// 执行：提示词 / 工具白名单 / llmRef / 委派条件，AG-1）。
//
// DSL-3：执行记录**不建树节点**，改为追加到作业根节点 `steps[]`（扁平、跨迭代累计）；子会话内
// 产生的工具节点经 `bindSubSession` 挂到该步所属**静态语句节点**（容器 dsl_loop/dsl_parallel 或作业根）。
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
			if len(parts) < 3 {
				// 正常路径下 checkLLMArgs 已在执行前拦下缺参；此处为执行期兜底。
				return "", errLLMMissingArgs
			}
			agent, _ := sc.Interp(parts[0])
			prompt, _ := sc.Interp(parts[1])
			purpose, _ := sc.Interp(parts[2])
			agent = strings.TrimSpace(agent)
			if agent == "" {
				return "", fmt.Errorf("LLM agent 不能为空（语法：LLM \"agent\" \"prompt\" \"目的\"）")
			}
			if !jr.s.agentDelegable(jr.node.InstanceID, jr.parent.req.ScenarioID, agent) {
				return "", fmt.Errorf("LLM agent %q 不可委派（app 级场景注册表与当前场景内均无此 agent）；可委派成员见系统提示词的团队成员段", agent)
			}
			if strings.TrimSpace(prompt) == "" {
				return "", fmt.Errorf("LLM 提示词不能为空（语法：LLM \"agent\" \"prompt\" \"目的\"）")
			}
			// 目的非空硬校验（插值后仍为空 → 该步失败；字面空串在执行前已由 checkLLMArgs 拦下）。
			if strings.TrimSpace(purpose) == "" {
				return "", fmt.Errorf("LLM 目的不能为空（语法：LLM \"agent\" \"prompt\" \"目的\"；目的 = 子任务展示名，建议 10–20 字）")
			}
			session := fmt.Sprintf("%s-%d", jr.jobSession, jr.nextStep())
			label := purposeLabel(purpose) // 展示名 = 目的（10–20 字软约束：超长截断、不足记录）
			// 静态语句节点（容器或作业根）：子会话内工具节点据此挂父（DSL-3，执行记录不建树节点）。
			parentID := jr.stepParent(args)
			jr.s.tasks.bindSubSession(session, parentID)
			// 追加一条 running 执行记录（跨迭代累计，不建树节点）+ 刷新容器进度。
			idx := jr.beginStep(session, label, args)

			// 子轮次 ctx 由**发起它的本 turn 的 ctx** 派生（G-18 gapA）：父轮次取消/关闭
			// （用户停止、级联取消）时取消信号沿链下传，子步不再跑满。
			start := time.Now()
			text, _, runErr := jr.s.runChildTurn(jr.parent.ctx, jr.parent.req, session, prompt, agent)
			elapsed := time.Since(start).Milliseconds()
			if runErr != nil {
				status := TaskStateError
				if jr.cancelled() {
					status = TaskStateCancelled
				}
				jr.endStep(idx, status, elapsed, "", runErr.Error())
				jr.markFail()
				return "", nil // 失败记错不阻塞（后续步骤继续）
			}
			jr.endStep(idx, TaskStateDone, elapsed, text, "")
			return text, nil
		},
	}
}

// stepParent 取该 LLM 语句所属静态节点 id（容器 dsl_loop/dsl_parallel，或作业根 dsl_job）。
// 未登记（AST 预走后仍在 map 外，异常）→ 回落作业根。
func (jr *jobEnv) stepParent(rawArgs string) string {
	if pid := jr.stepContainer[rawArgs]; pid != "" {
		return pid
	}
	return jr.node.TaskID
}

// beginStep 追加一条 running 执行记录（不建树节点）→ 刷新作业根 `steps[]` + 容器进度；
// 返回记录下标（供 endStep 回填）。
func (jr *jobEnv) beginStep(session, purpose, rawArgs string) int {
	jr.mu.Lock()
	jr.execSteps = append(jr.execSteps, dslExecStep{rec: DslStepRecord{
		No: len(jr.execSteps) + 1, Status: TaskStateRunning, Purpose: purpose,
		CreatedAt: time.Now().UTC().Format(time.RFC3339), SessionID: session,
	}})
	idx := len(jr.execSteps) - 1
	if jr.leadCount == nil {
		jr.leadCount = map[string]int{}
	}
	jr.leadCount[rawArgs]++
	jr.sessions = append(jr.sessions, session)
	steps := jr.stepsLocked()
	jr.mu.Unlock()
	jr.s.tasks.update(jr.node.TaskID, func(n *TaskNode) { n.Steps = steps })
	jr.applyContainerProgress()
	return idx
}

// endStep 回填执行记录终态（status/耗时/文本/错误）并广播作业根 `steps[]`。
func (jr *jobEnv) endStep(idx int, status string, elapsed int64, text, errMsg string) {
	jr.mu.Lock()
	if idx >= 0 && idx < len(jr.execSteps) {
		jr.execSteps[idx].rec.Status = status
		jr.execSteps[idx].rec.ElapsedMs = elapsed
		jr.execSteps[idx].text = text
		jr.execSteps[idx].errMsg = errMsg
	}
	steps := jr.stepsLocked()
	jr.mu.Unlock()
	jr.s.tasks.update(jr.node.TaskID, func(n *TaskNode) { n.Steps = steps })
}

// stepsLocked 取执行记录快照（持 jr.mu 调用）。
func (jr *jobEnv) stepsLocked() []DslStepRecord {
	out := make([]DslStepRecord, len(jr.execSteps))
	for i, s := range jr.execSteps {
		out[i] = s.rec
	}
	return out
}

// applyContainerProgress 把容器首步的执行计数投影为 `loop_current`（1 起；不随迭代新增节点）。
func (jr *jobEnv) applyContainerProgress() {
	jr.mu.Lock()
	type upd struct {
		cid   string
		count int
	}
	var ups []upd
	for cid, lead := range jr.containerLead {
		if lead == "" {
			continue
		}
		if c := jr.leadCount[lead]; c > 0 {
			ups = append(ups, upd{cid, c})
		}
	}
	jr.mu.Unlock()
	for _, u := range ups {
		u := u
		jr.s.tasks.update(u.cid, func(n *TaskNode) { n.LoopCurrent = u.count })
	}
}

// markReturn 把 `$RETURN` 两态投影到作业根节点（DSL-2：inline 全文 / file 文件名 + 字节数）；
// 未使用 → 不写任何字段（与既有载荷逐字节等价）。
func (jr *jobEnv) markReturn(rv dsl.ReturnValue) {
	if !rv.Used {
		return
	}
	jr.s.tasks.update(jr.node.TaskID, func(n *TaskNode) {
		if rv.Overflow {
			n.ReturnKind = "file"
			n.ReturnFile = filepath.Base(rv.File)
			n.ReturnSize = rv.Size
			return
		}
		n.ReturnKind = "inline"
		n.ReturnInline = rv.Text
	})
}

// finishStatic 收尾静态容器节点（标终态）+ 清理子会话→静态节点映射（DSL-3）。
func (jr *jobEnv) finishStatic(runErrs []dsl.RunError) {
	state := TaskStateDone
	if jr.cancelled() {
		state = TaskStateCancelled
	} else if len(runErrs) > 0 || jr.failedCount() > 0 {
		state = TaskStateError
	}
	for _, cid := range jr.containerIDs {
		jr.s.tasks.done(cid, state, "", "")
	}
	jr.s.tasks.unbindSubSessions(jr.sessions)
}

// failedCount 取失败步骤数（并发安全）。
func (jr *jobEnv) failedCount() int {
	jr.mu.Lock()
	defer jr.mu.Unlock()
	return jr.failed
}

// buildStaticTree 预走 AST 建**静态语句树**：LOOP/PARALLEL → 折叠容器节点（各建一次，
// **不随迭代增长**）；LLM 语句登记到其所属容器（或作业根）。每次迭代的执行记录一律入作业根
// `steps[]`，不建树节点 —— DSL-3「节点=静态语句」（本实现选 steps[] 方案，故不产出
// shadow/dsl_step 节点；前端 stepsFromNodes 对 job.steps 为首选）。
func (jr *jobEnv) buildStaticTree(stmts []dsl.Stmt) {
	_ = jr.walkStatic(stmts, jr.node.TaskID)
}

// walkStatic 递归预走：返回该层**首个 LLM 语句的原始参数**（供容器 lead 判定 loop_current）。
func (jr *jobEnv) walkStatic(stmts []dsl.Stmt, parentID string) string {
	var lead string
	for _, st := range stmts {
		switch t := st.(type) {
		case *dsl.LoopStmt:
			cid := jr.mkContainer(TaskKindDslLoop, loopLabel(t), parentID)
			inner := jr.walkStatic(t.Block, cid)
			if inner != "" && jr.containerLead[cid] == "" {
				jr.containerLead[cid] = inner
			}
			if lead == "" {
				lead = inner
			}
		case *dsl.ParallelStmt:
			cid := jr.mkContainer(TaskKindDslParallel, "PARALLEL", parentID)
			inner := jr.walkStatic(t.Block, cid)
			if inner != "" && jr.containerLead[cid] == "" {
				jr.containerLead[cid] = inner
			}
			if lead == "" {
				lead = inner
			}
		case *dsl.IfStmt:
			// IF 透明：块内语句挂当前容器（DSL-3 未定义 dsl_if；不新增 kind）
			if inner := jr.walkStatic(t.Block, parentID); inner != "" && lead == "" {
				lead = inner
			}
		case *dsl.ActionStmt:
			if strings.EqualFold(t.Verb, "LLM") {
				jr.stepContainer[t.Args] = parentID
				if lead == "" {
					lead = t.Args
				}
			}
		}
	}
	return lead
}

// mkContainer 建一个折叠容器节点（kind=dsl_loop/dsl_parallel，静态、各建一次）；启动失败回落父节点。
func (jr *jobEnv) mkContainer(kind, label, parentID string) string {
	c := &TaskNode{
		Tool: jr.node.Tool, ToolCallID: jr.node.ToolCallID, ParentID: parentID,
		TopSession: jr.node.TopSession, Kind: kind, SessionID: jr.node.SessionID,
		TurnID: jr.node.TurnID, InstanceID: jr.node.InstanceID, Name: label,
	}
	c.Simplified = label
	n, err := jr.s.tasks.start(c)
	if err != nil {
		return parentID // 启动失败（活跃上限等）→ 挂父，不阻塞作业
	}
	jr.containerIDs = append(jr.containerIDs, n.TaskID)
	return n.TaskID
}

// loopLabel 容器展示名（LOOP <迭代变量> / LOOP）。
func loopLabel(t *dsl.LoopStmt) string {
	if t.Var != "" {
		return "LOOP " + t.Var
	}
	return "LOOP"
}

// 目的软约束区间（10–20 字）：仅约束子任务节点展示名的可读性，超出不报错。
const (
	purposeMinLen = 10
	purposeMaxLen = 20
)

// purposeLabel 目的 → 子任务节点展示名（tasktree label）：软约束 10–20 字——
// 超长（>20 字）截断为 20 字并加 …（记录日志）；不足 10 字仅记录日志（不补全、不报错）。
func purposeLabel(purpose string) string {
	r := []rune(strings.TrimSpace(purpose))
	n := len(r)
	if n > purposeMaxLen {
		logf("[llm_run] LLM 目的超长（%d 字 > %d），展示名已截断：%q\n", n, purposeMaxLen, purpose)
		return string(r[:purposeMaxLen]) + "…"
	}
	if n < purposeMinLen {
		logf("[llm_run] LLM 目的过短（%d 字 < %d），建议 10–20 字：%q\n", n, purposeMinLen, purpose)
	}
	return string(r)
}

// errLLMTooManyArgs / errLLMMissingArgs LLM 指令参数个数违规（≠3）的统一错误（严格模式：顶层失败；
// 含正确语法示例）。
var (
	errLLMTooManyArgs = errors.New(`LLM 参数过多（最多 3 个：agent、提示词、目的）（语法：LLM "agent" "prompt" "目的" [=> 目标]）`)
	errLLMMissingArgs = errors.New(`LLM 需要 3 个参数：agent、提示词、目的（目的必填且非空）（语法：LLM "agent" "prompt" "目的" [=> 目标]）`)
)

// checkLLMArgs 执行前静态校验脚本内所有 LLM 指令（参数级违规：个数 ≠ 3 或任一参数为空串 →
// 顶层失败，与字面相对路径预校验同层）：参数个数/字面空串与插值无关，故可在执行前判定；
// 插值后为空（如 {{item.x}} 解析为空）交由执行期 llmAction 报出。
func checkLLMArgs(sts []dsl.Stmt) error {
	for _, st := range sts {
		switch t := st.(type) {
		case *dsl.ActionStmt:
			if !strings.EqualFold(t.Verb, "LLM") {
				continue
			}
			parts, err := dsl.SplitArgs(t.Args)
			if err != nil {
				continue // 分词错误交由执行期 llmAction 报出
			}
			if len(parts) > 3 {
				return fmt.Errorf("第 %d 行 %s", t.Line(), errLLMTooManyArgs)
			}
			if len(parts) < 3 {
				return fmt.Errorf("第 %d 行 %s", t.Line(), errLLMMissingArgs)
			}
			for i, argName := range [...]string{"agent", "提示词", "目的"} {
				if strings.TrimSpace(parts[i]) == "" {
					return fmt.Errorf("第 %d 行 LLM %s 不能为空（语法：LLM \"agent\" \"prompt\" \"目的\" [=> 目标]）", t.Line(), argName)
				}
			}
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
// 并**ctx 感知**（B3）：父轮次 ctx（`parent.ctx`）取消（用户停止 / 级联取消）时立即中止引擎，
// 不依赖 200ms 轮询的节点态（子步骤的阻塞等待随之中断）。
func (jr *jobEnv) watchCancel(eng *dsl.Engine) chan struct{} {
	done := make(chan struct{})
	// 父轮次 ctx 可能为 nil（裸构造的 turnCtx，如部分单测）→ 不取 Done（该分支永不命中）。
	var parentDone <-chan struct{}
	if jr.parent.ctx != nil {
		parentDone = jr.parent.ctx.Done()
	}
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-parentDone:
				eng.Cancel()
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

// buildSummary 汇总（DSL-2 + DSL-3）：
//   - `$RETURN` 已用（rv.Used）→ **取代**汇总：overflow → `<file: 文件名, size: N>`；inline → 全文；
//   - 未用 → 回落既有口径：步骤执行记录（LLM 子步结果/失败）+ 文件写日志 + 失败统计 / 全跳过兜底 +
//     DSL 运行时错误（runErrs，非 LLM 步骤失败；I-19：必须可见，不得静默丢弃）。
func (jr *jobEnv) buildSummary(runErrs []dsl.RunError, rv dsl.ReturnValue) string {
	if rv.Used {
		if rv.Overflow {
			return fmt.Sprintf("<file: %s, size: %d>", filepath.Base(rv.File), rv.Size)
		}
		return rv.Text
	}
	jr.mu.Lock()
	steps := append([]dslExecStep{}, jr.execSteps...)
	jr.mu.Unlock()
	var lines []string
	for _, s := range steps {
		label := s.rec.Purpose
		switch s.rec.Status {
		case TaskStateDone:
			if s.text != "" {
				lines = append(lines, fmt.Sprintf("【%s】%s", label, s.text))
			}
		case TaskStateError, TaskStateCancelled:
			lines = append(lines, fmt.Sprintf("【%s】失败: %s", label, s.errMsg))
		}
	}
	lines = append(lines, jr.fs.log.snapshot()...)
	for _, e := range runErrs {
		lines = append(lines, fmt.Sprintf("【DSL 错误】第 %d 行：%s", e.Line, e.Msg))
	}
	if len(lines) == 0 {
		if len(steps) > 0 {
			return "作业执行完成（无文本产出）"
		}
		return "本次全部跳过：断点续跑命中（或条件未命中），无新产出"
	}
	return strings.Join(lines, "\n")
}
