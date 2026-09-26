package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"

	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/fileops"
)

// elemCenter 取打标元素中心（viewport 坐标）。
func (r *Runner) elemCenter(st *Step, sel string) (x, y float64, err error) {
	js := fmt.Sprintf(`JSON.stringify((function(){var el=document.querySelector(%s);if(!el)return null;var r=el.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2};})())`, quote(sel))
	var raw string
	if e := r.evalJS(r.ctx, js, &raw); e != nil {
		return 0, 0, stepErr(st.Line, st.Raw, "js", "取元素位置失败: "+e.Error())
	}
	var c struct {
		X, Y float64
	}
	if raw == "" || json.Unmarshal([]byte(raw), &c) != nil {
		return 0, 0, stepErr(st.Line, st.Raw, "not_found", "元素不存在或不可见 "+sel)
	}
	return c.X, c.Y, nil
}

// dispatchMouse 发送鼠标事件。
func (r *Runner) dispatchMouse(ctx context.Context, typ input.MouseType, x, y float64, btn input.MouseButton, clickCount int) error {
	return chromedp.Run(ctx, chromedp.ActionFunc(func(c context.Context) error {
		return input.DispatchMouseEvent(typ, x, y).WithButton(btn).WithClickCount(int64(clickCount)).Do(c)
	}))
}

// scrollIntoView 使元素可见（动作前）。
func (r *Runner) scrollIntoView(sel string) error {
	return chromedp.Run(r.ctx, chromedp.ScrollIntoView(sel, chromedp.ByQuery))
}

// mouseActionAt 在目标元素上执行一次按压-释放（支持右键/双击）。
func (r *Runner) mouseActionAt(st *Step, sel string, right bool, double bool) error {
	if err := r.scrollIntoView(sel); err != nil {
		return stepErr(st.Line, st.Raw, "js", "滚动失败: "+err.Error())
	}
	x, y, err := r.elemCenter(st, sel)
	if err != nil {
		return err
	}
	btn := input.Left
	if right {
		btn = input.Right
	}
	clicks := 1
	if double {
		clicks = 2
	}
	if err := r.dispatchMouse(r.ctx, input.MousePressed, x, y, btn, clicks); err != nil {
		return stepErr(st.Line, st.Raw, "js", "鼠标事件失败: "+err.Error())
	}
	if err := r.dispatchMouse(r.ctx, input.MouseReleased, x, y, btn, clicks); err != nil {
		return stepErr(st.Line, st.Raw, "js", "鼠标事件失败: "+err.Error())
	}
	return nil
}

// stepClickLike 处理 CLK/DBL/RCL/HOV/CHK/UCHK。
func (r *Runner) stepClickLike(st *Step) error {
	loc, ok := strArg(st, 0)
	if !ok {
		return stepErr(st.Line, st.Raw, "syntax", st.Cmd+" 需要 locator")
	}
	sel, err := r.resolve(r.ctx, st, loc)
	if err != nil {
		return err
	}
	switch st.Cmd {
	case "HOV":
		if err := r.scrollIntoView(sel); err != nil {
			return stepErr(st.Line, st.Raw, "js", err.Error())
		}
		x, y, err := r.elemCenter(st, sel)
		if err != nil {
			return err
		}
		if err := r.dispatchMouse(r.ctx, input.MouseMoved, x, y, input.None, 0); err != nil {
			return stepErr(st.Line, st.Raw, "js", "hover 失败: "+err.Error())
		}
		return nil
	case "CHK", "UCHK":
		want := st.Cmd == "CHK"
		ok, val, err := r.probe(r.ctx, loc, "attr", "checked")
		if err != nil {
			return stepErr(st.Line, st.Raw, "js", err.Error())
		}
		isChecked := ok && truthy(val)
		if isChecked != want {
			return r.mouseActionAt(st, sel, false, false)
		}
		return nil
	case "DBL":
		return r.mouseActionAt(st, sel, false, true)
	case "RCL":
		return r.mouseActionAt(st, sel, true, false)
	default: // CLK
		return r.mouseActionAt(st, sel, false, false)
	}
}

// stepFill FILL <loc> "text"：聚焦 → 清空 → 键盘输入。
func (r *Runner) stepFill(st *Step) error {
	if len(st.Args) < 2 {
		return stepErr(st.Line, st.Raw, "syntax", "FILL 需要 locator 和文本")
	}
	loc := st.Args[0]
	text := st.Args[1]
	sel, err := r.resolve(r.ctx, st, loc)
	if err != nil {
		return err
	}
	if err := r.scrollIntoView(sel); err != nil {
		return stepErr(st.Line, st.Raw, "js", err.Error())
	}
	if err := chromedp.Run(r.ctx, chromedp.Click(sel, chromedp.ByQuery)); err != nil {
		return stepErr(st.Line, st.Raw, "js", "聚焦失败: "+err.Error())
	}
	// 全选后输入（清空旧值）；textarea/input 走 select()
	clearJS := fmt.Sprintf(`(function(){var el=document.querySelector(%s);if(!el)return false;if(typeof el.select==='function'){el.select();return true;}var r=document.createRange();r.selectNodeContents(el);var s=getSelection();s.removeAllRanges();s.addRange(r);return true;})()`, quote(sel))
	var ok bool
	if err := r.evalJS(r.ctx, clearJS, &ok); err != nil || !ok {
		return stepErr(st.Line, st.Raw, "js", "清空输入失败")
	}
	if err := chromedp.Run(r.ctx, chromedp.SendKeys(sel, text, chromedp.ByQuery)); err != nil {
		return stepErr(st.Line, st.Raw, "js", "输入失败: "+err.Error())
	}
	return nil
}

// stepSelect SELO <loc> "option"：按 value/text 选择下拉。
func (r *Runner) stepSelect(st *Step) error {
	if len(st.Args) < 2 {
		return stepErr(st.Line, st.Raw, "syntax", "SELO 需要 locator 和选项")
	}
	loc := st.Args[0]
	opt := st.Args[1]
	sel, err := r.resolve(r.ctx, st, loc)
	if err != nil {
		return err
	}
	js := fmt.Sprintf(`(function(){var el=document.querySelector(%s);if(!el||!el.options)return false;var target=null;for(var i=0;i<el.options.length;i++){var o=el.options[i];if(o.value==%s||o.text.trim()==%s){target=o;break;}}if(!target)return false;el.value=target.value;el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return true;})()`,
		quote(sel), quote(opt), quote(opt))
	var ok bool
	if err := r.evalJS(r.ctx, js, &ok); err != nil {
		return stepErr(st.Line, st.Raw, "js", "选择失败: "+err.Error())
	}
	if !ok {
		return stepErr(st.Line, st.Raw, "not_found", "未找到选项 "+opt)
	}
	return nil
}

// ─── 键盘 KPR/KDN/KUP ───
var browserMods = map[string]struct {
	name string
	vk   int64
}{
	"ctrl": {"Control", 17}, "control": {"Control", 17},
	"shift": {"Shift", 16}, "alt": {"Alt", 18},
	"meta": {"Meta", 91}, "win": {"Meta", 91},
}

// keyVK 常用键 → Windows 虚拟键码。
func keyVK(name string) int64 {
	switch strings.ToLower(name) {
	case "enter", "return":
		return 13
	case "tab":
		return 9
	case "space", " ":
		return 32
	case "escape", "esc":
		return 27
	case "backspace":
		return 8
	case "delete", "del":
		return 46
	case "home":
		return 36
	case "end":
		return 35
	case "pageup":
		return 33
	case "pagedown":
		return 34
	case "left":
		return 37
	case "up":
		return 38
	case "right":
		return 39
	case "down":
		return 40
	case "insert", "ins":
		return 45
	}
	if len(name) == 1 {
		c := name[0]
		if c >= 'a' && c <= 'z' {
			return int64(c - 32) // VK 用大写字母码
		}
		if c >= 'A' && c <= 'Z' {
			return int64(c)
		}
		if c >= '0' && c <= '9' {
			return int64(c)
		}
	}
	if len(name) > 1 && (name[0] == 'f' || name[0] == 'F') {
		var n int
		if _, err := fmt.Sscanf(name[1:], "%d", &n); err == nil && n >= 1 && n <= 12 {
			return int64(111 + n)
		}
	}
	return 0
}

// dispatchKey 发送单次键盘事件。
func (r *Runner) dispatchKey(typ input.KeyType, key string, vk int64) error {
	return chromedp.Run(r.ctx, chromedp.ActionFunc(func(c context.Context) error {
		p := input.DispatchKeyEvent(typ).WithKey(key)
		if vk > 0 {
			p = p.WithWindowsVirtualKeyCode(vk)
		}
		return p.Do(c)
	}))
}

func (r *Runner) stepKey(st *Step) error {
	key, ok := strArg(st, 0)
	if !ok {
		return stepErr(st.Line, st.Raw, "syntax", st.Cmd+" 需要按键名")
	}
	action := st.Cmd // KPR/KDN/KUP
	// 组合键：ctrl+a / shift+enter
	if strings.Contains(key, "+") {
		parts := strings.Split(key, "+")
		var mods []struct {
			name string
			vk   int64
		}
		main := parts[len(parts)-1]
		for _, m := range parts[:len(parts)-1] {
			mod, isM := browserMods[strings.ToLower(m)]
			if !isM {
				return stepErr(st.Line, st.Raw, "syntax", "未知修饰键 "+m)
			}
			mods = append(mods, mod)
		}
		return r.comboKey(st, mods, main, action)
	}
	switch action {
	case "KDN":
		if err := r.dispatchKey(input.KeyDown, key, keyVK(key)); err != nil {
			return stepErr(st.Line, st.Raw, "js", "key down 失败: "+err.Error())
		}
	case "KUP":
		if err := r.dispatchKey(input.KeyUp, key, keyVK(key)); err != nil {
			return stepErr(st.Line, st.Raw, "js", "key up 失败: "+err.Error())
		}
	default: // KPR
		if err := chromedp.Run(r.ctx, chromedp.KeyEvent(key)); err != nil {
			return stepErr(st.Line, st.Raw, "js", "按键失败: "+err.Error())
		}
	}
	return nil
}

func (r *Runner) comboKey(st *Step, mods []struct {
	name string
	vk   int64
}, main, action string) error {
	down := func() error {
		for _, m := range mods {
			if err := r.dispatchKey(input.KeyRawDown, m.name, m.vk); err != nil {
				return err
			}
		}
		return nil
	}
	up := func() error {
		for i := len(mods) - 1; i >= 0; i-- {
			if err := r.dispatchKey(input.KeyUp, mods[i].name, mods[i].vk); err != nil {
				return err
			}
		}
		return nil
	}
	switch action {
	case "KDN":
		return down()
	case "KUP":
		return up()
	default:
		if err := down(); err != nil {
			return stepErr(st.Line, st.Raw, "js", err.Error())
		}
		if err := r.dispatchKey(input.KeyRawDown, main, keyVK(main)); err != nil {
			return stepErr(st.Line, st.Raw, "js", err.Error())
		}
		_ = up()
		return nil
	}
}

// stepScroll SCL：SCL <loc>（滚动到元素）| SCL dx,dy（相对滚动）| SCL（默认向下 500）。
func (r *Runner) stepScroll(st *Step) error {
	if len(st.Args) == 0 {
		return r.scrollBy(0, 500, st)
	}
	first := st.Args[0]
	if strings.Contains(first, "=") || strings.HasPrefix(first, "css") || strings.HasPrefix(first, "#") || strings.HasPrefix(first, ".") || strings.HasPrefix(first, "[") {
		sel, err := r.resolve(r.ctx, st, first)
		if err != nil {
			return err
		}
		if err := chromedp.Run(r.ctx, chromedp.ScrollIntoView(sel, chromedp.ByQuery)); err != nil {
			return stepErr(st.Line, st.Raw, "js", err.Error())
		}
		return nil
	}
	if len(st.Args) >= 2 {
		var dx, dy int
		fmt.Sscanf(st.Args[0], "%d", &dx)
		fmt.Sscanf(st.Args[1], "%d", &dy)
		return r.scrollBy(dx, dy, st)
	}
	return r.scrollBy(0, 500, st)
}

func (r *Runner) scrollBy(dx, dy int, st *Step) error {
	js := fmt.Sprintf(`window.scrollBy(%d, %d); true`, dx, dy)
	var ok bool
	if err := r.evalJS(r.ctx, js, &ok); err != nil {
		return stepErr(st.Line, st.Raw, "js", "滚动失败: "+err.Error())
	}
	return nil
}

// stepDrag DRG <from> -> <to>：模拟拖拽（分步移动）。
func (r *Runner) stepDrag(st *Step) error {
	// args: [from, "->", to]（tokenizer 保留 ->）
	var fromLoc, toLoc string
	if len(st.Args) >= 3 && st.Args[1] == "->" {
		fromLoc, toLoc = st.Args[0], st.Args[2]
	} else {
		return stepErr(st.Line, st.Raw, "syntax", "DRG 需要 <from> -> <to>")
	}
	fromSel, err := r.resolve(r.ctx, st, fromLoc)
	if err != nil {
		return err
	}
	toSel, err := r.resolve(r.ctx, st, toLoc)
	if err != nil {
		return err
	}
	fx, fy, err := r.elemCenter(st, fromSel)
	if err != nil {
		return err
	}
	tx, ty, err := r.elemCenter(st, toSel)
	if err != nil {
		return err
	}
	if err := r.dispatchMouse(r.ctx, input.MousePressed, fx, fy, input.Left, 1); err != nil {
		return stepErr(st.Line, st.Raw, "js", err.Error())
	}
	stepsN := 8
	for i := 1; i <= stepsN; i++ {
		x := fx + (tx-fx)*float64(i)/float64(stepsN)
		y := fy + (ty-fy)*float64(i)/float64(stepsN)
		if err := r.dispatchMouse(r.ctx, input.MouseMoved, x, y, input.Left, 1); err != nil {
			return stepErr(st.Line, st.Raw, "js", err.Error())
		}
	}
	if err := r.dispatchMouse(r.ctx, input.MouseReleased, tx, ty, input.Left, 1); err != nil {
		return stepErr(st.Line, st.Raw, "js", err.Error())
	}
	return nil
}

// stepUpload UPF <loc> "file"：给 file input 设置文件。
func (r *Runner) stepUpload(st *Step) error {
	if len(st.Args) < 2 {
		return stepErr(st.Line, st.Raw, "syntax", "UPF 需要 locator 和文件路径")
	}
	// 上传文件路径（R-11，运行时解析：覆盖 {{}} 插值结果与 !/ 临时目录）
	upload, err := fileops.ResolveLocalPath(st.Args[1])
	if err != nil {
		return stepErr(st.Line, st.Raw, "path", err.Error())
	}
	sel, err := r.resolve(r.ctx, st, st.Args[0])
	if err != nil {
		return err
	}
	var nodes []*cdp.Node
	if err := chromedp.Run(r.ctx,
		dom.Enable(),
		chromedp.Nodes(sel, &nodes, chromedp.ByQuery, chromedp.AtLeast(1)),
	); err != nil || len(nodes) == 0 {
		return stepErr(st.Line, st.Raw, "not_found", "未找到上传控件 "+sel)
	}
	nodeID := nodes[0].NodeID
	if err := chromedp.Run(r.ctx, chromedp.ActionFunc(func(c context.Context) error {
		return dom.SetFileInputFiles([]string{upload}).WithNodeID(nodeID).Do(c)
	})); err != nil {
		return stepErr(st.Line, st.Raw, "js", "设置文件失败: "+err.Error())
	}
	return nil
}
