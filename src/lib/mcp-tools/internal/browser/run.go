package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
	"github.com/chonkpilot/chonkpilot-lib/dsl"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/dslfs"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/fileops"
)

// Options 是 browser_run 执行入参。
type Options struct {
	Script       string
	DomFile      string
	ConsoleFile  string
	FailShot     string
	WatTimeoutMs int64
	Headless     bool
	ChromePath   string         // 空 = 自动探测
	Vars         map[string]any // 宿主只读 env（dsl.Options.Vars，供 {{env.*}} 插值）
}

// Runner 承载一次脚本执行。
type Runner struct {
	opt Options

	parent  context.Context // 惰性启动的父上下文（Execute 传入；缺省 context.Background()）
	started bool            // 浏览器是否已启动（惰性：首个浏览器动作执行时才启动）
	failed  atomic.Bool     // 本次会话是否出现过动作失败（供 Close 判定失败截图）

	// runtime
	ctx         context.Context // 当前激活 tab
	browserCtx  context.Context // browser 会话（target 管理）
	curID       string          // 当前激活 tab target id
	out         []string        // 过程输出（TAB list 等）
	cancelCtx   context.CancelFunc
	cancelAlloc context.CancelFunc
	console     []string
	closed      bool
}

// stepCtx 构造带超时的上下文（WAT 用）。
func stepCtx(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

// Execute 解析 + 执行整个脚本。返回 nil 成功，否则 *StepError 定位失败指令。
// 脚本语法 = ChonkPilot DSL 核心（dsl-core.md）：全部 browser 动词注册为 Raw 动作
// （参数原文透传，EVL/EXP 多行 JS 用 "… <<< ... >>>" 引号内多行表达），并因此获得
// 核心流控 SET/IF/LOOP/PARALLEL/BREAK/CONTINUE/EXIT、变量与 {{}} 插值。首错即停。
func Execute(parent context.Context, opt Options) (string, error) {
	sess, _ := NewSession(opt)
	sess.parent = parent
	r := sess.r
	actions := sess.Actions()
	ast, err := dsl.Parse(opt.Script, actions)
	if err != nil {
		return "", stepErr(0, "", "syntax", "解析失败: "+err.Error())
	}
	if len(ast.Stmts) == 0 {
		return "", stepErr(0, "", "syntax", "脚本为空")
	}
	// 惰性启动：不在执行前启动浏览器；首个浏览器动作执行时才启动（脚本无浏览器动作则完全不启动）。
	eng := dsl.NewEngine(dsl.Options{Files: sess.Files(), Actions: actions, StopOnError: true, Vars: opt.Vars})
	_ = eng.Execute(ast)
	res := eng.Result()
	r.teardown(len(res.Errors) > 0)
	if len(res.Errors) > 0 {
		e0 := res.Errors[0]
		return strings.Join(r.out, "\n"), stepErr(e0.Line, "", "step", e0.Msg)
	}
	return strings.Join(r.out, "\n"), nil
}

// startBrowser 启动 Chrome/CDP 生命周期。
func (r *Runner) startBrowser(parent context.Context) (retErr error) {
	path := r.opt.ChromePath
	if path == "" {
		path = findBrowserPath()
	}
	if path == "" {
		return stepErr(0, "", "no_chrome", "未找到 Chrome/Edge，请安装或设置环境变量 CHONK_CHROME 指向 chrome.exe")
	}
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(path),
		chromedp.Flag("headless", r.opt.Headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("disable-background-timer-throttling", true),
	)
	if !r.opt.Headless {
		allocOpts = append(allocOpts, chromedp.Flag("headless", false), chromedp.Flag("start-maximized", true))
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(parent, allocOpts...)
	r.cancelAlloc = cancelAlloc
	bCtx, cancelB := chromedp.NewContext(allocCtx)
	r.browserCtx = bCtx
	r.ctx, r.cancelCtx = bCtx, cancelB
	chromedp.ListenTarget(bCtx, r.onTargetEvent)
	return nil
}

// ensureStarted 惰性启动 Chrome/CDP：仅首个浏览器动作执行时调用；会话构造与脚本解析阶段
// 不启动浏览器、不创建 chromedp 上下文（脚本无浏览器动作时不产生任何浏览器副作用）。
func (r *Runner) ensureStarted() error {
	if r.started {
		return nil
	}
	parent := r.parent
	if parent == nil {
		parent = context.Background()
	}
	if err := r.startBrowser(parent); err != nil {
		return err
	}
	r.started = true
	return nil
}

// teardown 收尾：console/DOM 落盘、失败截图、关浏览器（等价旧 run 的 defer 段）。
// 浏览器未启动（脚本无浏览器动作）时仅释放可能的 allocator，不做落盘/截图。
func (r *Runner) teardown(fail bool) {
	if r.started {
		_ = r.flushConsole()
		if fail && r.opt.FailShot != "" {
			_ = r.shotFile(r.opt.FailShot)
		}
		if r.opt.DomFile != "" {
			_ = r.domToFile(r.opt.DomFile)
		}
	}
	r.closeBrowser()
	if r.cancelAlloc != nil {
		r.cancelAlloc()
	}
}

// dslFileVerbs 是 browser DSL 中涉及本地文件的动词（R-11）：SHT/DOM/DBG 落盘、UPF 读取上传文件；
// 文件路径均为该指令的**最后一个参数**。
var dslFileVerbs = map[string]bool{"SHT": true, "DOM": true, "DBG": true, "UPF": true}

// prevalidateScript 解析脚本并预校验字面本地文件路径（R-11）；返回违规消息（空 = 通过）。
// 语法错误返回 nil（交由 Execute 报语法错）。
func prevalidateScript(script string) []string {
	ast, err := dsl.Parse(script, browserActions(&Runner{}))
	if err != nil {
		return nil
	}
	return prevalidateScriptPaths(ast)
}

// prevalidateScriptPaths 执行前预校验 DSL 内**字面**本地文件路径（R-11）：返回违规消息列表。
// 两类来源：① 动作动词参数内的落盘/取文件路径（SHT/DOM/DBG/UPF 的最后一个参数）；
// ② 核心语句的文件句柄（LOOP/SET 数据源、IF exist、访问器、行尾 `=> #"file"` 目标）——
// 经 dsl.CollectHandleRefs 收集。含 {{}} 插值的路径跳过（执行时由 checkLocalPath 兜底校验）；
// 字面相对路径据此在启动浏览器前被拒绝，不产生任何文件改动。
func prevalidateScriptPaths(script *dsl.Script) []string {
	var msgs []string
	// 核心语句的文件句柄（含 IF 条件、LOOP/SET 数据源、=> 目标）
	for _, ref := range dsl.CollectHandleRefs(script) {
		if strings.Contains(ref.Path, "{{") {
			continue
		}
		label := fmt.Sprintf("第 %d 行 %s", ref.Line, ref.Desc)
		if msg := fileops.ValidateField(label, ref.Path); msg != "" {
			msgs = append(msgs, msg)
		}
	}
	var walk func(sts []dsl.Stmt)
	walk = func(sts []dsl.Stmt) {
		for _, st := range sts {
			switch t := st.(type) {
			case *dsl.ActionStmt:
				verb := strings.ToUpper(t.Verb)
				if t.RawArgsMode && dslFileVerbs[verb] {
					toks, _ := tokenize(t.RawArgs)
					if n := len(toks); n > 0 {
						if p := toks[n-1]; !strings.Contains(p, "{{") {
							if msg := fileops.ValidateField(fmt.Sprintf("第 %d 行 %s", t.Line(), verb), p); msg != "" {
								msgs = append(msgs, msg)
							}
						}
					}
				}
			case *dsl.IfStmt:
				walk(t.Block)
			case *dsl.LoopStmt:
				walk(t.Block)
			case *dsl.ParallelStmt:
				walk(t.Block)
			}
		}
	}
	walk(script.Stmts)
	return msgs
}

// resolveLocalPath 运行时解析 DSL 内本地文件路径（R-11）：覆盖 {{}} 插值后的结果，
// 支持绝对 / ~/ / !/（临时目录）。
func resolveLocalPath(p string) (string, error) {
	return fileops.ResolveLocalPath(p)
}

// browserActions 注册全部浏览器动词为 Raw 动作：参数原文 → tokenize（与 parse.go 一致）→ execStep。
func browserActions(r *Runner) []dsl.Action {
	verbs := []string{"OPN", "WAT", "CLK", "DBL", "RCL", "HOV", "CHK", "UCHK",
		"FILL", "SELO", "KPR", "KDN", "KUP", "EXP", "EVL", "SHT",
		"DOM", "DBG", "SCL", "DRG", "UPF", "TAB", "SLP"}
	acts := make([]dsl.Action, 0, len(verbs))
	for _, v := range verbs {
		verb := v
		acts = append(acts, dsl.Action{
			Name: verb,
			Raw:  true,
			Run: func(sc *dsl.Scope, args string) (string, error) {
				toks, quoted := tokenize(args)
				for i := range toks {
					if strings.Contains(toks[i], "{{") {
						if iv, e := sc.Interp(toks[i]); e == nil {
							toks[i] = iv
						}
					}
				}
				st := &Step{Cmd: verb, Args: toks, Quoted: quoted, Raw: verb + " " + args}
				if js, ok := blockJS(args); ok {
					st.JS = js // EVL/EXP 多行（引号内 <<< ... >>>）
				}
				if err := r.execStep(st); err != nil {
					r.failed.Store(true)
					if se, ok := err.(*StepError); ok {
						return "", errors.New(se.Msg) // 行号由引擎按动作行标注
					}
					return "", err
				}
				return "", nil
			},
		})
	}
	return acts
}

// blockJS 从 "…<<< … >>>…" 多行参数提取 JS 体；无多行标记返回 false。
func blockJS(raw string) (string, bool) {
	i := strings.Index(raw, "<<<")
	if i < 0 {
		return "", false
	}
	j := strings.Index(raw[i+3:], ">>>")
	if j < 0 {
		return raw, true
	}
	j += i + 3
	pre := strings.Trim(strings.TrimSpace(raw[:i]), `"`)
	mid := strings.Trim(raw[i+3:j], "\n")
	post := strings.Trim(strings.TrimSpace(raw[j+3:]), `"`)
	var parts []string
	if pre != "" {
		parts = append(parts, pre)
	}
	if mid != "" {
		parts = append(parts, mid)
	}
	if post != "" {
		parts = append(parts, post)
	}
	return strings.Join(parts, "\n"), true
}

// scriptFS 核心语句（#"文件".lines 等）使用的校验型文件系统：句柄实现见共享包 dslfs
// （browser 档），路径强校验（R-11）与 agentbox 沙箱读写校验统一在句柄层完成。
type scriptFS struct{}

func (scriptFS) Open(path string) dsl.FileHandle { return dslfs.New(path, dslfs.Browser) }

func (r *Runner) closeBrowser() {
	if !r.closed {
		r.closed = true
		if r.cancelCtx != nil {
			r.cancelCtx()
		}
	}
}

func formatRemoteObject(o *runtime.RemoteObject) string {
	if o == nil {
		return ""
	}
	if o.Value != nil {
		if b, err := json.Marshal(o.Value); err == nil {
			return string(b)
		}
	}
	if o.Description != "" {
		return o.Description
	}
	return o.Type.String()
}

// evalJS 在激活 tab 执行 JS 并解码到 res。
func (r *Runner) evalJS(ctx context.Context, js string, res interface{}) error {
	return chromedp.Run(ctx, chromedp.Evaluate(js, res, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithReturnByValue(true)
	}))
}

// ensureCk 确保定位函数存在（导航后需重新注入）。
func (r *Runner) ensureCk(ctx context.Context) error {
	var ok bool
	if err := r.evalJS(ctx, ensureLocatorJS(), &ok); err != nil {
		return err
	}
	return nil
}

// probe 对 locator 做只读探测，返回 ok/value。
func (r *Runner) probe(ctx context.Context, loc, mode, want string) (bool, interface{}, error) {
	if err := r.ensureCk(ctx); err != nil {
		return false, nil, err
	}
	var pr probeResult
	if err := r.evalJS(ctx, probeJS(loc, mode, want), &pr); err != nil {
		return false, nil, err
	}
	return pr.OK, pr.Value, nil
}

// resolve 动作定位：找到目标打标，返回可点击 selector；找不到返回 not_found。
func (r *Runner) resolve(ctx context.Context, st *Step, loc string) (string, error) {
	if strings.TrimSpace(loc) == "" {
		return "", stepErr(st.Line, st.Raw, "syntax", "缺少 locator")
	}
	if err := r.ensureCk(ctx); err != nil {
		return "", stepErr(st.Line, st.Raw, "js", "定位器注入失败: "+err.Error())
	}
	tag := newTag()
	var found bool
	if err := r.evalJS(ctx, resolveJS(loc, tag), &found); err != nil {
		return "", stepErr(st.Line, st.Raw, "js", "定位失败: "+err.Error())
	}
	if !found {
		return "", stepErr(st.Line, st.Raw, "not_found", "未找到元素 "+loc)
	}
	return ckSelector(tag), nil
}

func (r *Runner) execStep(st *Step) error {
	// 惰性启动：首个浏览器动作执行时才启动 Chrome/CDP（无浏览器动作的脚本不启动）。
	if err := r.ensureStarted(); err != nil {
		return err
	}
	switch st.Cmd {
	case "OPN":
		return r.stepOPN(st)
	case "WAT":
		return r.stepWAT(st)
	case "CLK", "DBL", "RCL", "HOV", "CHK", "UCHK":
		return r.stepClickLike(st)
	case "FILL":
		return r.stepFill(st)
	case "SELO":
		return r.stepSelect(st)
	case "KPR", "KDN", "KUP":
		return r.stepKey(st)
	case "EXP":
		return r.stepEXP(st)
	case "EVL":
		return r.stepEVL(st)
	case "SHT":
		return r.stepShot(st)
	case "DOM":
		return r.stepDOM(st)
	case "DBG":
		return r.stepDBG(st)
	case "SCL":
		return r.stepScroll(st)
	case "DRG":
		return r.stepDrag(st)
	case "UPF":
		return r.stepUpload(st)
	case "TAB":
		return r.stepTab(st)
	case "SLP":
		ms, err := parseIntArg(st, 0, 0)
		if err != nil {
			return err
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return nil
	default:
		return stepErr(st.Line, st.Raw, "syntax", "未知指令 "+st.Cmd)
	}
}

// parseIntArg 取数字参数（支持默认）。
func parseIntArg(st *Step, idx, def int) (int, error) {
	if idx >= len(st.Args) {
		return def, nil
	}
	var v int
	if _, err := fmt.Sscanf(st.Args[idx], "%d", &v); err != nil {
		return 0, stepErr(st.Line, st.Raw, "syntax", "参数应为数字: "+st.Args[idx])
	}
	return v, nil
}

// strArg 取字符串参数（无引号标记，token 已解）。
func strArg(st *Step, idx int) (string, bool) {
	if idx >= len(st.Args) {
		return "", false
	}
	return st.Args[idx], true
}

// ─── OPN ───
func (r *Runner) stepOPN(st *Step) error {
	url, ok := strArg(st, 0)
	if !ok {
		return stepErr(st.Line, st.Raw, "syntax", "OPN 需要 URL")
	}
	if err := chromedp.Run(r.ctx, chromedp.Navigate(url)); err != nil {
		return stepErr(st.Line, st.Raw, "navigation", "打开失败: "+err.Error())
	}
	return nil
}

// ─── EVL（单行或多行块）───
func (r *Runner) stepEVL(st *Step) error {
	js := ""
	if st.JS != "" {
		js = st.JS
	} else if len(st.Args) >= 1 {
		js = strings.Join(st.Args, " ")
	} else {
		return stepErr(st.Line, st.Raw, "syntax", "EVL 需要 JS 表达式或多行块")
	}
	var res interface{}
	if err := r.evalJS(r.ctx, js, &res); err != nil {
		return stepErr(st.Line, st.Raw, "js", "执行失败: "+err.Error())
	}
	return nil
}

// ─── EXP 断言 ───
func (r *Runner) stepEXP(st *Step) error {
	args := st.Args
	if len(args) == 0 {
		return stepErr(st.Line, st.Raw, "syntax", "EXP 需要参数")
	}
	// EXP js <expr> ["err"]：末尾双引号参数 = 自定义错误消息，其余为表达式
	if strings.EqualFold(args[0], "js") {
		expr := ""
		if st.JS != "" {
			expr = st.JS
		} else {
			parts := st.Args[1:]
			// Quoted 与 Args 全量对齐（含指令名 token），末尾引号参数当错误消息剥掉
			if len(parts) >= 2 && len(st.Quoted) == len(st.Args) && st.Quoted[len(st.Args)-1] {
				parts = parts[:len(parts)-1]
			}
			expr = strings.Join(parts, " ")
		}
		if expr == "" {
			return stepErr(st.Line, st.Raw, "syntax", "EXP js 需要 JS 表达式")
		}
		var res interface{}
		if err := r.evalJS(r.ctx, expr, &res); err != nil {
			return stepErr(st.Line, st.Raw, "js", "断言表达式执行失败: "+err.Error())
		}
		if !truthy(res) {
			msg := lastQuotedArg(st)
			if msg == "" {
				msg = "断言失败: " + expr
			}
			return stepErr(st.Line, st.Raw, "assert", msg)
		}
		return nil
	}
	// EXP page url|title ".."
	if strings.EqualFold(args[0], "page") {
		if len(args) < 2 {
			return stepErr(st.Line, st.Raw, "syntax", "EXP page 需要 url|title")
		}
		mode := strings.ToLower(args[1])
		want := ""
		if len(args) >= 3 {
			want = args[2]
		}
		ok, val, err := r.probe(r.ctx, "page", mode, "")
		if err != nil {
			return stepErr(st.Line, st.Raw, "js", err.Error())
		}
		got := fmt.Sprintf("%v", val)
		if !ok || (want != "" && got != want) {
			msg := customErrMsg(args, 3)
			if msg == "" {
				msg = fmt.Sprintf("断言失败: 期望 %s=%q，实际 %q", mode, want, got)
			}
			return stepErr(st.Line, st.Raw, "assert", msg)
		}
		return nil
	}
	// EXP <loc> visible|enabled|text ".."|count N|attr name "v"|exists
	if len(args) < 2 {
		return stepErr(st.Line, st.Raw, "syntax", "EXP <loc> 需要断言模式")
	}
	loc := args[0]
	mode := strings.ToLower(args[1])
	want := ""
	rest := args[2:]
	if len(rest) > 0 {
		want = rest[0]
	}
	ok, val, err := r.probe(r.ctx, loc, mode, want)
	if err != nil {
		return stepErr(st.Line, st.Raw, "js", err.Error())
	}
	pass := false
	switch mode {
	case "exists", "visible", "enabled":
		pass = ok && truthy(val)
	case "text", "attr", "url", "title":
		pass = ok && fmt.Sprintf("%v", val) == want
	case "count":
		var n int
		if ok {
			if f, isF := val.(float64); isF {
				n = int(f)
			}
		}
		wantN := -1
		if want != "" {
			fmt.Sscanf(want, "%d", &wantN)
		}
		pass = wantN >= 0 && n == wantN
	}
	if !pass {
		msg := customErrMsg(args, 2)
		if msg == "" {
			got := "无"
			if ok {
				got = fmt.Sprintf("%v", val)
			}
			msg = fmt.Sprintf("断言失败: %s %s，期望 %q，实际 %q", loc, mode, want, got)
		}
		return stepErr(st.Line, st.Raw, "assert", msg)
	}
	return nil
}

// customErrMsg 从 args 中提取自定义错误信息（通常最后一个双引号参数，未匹配 want 时）。
func customErrMsg(args []string, wantIdx int) string {
	if len(args) > wantIdx+1 {
		return args[len(args)-1]
	}
	return ""
}

// lastQuotedArg 返回步骤最后一个被双引号包裹的参数（自定义错误消息）。
func lastQuotedArg(st *Step) string {
	if len(st.Quoted) > 0 && st.Quoted[len(st.Quoted)-1] {
		return st.Args[len(st.Args)-1]
	}
	return ""
}

func truthy(v interface{}) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != "" && t != "false"
	case float64:
		return t != 0
	case json.Number:
		f, _ := t.Float64()
		return f != 0
	default:
		return true
	}
}

// ─── SHT 截图 ───
func (r *Runner) stepShot(st *Step) error {
	args := st.Args
	// SHT "file.png"（全页）或 SHT <loc> "file.png"（元素）
	if len(args) == 0 {
		return stepErr(st.Line, st.Raw, "syntax", "SHT 需要文件名")
	}
	if len(args) == 1 {
		// 落盘路径（R-11，运行时解析：覆盖 {{}} 插值结果与 !/ 临时目录）
		p, err := resolveLocalPath(args[0])
		if err != nil {
			return stepErr(st.Line, st.Raw, "path", err.Error())
		}
		return r.shotFile(p)
	}
	// 元素截图
	sel, err := r.resolve(r.ctx, st, args[0])
	if err != nil {
		return err
	}
	p, err := resolveLocalPath(args[1])
	if err != nil {
		return stepErr(st.Line, st.Raw, "path", err.Error())
	}
	return r.shotElement(st, sel, p)
}

func (r *Runner) shotFile(name string) error {
	var buf []byte
	if err := chromedp.Run(r.ctx, chromedp.FullScreenshot(&buf, 90)); err != nil {
		return stepErr(0, name, "shot", "截图失败: "+err.Error())
	}
	return savePNG(name, buf)
}

func (r *Runner) shotElement(st *Step, sel, name string) error {
	var rectJSON string
	js := fmt.Sprintf(`JSON.stringify((function(){var el=document.querySelector(%s);if(!el)return null;var r=el.getBoundingClientRect();return {x:r.x+scrollX,y:r.y+scrollY,w:r.width,h:r.height};})())`, quote(sel))
	if err := r.evalJS(r.ctx, js, &rectJSON); err != nil {
		return stepErr(st.Line, st.Raw, "shot", "定位元素失败: "+err.Error())
	}
	var rc struct {
		X, Y, W, H float64
	}
	if rectJSON == "" || json.Unmarshal([]byte(rectJSON), &rc) != nil || rc.W <= 0 || rc.H <= 0 {
		return stepErr(st.Line, st.Raw, "not_found", "元素不可见，无法截图 "+sel)
	}
	var shot []byte
	clip := &page.Viewport{X: rc.X, Y: rc.Y, Width: rc.W, Height: rc.H, Scale: 1}
	if err := chromedp.Run(r.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		b, e := page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatPng).WithClip(clip).Do(ctx)
		if e != nil {
			return e
		}
		shot = b
		return nil
	})); err != nil {
		return stepErr(st.Line, st.Raw, "shot", "截图失败: "+err.Error())
	}
	return savePNG(name, shot)
}

// writeFileChecked 是 browser 域落盘（SHT/DOM/DBG/console）的统一收口：写前过 agentbox
// 沙箱写校验（未启用隔离 = 一律放行），杜绝裸 os.WriteFile 绕过沙箱。
func writeFileChecked(name string, data []byte) error {
	if err := agentbox.Check(name, true); err != nil {
		return err
	}
	return os.WriteFile(name, data, 0o644)
}

// savePNG 写 PNG（buf 来自 CaptureScreenshot 类接口）。
func savePNG(name string, buf []byte) error {
	// chromedp.FullScreenshot 已返回编码 PNG 字节
	return writeFileChecked(name, buf)
}

// stepDOM DOM [<loc>] "file.html"：把元素 outerHTML（无 loc = 整页 documentElement）写入文件。
func (r *Runner) stepDOM(st *Step) error {
	args := st.Args
	if len(args) == 0 {
		return stepErr(st.Line, st.Raw, "syntax", "DOM 需要文件名（可选前置 locator）")
	}
	var html string
	if len(args) == 1 {
		// 整页 DOM
		if err := r.evalJS(r.ctx, `document.documentElement.outerHTML`, &html); err != nil {
			return stepErr(st.Line, st.Raw, "js", "取 DOM 失败: "+err.Error())
		}
	} else {
		sel, err := r.resolve(r.ctx, st, args[0])
		if err != nil {
			return err
		}
		js := fmt.Sprintf(`(function(){var el=document.querySelector(%s);if(!el)return '';return el.outerHTML;})()`, quote(sel))
		if err := r.evalJS(r.ctx, js, &html); err != nil {
			return stepErr(st.Line, st.Raw, "js", "取元素 DOM 失败: "+err.Error())
		}
		if html == "" {
			return stepErr(st.Line, st.Raw, "not_found", "元素不存在，无法导出 DOM "+args[0])
		}
		// 去掉打标残留（data-ck-loc），导出干净 DOM
		html = ckAttrRe.ReplaceAllString(html, "")
	}
	name := args[len(args)-1]
	// 落盘路径（R-11，运行时解析：覆盖 {{}} 插值结果与 !/ 临时目录）
	name, err := resolveLocalPath(name)
	if err != nil {
		return stepErr(st.Line, st.Raw, "path", err.Error())
	}
	if err := writeFileChecked(name, []byte(html)); err != nil {
		return stepErr(st.Line, st.Raw, "js", "写入文件失败: "+err.Error())
	}
	r.out = append(r.out, fmt.Sprintf("DOM: %d 字符已写入 %s", len(html), name))
	return nil
}

// stepDBG DBG "file.log"：把当前累计 console 内容写入文件。
func (r *Runner) stepDBG(st *Step) error {
	if len(st.Args) == 0 {
		return stepErr(st.Line, st.Raw, "syntax", "DBG 需要文件名")
	}
	// 落盘路径（R-11，运行时解析：覆盖 {{}} 插值结果与 !/ 临时目录）
	name, err := resolveLocalPath(st.Args[0])
	if err != nil {
		return stepErr(st.Line, st.Raw, "path", err.Error())
	}
	content := strings.Join(r.console, "\n")
	if err := writeFileChecked(name, []byte(content)); err != nil {
		return stepErr(st.Line, st.Raw, "js", "写入文件失败: "+err.Error())
	}
	r.out = append(r.out, fmt.Sprintf("DBG: %d 条 console 日志已写入 %s", len(r.console), name))
	return nil
}

func (r *Runner) domToFile(name string) error {
	var html string
	if err := r.evalJS(r.ctx, "document.documentElement.outerHTML", &html); err != nil {
		return err
	}
	return writeFileChecked(name, []byte(html))
}

func (r *Runner) flushConsole() error {
	if r.opt.ConsoleFile == "" || len(r.console) == 0 {
		return nil
	}
	content := strings.Join(r.console, "\n")
	return writeFileChecked(r.opt.ConsoleFile, []byte(content))
}

// findBrowserPath 探测 Chrome/Edge。
func findBrowserPath() string {
	if p := os.Getenv("CHONK_CHROME"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	cands := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		filepath.Join(os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`),
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
