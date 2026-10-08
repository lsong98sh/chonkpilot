// desktop_run 桌面编排 DSL 执行器。
//
// 指令集（三字母）：
//
//	WIN <target> <op...>      target: "title" | class:xxx | hwnd:123
//	                          op: focus|min|max|restore|move x,y|size w,h|rect x,y,w,h + 内嵌 SHT "file"
//	WIN list
//	MOV x,y
//	CLK [x,y] | DBL [x,y] | CLKR [x,y] | CLKM [x,y] | DBLR [x,y] | DBLM [x,y]
//	LMD [x,y] | LMU [x,y] | RMD [x,y] | RMU [x,y] | MMD [x,y] | MMU [x,y]
//	DRG x1,y1 -> x2,y2 [speed=..] [jitter=..]
//	WHL [x,y] dx,dy
//	INP "text"
//	KPR key | KDN key | KUP key
//	SHT "file.png" | SHT x,y,w,h "file.png"
//	SLP ms
//
// 坐标：绝对 300,400 ｜ D100,200（窗口 rect 相对）｜ C50,80（客户区相对）｜
//
//	@center/@title/@tl/@tr/@bl/@br/@left/@right/@top/@bottom ｜ @30%,40% ｜ $X+10,$Y 表达式。
//
// 预置变量（WIN 匹配后）：$X $Y $W $H（窗口 rect）、$CX $CY $CW $CH（客户区）、$WIN（句柄）。
package desktop

import (
	"fmt"
	"image"
	"image/png"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
	"github.com/chonkpilot/chonkpilot-lib/dsl"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/dslfs"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/fileops"
)

// HandleDesktopRun 执行桌面编排脚本（script | file 二选一）。
//
// 语法 = ChonkPilot DSL 核心（dsl-core.md）：desktop 动词（WIN/MOV/CLK/...）注册为
// Raw 动作（参数原文透传），并因此获得核心流控 SET/IF/LOOP/PARALLEL/BREAK/CONTINUE/EXIT、
// 变量与 {{}} 插值（WIN 后的 $X/$Y/$W/$H/$CX/$CY/$CW/$CH/$WIN 同步进作用域，可在引号内
// 以 {{$CX}} 引用）、#"文件".lines/.array 循环等。首错即停（StopOnError），报行号与已执行数。
func HandleDesktopRun(args map[string]interface{}) *ToolResult {
	script, scriptErr := dsScript(args)
	if scriptErr != "" {
		return &ToolResult{Success: false, Error: scriptErr, Output: "❌ desktop_run：" + scriptErr, Tool: "desktop_run"}
	}
	if script == "" {
		return &ToolResult{Success: false, Error: "script or file is required", Output: "❌ desktop_run：缺少 script 或 file", Tool: "desktop_run"}
	}

	sess, _ := NewSession()
	defer sess.Close()
	ctx := sess.ctx
	actions := sess.Actions()

	ast, err := dsl.Parse(script, actions)
	if err != nil {
		return &ToolResult{
			Success: false, Error: err.Error(),
			Output: fmt.Sprintf("❌ desktop_run：脚本解析失败：%s", err), Tool: "desktop_run",
		}
	}
	// 脚本内字面落盘路径（SHT / WIN SHT / `=> #"file"` 目标）在执行前预校验（R-11）。
	if msgs := prevalidateScriptPaths(ast); len(msgs) > 0 {
		msg := strings.Join(msgs, "; ")
		return &ToolResult{Success: false, Error: msg, Output: "❌ desktop_run：" + msg, Tool: "desktop_run"}
	}

	// 宿主注入的只读 env（CHONKPILOT_* → {{env.CHONKPILOT_*}}）；缺 instance → 顶层失败。
	te, envErr := fileops.BuildToolEnv()
	if envErr != nil {
		return cli.Err("desktop_run", envErr.Error())
	}
	eng := dsl.NewEngine(dsl.Options{Files: sess.Files(), Actions: actions, StopOnError: true, Vars: te.Vars})
	_ = eng.Execute(ast)
	res := eng.Result()
	if len(res.Errors) > 0 {
		e0 := res.Errors[0]
		return &ToolResult{
			Success:   false,
			Error:     e0.Msg,
			Output:    fmt.Sprintf("❌ desktop_run：第 %d 行失败：%s（已执行 %d 条）", e0.Line, e0.Msg, ctx.done),
			Tool:      "desktop_run",
			RawResult: map[string]interface{}{"line": e0.Line, "error": e0.Msg, "executed": ctx.done},
		}
	}
	return &ToolResult{
		Success: true,
		Output:  fmt.Sprintf("✅ desktop_run：执行 %d 条指令", ctx.done),
		Tool:    "desktop_run",
		RawResult: map[string]interface{}{
			"executed": ctx.done,
			"window":   ctx.curHWND,
			"x":        int(ctx.vars["X"]), "y": int(ctx.vars["Y"]),
			"w": int(ctx.vars["W"]), "h": int(ctx.vars["H"]),
		},
	}
}

// prevalidateScriptPaths 执行前预校验 DSL 内**字面**落盘路径（R-11）：返回违规消息列表。
// 两类来源：① 动作参数内的落盘路径（SHT 文件名、WIN 链内 SHT 文件名）；
// ② 核心语句的文件句柄（LOOP/SET 数据源、IF exist、访问器、行尾 `=> #"file"` 目标）——
// 经 dsl.CollectHandleRefs 收集。含 {{}} 插值的路径跳过（执行时由 checkLocalPath / scriptFile
// 写方法兜底校验）。字面相对路径据此在执行前被拒绝。
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
				toks := tokenize(t.RawArgs)
				switch verb {
				case "SHT":
					if n := len(toks); n > 0 {
						if p := unquote(toks[n-1]); !strings.Contains(p, "{{") {
							if msg := fileops.ValidateField(fmt.Sprintf("第 %d 行 SHT", t.Line()), p); msg != "" {
								msgs = append(msgs, msg)
							}
						}
					}
				case "WIN":
					for i := 0; i+1 < len(toks); i++ {
						op := strings.ToLower(toks[i])
						if op != "sht" && op != "shot" {
							continue
						}
						if p := unquote(toks[i+1]); !strings.Contains(p, "{{") {
							if msg := fileops.ValidateField(fmt.Sprintf("第 %d 行 WIN SHT", t.Line()), p); msg != "" {
								msgs = append(msgs, msg)
							}
						}
						i++
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

// resolveLocalPath 运行时解析 DSL 内落盘路径（R-11）：覆盖 {{}} 插值后的结果，
// 支持绝对 / ~/ / !/（临时目录）。
func resolveLocalPath(p string) (string, error) {
	return fileops.ResolveLocalPath(p)
}

// desktopActions 注册全部桌面动词为 Raw 动作（参数原文 → tokenize → 原指令实现）。
func desktopActions(ctx *runCtx) []dsl.Action {
	verbs := []string{"WIN", "MOV", "CLK", "DBL", "CLKR", "CLKM", "DBLR", "DBLM",
		"LMD", "LMU", "RMD", "RMU", "MMD", "MMU", "DRG", "WHL",
		"INP", "KPR", "KDN", "KUP", "IME", "SHT", "SLP"}
	acts := make([]dsl.Action, 0, len(verbs))
	for _, v := range verbs {
		verb := v
		acts = append(acts, dsl.Action{
			Name: verb,
			Raw:  true,
			Run: func(sc *dsl.Scope, args string) (string, error) {
				ctx.mu.Lock()
				ctx.done++
				ctx.out = "" // 每条动作独立文本输出（如 WIN list；无输出保持空串，重定向仍执行）
				ctx.mu.Unlock()
				// WIN 后把窗口变量同步进作用域，供 {{$CX}} 等引号内插值
				ctx.syncVars(sc)
				toks := tokenize(args)
				toks = interpTokens(sc, toks)
				err := ctx.dispatch(verb, toks)
				ctx.syncVars(sc)
				return ctx.out, err
			},
		})
	}
	return acts
}

// interpTokens 对 token（引号内文本）做 {{}} 插值，保留引号包裹。
func interpTokens(sc *dsl.Scope, toks []string) []string {
	out := make([]string, len(toks))
	for i, t := range toks {
		if !strings.Contains(t, "{{") {
			out[i] = t
			continue
		}
		if len(t) >= 2 && t[0] == '"' && t[len(t)-1] == '"' {
			inner, err := sc.Interp(t[1 : len(t)-1])
			if err == nil {
				out[i] = `"` + inner + `"`
				continue
			}
		}
		s, err := sc.Interp(t)
		if err == nil {
			out[i] = s
			continue
		}
		out[i] = t
	}
	return out
}

// syncVars 把窗口预置变量同步进 dsl 作用域（$ 前缀保持坐标/插值语法一致）。
func (c *runCtx) syncVars(sc *dsl.Scope) {
	if sc == nil {
		return
	}
	for k, v := range c.vars {
		sc.Set("$"+k, v)
	}
}

// scriptFS 核心语句（#"文件".lines/.array 等）使用的校验型文件系统：句柄实现见共享包 dslfs
// （default 档），路径强校验（R-11）与 agentbox 沙箱读写校验统一在句柄层完成。
type scriptFS struct{}

func (scriptFS) Open(path string) dsl.FileHandle { return dslfs.New(path, dslfs.Default) }

// dsScript 从 script / file 参数取桌面编排脚本文本。
// file 为脚本文件路径，须满足 R-11（绝对路径或 ~/ 开头）；违规返回错误消息。
func dsScript(args map[string]interface{}) (string, string) {
	if s, _ := args["script"].(string); s != "" {
		return s, ""
	}
	if p, _ := args["file"].(string); p != "" {
		resolved, msg := fileops.ValidateFilePath(p)
		if msg != "" {
			return "", "file：" + msg
		}
		// 读脚本文件前过 agentbox 沙箱读校验（未启用隔离 = 放行；口径同 fileops/scriptrun）。
		if err := agentbox.Check(resolved, false); err != nil {
			return "", "file：" + err.Error()
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return "", "读取脚本文件失败：" + err.Error()
		}
		return string(data), ""
	}
	return "", ""
}

// runCtx 是脚本执行状态。
type runCtx struct {
	curHWND    syscall.Handle
	curRect    Rect // 窗口 rect（屏幕坐标）
	clientRect Rect // 客户区 rect（屏幕坐标）
	vars       map[string]float64
	speed      int // 1-10 移动速度（默认 5）
	jitter     int // 0-20 轨迹随机扰动像素（默认 3）
	delay      int // 指令间固定等待 ms（默认 0，脚本内 SLP 控制）
	mu         sync.Mutex
	done       int    // 已执行动作数
	out        string // 每条动作的文本输出（WIN list 等；无输出为空串，供动作 => 目标重定向/汇总）
}

// dispatch 执行单条指令（cmd 大写；rest 已按 tokenize 拆分并做插值）。
func (c *runCtx) dispatch(cmd string, rest []string) error {
	switch cmd {
	case "WIN":
		return c.cmdWin(rest)
	case "MOV":
		if len(rest) < 1 {
			return fmt.Errorf("MOV 需要坐标")
		}
		x, y, err := c.parseCoord(rest[0])
		if err != nil {
			return err
		}
		c.moveTo(x, y)
		return nil
	case "CLK", "DBL", "CLKR", "CLKM", "DBLR", "DBLM":
		return c.cmdClick(cmd, rest)
	case "LMD", "LMU", "RMD", "RMU", "MMD", "MMU":
		return c.cmdMouseDownUp(cmd, rest)
	case "DRG":
		return c.cmdDrag(rest)
	case "WHL":
		return c.cmdWheel(rest)
	case "INP":
		if len(rest) < 1 {
			return fmt.Errorf("INP 需要文本")
		}
		text := unquote(rest[0])
		for _, r := range text {
			typeRune(r)
		}
		return nil
	case "KPR", "KDN", "KUP":
		if len(rest) < 1 {
			return fmt.Errorf("%s 需要按键名（支持组合键，如 ctrl+s）", cmd)
		}
		return c.cmdKey(cmd, rest[0])
	case "IME":
		if len(rest) < 1 {
			return fmt.Errorf("IME 需要参数 en|cn（英文/中文输入法）")
		}
		return c.cmdIME(rest[0])
	case "SHT":
		return c.cmdShot(rest)
	case "SLP":
		if len(rest) < 1 {
			return fmt.Errorf("SLP 需要毫秒数")
		}
		ms := atoi(rest[0])
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return nil
	default:
		return fmt.Errorf("未知指令 %q", cmd)
	}
}

// ─── 窗口 ───

func (c *runCtx) cmdWin(rest []string) error {
	if len(rest) == 0 {
		return fmt.Errorf("WIN 需要 target 或 list")
	}
	if strings.EqualFold(rest[0], "list") {
		return c.listWindows()
	}
	hwnd, err := c.resolveTarget(rest[0])
	if err != nil {
		return err
	}
	c.bindWindow(hwnd)
	// 后续 ops 连续执行
	for i := 1; i < len(rest); i++ {
		op := strings.ToLower(rest[i])
		switch op {
		case "focus":
			SetForegroundWindow.Call(uintptr(hwnd))
		case "min", "minimize":
			ShowWindow.Call(uintptr(hwnd), SwMinimize)
		case "max", "maximize":
			ShowWindow.Call(uintptr(hwnd), SwMaximize)
		case "restore":
			ShowWindow.Call(uintptr(hwnd), SwRestore)
		case "move":
			if i+1 >= len(rest) {
				return fmt.Errorf("WIN move 需要坐标")
			}
			i++
			x, y, err := c.parseCoord(rest[i])
			if err != nil {
				return err
			}
			SetWindowPos.Call(uintptr(hwnd), 0, uintptr(int32(x)), uintptr(int32(y)),
				uintptr(int32(c.curRect.Right-c.curRect.Left)), uintptr(int32(c.curRect.Bottom-c.curRect.Top)), SwpNoZOrder)
			c.bindWindow(hwnd)
		case "size":
			if i+1 >= len(rest) {
				return fmt.Errorf("WIN size 需要坐标")
			}
			i++
			w, h, err := c.parseCoord(rest[i])
			if err != nil {
				return err
			}
			SetWindowPos.Call(uintptr(hwnd), 0, uintptr(int32(c.curRect.Left)), uintptr(int32(c.curRect.Top)),
				uintptr(int32(w)), uintptr(int32(h)), SwpNoZOrder)
			c.bindWindow(hwnd)
		case "rect":
			if i+2 >= len(rest) {
				return fmt.Errorf("WIN rect 需要坐标+尺寸")
			}
			x, y, err := c.parseCoord(rest[i+1])
			if err != nil {
				return err
			}
			w, h, err := c.parseCoord(rest[i+2])
			if err != nil {
				return err
			}
			SetWindowPos.Call(uintptr(hwnd), 0, uintptr(int32(x)), uintptr(int32(y)),
				uintptr(int32(w)), uintptr(int32(h)), SwpNoZOrder)
			c.bindWindow(hwnd)
			i += 2
		case "sht", "shot":
			if i+1 < len(rest) {
				file, err := resolveLocalPath(unquote(rest[i+1]))
				if err != nil {
					return fmt.Errorf("WIN SHT：%s", err)
				}
				if err := saveWindowShot(hwnd, file); err != nil {
					return err
				}
				i++
			} else {
				return fmt.Errorf("WIN SHT 需要文件名")
			}
		default:
			return fmt.Errorf("未知窗口操作 %q", op)
		}
	}
	return nil
}

func (c *runCtx) listWindows() error {
	hwnds := EnumWindowsList()
	var titles []string
	for _, h := range hwnds {
		t := GetWindowTitle(h)
		if t != "" {
			info := GetWindowInfo(h)
			titles = append(titles, fmt.Sprintf("%s  [hwnd=%d]", t, info.Hwnd))
		}
	}
	if len(titles) == 0 {
		return fmt.Errorf("无可见窗口")
	}
	c.out = strings.Join(titles, "\n")
	fmt.Fprintf(os.Stderr, "[desktop_run] windows:\n%s\n", c.out)
	return nil
}

// resolveTarget 解析窗口 target："title" / class:xxx / hwnd:123。
func (c *runCtx) resolveTarget(s string) (syscall.Handle, error) {
	s = unquote(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(s, "hwnd:"):
		return syscall.Handle(uintptr(int32(atoi(s[5:])))), nil
	case strings.HasPrefix(s, "class:"):
		h, err := FindWindowByClass(strings.TrimSpace(s[6:]))
		if err != nil {
			return 0, fmt.Errorf("窗口未找到（class %s）", s[6:])
		}
		return h, nil
	default:
		h, err := FindWindowByTitle(s)
		if err != nil {
			return 0, fmt.Errorf("窗口未找到：%s", s)
		}
		return h, nil
	}
}

// bindWindow 绑定当前窗口并更新预置变量。
func (c *runCtx) bindWindow(hwnd syscall.Handle) {
	c.curHWND = hwnd
	info := GetWindowInfo(hwnd)
	c.curRect = Rect{info.Left, info.Top, info.Right, info.Bottom}
	c.clientRect = ClientScreenRect(hwnd)
	w := float64(c.curRect.Right - c.curRect.Left)
	h := float64(c.curRect.Bottom - c.curRect.Top)
	cw := float64(c.clientRect.Right - c.clientRect.Left)
	ch := float64(c.clientRect.Bottom - c.clientRect.Top)
	c.vars["X"], c.vars["Y"], c.vars["W"], c.vars["H"] = float64(c.curRect.Left), float64(c.curRect.Top), w, h
	c.vars["CX"], c.vars["CY"] = float64(c.clientRect.Left), float64(c.clientRect.Top)
	c.vars["CW"], c.vars["CH"] = cw, ch
	c.vars["WIN"] = float64(hwnd)
}

// ─── 鼠标 ───

func (c *runCtx) cmdClick(cmd string, rest []string) error {
	btn, double := clickSpec(cmd)
	var x, y int
	var hasXY bool
	if len(rest) >= 1 {
		var err error
		x, y, err = c.parseCoord(rest[0])
		if err != nil {
			return err
		}
		hasXY = true
	}
	if hasXY {
		c.moveTo(x, y)
	}
	times := 1
	if double {
		times = 2
	}
	for i := 0; i < times; i++ {
		SendMouseEvent(btn.down, 0, 0, 0)
		SendMouseEvent(btn.up, 0, 0, 0)
	}
	return nil
}

func (c *runCtx) cmdMouseDownUp(cmd string, rest []string) error {
	down := strings.HasSuffix(cmd, "MD")
	var flag uint32
	switch {
	case strings.HasPrefix(cmd, "L"):
		if down {
			flag = MouseEventLeftDown
		} else {
			flag = MouseEventLeftUp
		}
	case strings.HasPrefix(cmd, "R"):
		if down {
			flag = MouseEventRightDown
		} else {
			flag = MouseEventRightUp
		}
	default:
		if down {
			flag = MouseEventMiddleDown
		} else {
			flag = MouseEventMiddleUp
		}
	}
	if len(rest) >= 1 {
		x, y, err := c.parseCoord(rest[0])
		if err != nil {
			return err
		}
		c.moveTo(x, y)
	}
	SendMouseEvent(flag, 0, 0, 0)
	return nil
}

func (c *runCtx) cmdDrag(rest []string) error {
	// DRG x1,y1 -> x2,y2 [speed=..] [jitter=..]
	var from, to string
	var speed, jitter = c.speed, c.jitter
	for i := 0; i < len(rest); i++ {
		t := rest[i]
		switch {
		case t == "->":
			continue
		case strings.HasPrefix(t, "speed="):
			speed = parseSpeedVal(strings.TrimPrefix(t, "speed="))
		case strings.HasPrefix(t, "jitter="):
			jitter = parseJitterVal(strings.TrimPrefix(t, "jitter="))
		case from == "":
			from = t
		case to == "":
			to = t
		default:
			if strings.HasPrefix(t, "speed") || strings.HasPrefix(t, "jitter") {
				continue
			}
			return fmt.Errorf("DRG 多余参数 %q", t)
		}
	}
	if from == "" || to == "" {
		return fmt.Errorf("DRG 需要 x1,y1 -> x2,y2")
	}
	x1, y1, err := c.parseCoord(from)
	if err != nil {
		return err
	}
	x2, y2, err := c.parseCoord(to)
	if err != nil {
		return err
	}
	c.moveTo(x1, y1)
	SendMouseEvent(MouseEventLeftDown, 0, 0, 0)
	steps := clampInt(2+speed, 2, 20)
	time.Sleep(time.Millisecond * 40)
	for i := 1; i <= steps; i++ {
		f := float64(i) / float64(steps)
		// ease-in-out
		f = f * f * (3 - 2*f)
		px := x1 + int(float64(x2-x1)*f)
		py := y1 + int(float64(y2-y1)*f)
		if jitter > 0 {
			px += rand.Intn(2*jitter+1) - jitter
			py += rand.Intn(2*jitter+1) - jitter
		}
		c.moveTo(px, py)
		time.Sleep(time.Millisecond * 12)
	}
	time.Sleep(time.Millisecond * 40)
	SendMouseEvent(MouseEventLeftUp, 0, 0, 0)
	return nil
}

func (c *runCtx) cmdWheel(rest []string) error {
	// WHL [x,y] dx,dy
	var dx, dy int
	if len(rest) == 1 {
		dx, dy = splitXYInt(rest[0])
	} else if len(rest) >= 2 {
		x, y, err := c.parseCoord(rest[0])
		if err != nil {
			return err
		}
		c.moveTo(x, y)
		dx, dy = splitXYInt(rest[1])
	} else {
		return fmt.Errorf("WHL 需要 dx,dy")
	}
	if dx != 0 {
		SendMouseEvent(MouseEventHWheel, 0, 0, uint32(int32(dx)))
	}
	if dy != 0 {
		SendMouseEvent(MouseEventWheel, 0, 0, uint32(int32(dy)))
	}
	return nil
}

// ─── 键盘 ───

// cmdKey 执行 KPR/KDN/KUP。spec 支持单键（enter/ctrl）与组合键（ctrl+s、alt+f4、win+r）：
//   - KPR：按顺序按下修饰 → 按下/释放主键 → 逆序释放修饰
//   - KDN：按下修饰与主键（保持按住，供后续 INP/主键配合）
//   - KUP：释放主键与修饰
func (c *runCtx) cmdKey(cmd, spec string) error {
	mods, mainVk, err := parseKeySpec(spec)
	if err != nil {
		return err
	}
	switch cmd {
	case "KPR":
		release := DesktopPressMods(mods)
		if !SendKey(mainVk, false) {
			release()
			return fmt.Errorf("按键注入失败 %s", spec)
		}
		time.Sleep(time.Millisecond * 30)
		if !SendKey(mainVk, true) {
			release()
			return fmt.Errorf("按键注入失败 %s", spec)
		}
		time.Sleep(time.Millisecond * 30)
		release()
	case "KDN":
		// 已按下的修饰键：主键失败时须逆序释放，避免键盘状态泄漏（C-05）。
		var pressed []uint16
		releaseMods := func() {
			for i := len(pressed) - 1; i >= 0; i-- {
				SendKey(pressed[i], true)
			}
		}
		for _, m := range mods {
			if !SendKey(m, false) {
				releaseMods()
				return fmt.Errorf("按键注入失败 %s", spec)
			}
			pressed = append(pressed, m)
			time.Sleep(time.Millisecond * 20)
		}
		if !SendKey(mainVk, false) {
			releaseMods()
			return fmt.Errorf("按键注入失败 %s", spec)
		}
	case "KUP":
		if !SendKey(mainVk, true) {
			return fmt.Errorf("按键注入失败 %s", spec)
		}
		for i := len(mods) - 1; i >= 0; i-- {
			if !SendKey(mods[i], true) {
				return fmt.Errorf("按键注入失败 %s", spec)
			}
		}
	}
	return nil
}

// cmdIME 切换前台窗口输入法（en=英文直通，cn=中文）。
func (c *runCtx) cmdIME(arg string) error {
	open, err := imeArg(arg)
	if err != nil {
		return err
	}
	return setImeOpen(open)
}

// ─── 截图 ───

func (c *runCtx) cmdShot(rest []string) error {
	if len(rest) == 0 {
		return fmt.Errorf("SHT 需要文件名")
	}
	if len(rest) == 1 {
		file, err := resolveLocalPath(unquote(rest[0]))
		if err != nil {
			return fmt.Errorf("SHT：%s", err)
		}
		return saveScreenShot(file)
	}
	// SHT x,y,w,h "file.png"
	x, y, err := c.parseCoord(rest[0])
	if err != nil {
		return err
	}
	w, h, err := c.parseCoord(rest[1])
	if err != nil {
		return err
	}
	file, err := resolveLocalPath(unquote(rest[2]))
	if err != nil {
		return fmt.Errorf("SHT：%s", err)
	}
	rect := &Rect{Left: int32(x), Top: int32(y), Right: int32(x + w), Bottom: int32(y + h)}
	img, err := captureImage(rect, 0)
	if err != nil {
		return err
	}
	return encodePNG(img, file)
}

func saveWindowShot(hwnd syscall.Handle, file string) error {
	info := GetWindowInfo(hwnd)
	rect := &Rect{info.Left, info.Top, info.Right, info.Bottom}
	img, err := captureImage(rect, uintptr(hwnd))
	if err != nil {
		return err
	}
	return encodePNG(img, file)
}

func saveScreenShot(file string) error {
	img, err := captureImage(GetFullScreenRect(), 0)
	if err != nil {
		return err
	}
	return encodePNG(img, file)
}

func encodePNG(img *image.RGBA, file string) error {
	// 落盘收口：写前过 agentbox 沙箱写校验（未启用隔离 = 一律放行），杜绝裸 os.Create 绕过沙箱。
	if err := agentbox.Check(file, true); err != nil {
		return err
	}
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// ─── 坐标解析 ───

// parseCoord 解析坐标表达式为屏幕绝对坐标。
func (c *runCtx) parseCoord(expr string) (int, int, error) {
	e := strings.TrimSpace(expr)
	if strings.HasPrefix(e, "@") {
		return c.anchorCoord(e[1:])
	}
	if len(e) >= 2 && (e[0] == 'D' || e[0] == 'C') && isDigit(e[1]) {
		base := c.curRect
		if e[0] == 'C' {
			base = c.clientRect
		}
		if c.curHWND == 0 {
			return 0, 0, fmt.Errorf("坐标前缀 D/C 需要先 WIN 定位窗口")
		}
		x, y, err := splitXYEval(e[1:], c.vars)
		if err != nil {
			return 0, 0, err
		}
		return int(base.Left) + x, int(base.Top) + y, nil
	}
	return splitXYEval(e, c.vars)
}

func (c *runCtx) anchorCoord(anchor string) (int, int, error) {
	if c.curHWND == 0 {
		return 0, 0, fmt.Errorf("@ 锚点需要先 WIN 定位窗口")
	}
	r := c.curRect
	w := r.Right - r.Left
	h := r.Bottom - r.Top
	switch strings.ToLower(anchor) {
	case "center":
		return int(r.Left + w/2), int(r.Top + h/2), nil
	case "title":
		return int(r.Left + w/2), int(r.Top + 15), nil
	case "tl":
		return int(r.Left), int(r.Top), nil
	case "tr":
		return int(r.Right), int(r.Top), nil
	case "bl":
		return int(r.Left), int(r.Bottom), nil
	case "br":
		return int(r.Right), int(r.Bottom), nil
	case "left":
		return int(r.Left), int(r.Top + h/2), nil
	case "right":
		return int(r.Right), int(r.Top + h/2), nil
	case "top":
		return int(r.Left + w/2), int(r.Top), nil
	case "bottom":
		return int(r.Left + w/2), int(r.Bottom), nil
	}
	// 百分比 @30%,40%
	if strings.Contains(anchor, "%") {
		parts := strings.SplitN(anchor, ",", 2)
		if len(parts) == 2 {
			px := parsePercent(parts[0])
			py := parsePercent(parts[1])
			return int(r.Left) + int(float64(w)*px), int(r.Top) + int(float64(h)*py), nil
		}
	}
	return 0, 0, fmt.Errorf("未知锚点 @%s", anchor)
}

// ─── 工具函数 ───

func tokenize(line string) []string {
	var toks []string
	var cur strings.Builder
	inQuote := false
	for _, r := range line {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case (r == ' ' || r == '\t') && !inQuote:
			if cur.Len() > 0 {
				toks = append(toks, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		toks = append(toks, cur.String())
	}
	return toks
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

type btnSpec struct{ down, up uint32 }

func clickSpec(cmd string) (btnSpec, bool) {
	switch cmd {
	case "CLKR", "DBLR":
		return btnSpec{MouseEventRightDown, MouseEventRightUp}, strings.HasPrefix(cmd, "DB")
	case "CLKM", "DBLM":
		return btnSpec{MouseEventMiddleDown, MouseEventMiddleUp}, strings.HasPrefix(cmd, "DB")
	default: // CLK / DBL
		return btnSpec{MouseEventLeftDown, MouseEventLeftUp}, cmd == "DBL"
	}
}

func (c *runCtx) moveTo(x, y int) {
	full := GetFullScreenRect()
	if full.Right <= 0 {
		full.Right = 1920
	}
	if full.Bottom <= 0 {
		full.Bottom = 1080
	}
	SendMouseEvent(MouseEventMove|MouseEventAbsolute,
		int32(float64(x)*65535.0/float64(full.Right)),
		int32(float64(y)*65535.0/float64(full.Bottom)), 0)
}

func typeRune(r rune) {
	// 可打印字符（含 ASCII 标点）一律 Unicode 注入：绕过 IME，避免中文态把 : \ . 等转全角。
	// 控制类字符（换行/制表/回车，<0x20）仍走 VK（Unicode 注入无法表达按键语义）。
	if r < 0x20 {
		vk, needsShift := CharToVK(byte(r))
		if needsShift {
			SendKey(0x10, false)
		}
		SendKey(vk, false)
		SendKey(vk, true)
		if needsShift {
			SendKey(0x10, true)
		}
		return
	}
	SendKeyUnicode(uint16(r), false)
	SendKeyUnicode(uint16(r), true)
}

func splitXYEval(s string, vars map[string]float64) (int, int, error) {
	parts := strings.SplitN(s, ",", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("坐标需含逗号：%s", s)
	}
	x, err := evalExpr(parts[0], vars)
	if err != nil {
		return 0, 0, err
	}
	y, err := evalExpr(parts[1], vars)
	if err != nil {
		return 0, 0, err
	}
	return int(x), int(y), nil
}

func splitXYInt(s string) (int, int) {
	parts := strings.SplitN(s, ",", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	return atoi(parts[0]), atoi(parts[1])
}

func parsePercent(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(s, "%"))
	v, _ := strconv.ParseFloat(s, 64)
	return v / 100.0
}

func parseSpeedVal(s string) int {
	switch strings.ToLower(s) {
	case "快", "fast", "high":
		return 8
	case "慢", "slow", "low":
		return 2
	default:
		if n := atoi(s); n > 0 {
			return n
		}
		return 5
	}
}

func parseJitterVal(s string) int {
	switch strings.ToLower(s) {
	case "高", "high":
		return 10
	case "低", "low":
		return 2
	default:
		if n := atoi(s); n >= 0 {
			return n
		}
		return 3
	}
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func atoi(s string) int {
	s = strings.TrimSpace(s)
	n := 0
	neg := false
	for i, c := range s {
		if i == 0 && (c == '-' || c == '+') {
			neg = c == '-'
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		return -n
	}
	return n
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// evalExpr 简单四则表达式求值（支持 $变量、数字、+ - * /；无括号）。
func evalExpr(s string, vars map[string]float64) (float64, error) {
	p := &exprParser{s: s, vars: vars}
	v, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipSpace()
	if p.pos < len(p.s) {
		return 0, fmt.Errorf("表达式含多余字符：%s", p.s[p.pos:])
	}
	return v, nil
}

type exprParser struct {
	s    string
	pos  int
	vars map[string]float64
}

func (p *exprParser) skipSpace() {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t') {
		p.pos++
	}
}

func (p *exprParser) parseExpr() (float64, error) {
	v, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.s) {
			return v, nil
		}
		op := p.s[p.pos]
		if op != '+' && op != '-' {
			return v, nil
		}
		p.pos++
		r, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			v += r
		} else {
			v -= r
		}
	}
}

func (p *exprParser) parseTerm() (float64, error) {
	v, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.s) {
			return v, nil
		}
		op := p.s[p.pos]
		if op != '*' && op != '/' {
			return v, nil
		}
		p.pos++
		r, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		if op == '*' {
			v *= r
		} else {
			if r == 0 {
				return 0, fmt.Errorf("除零")
			}
			v /= r
		}
	}
}

func (p *exprParser) parseFactor() (float64, error) {
	p.skipSpace()
	if p.pos >= len(p.s) {
		return 0, fmt.Errorf("表达式不完整")
	}
	if p.s[p.pos] == '$' {
		j := p.pos + 1
		for j < len(p.s) && (p.s[j] == '_' || (p.s[j] >= '0' && p.s[j] <= '9') || (p.s[j] >= 'A' && p.s[j] <= 'Z') || (p.s[j] >= 'a' && p.s[j] <= 'z')) {
			j++
		}
		name := p.s[p.pos+1 : j]
		p.pos = j
		if v, ok := p.vars[name]; ok {
			return v, nil
		}
		return 0, fmt.Errorf("未知变量 $%s", name)
	}
	j := p.pos
	hasDot := false
	for j < len(p.s) && ((p.s[j] >= '0' && p.s[j] <= '9') || (p.s[j] == '.' && !hasDot)) {
		if p.s[j] == '.' {
			hasDot = true
		}
		j++
	}
	if j == p.pos {
		return 0, fmt.Errorf("表达式 %q 位置 %d 非法", p.s, p.pos)
	}
	v, err := strconv.ParseFloat(p.s[p.pos:j], 64)
	if err != nil {
		return 0, err
	}
	p.pos = j
	return v, nil
}
