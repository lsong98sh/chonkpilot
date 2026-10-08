package browser

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// tabInfo 页面 target 摘要。
type tabInfo struct {
	ID    string
	Title string
	URL   string
	Cur   bool
}

// listTargets 枚举所有页面（type=page）target。
func (r *Runner) listTargets() ([]tabInfo, error) {
	var infos []*target.Info
	err := chromedp.Run(r.browserCtx, chromedp.ActionFunc(func(c context.Context) error {
		list, e := target.GetTargets().Do(c)
		if e != nil {
			return e
		}
		infos = list
		return nil
	}))
	if err != nil {
		return nil, err
	}
	var out []tabInfo
	for _, in := range infos {
		if in.Type != "page" {
			continue
		}
		out = append(out, tabInfo{ID: in.TargetID.String(), Title: in.Title, URL: in.URL})
	}
	return out, nil
}

// activate 切换当前激活 tab。
func (r *Runner) activate(id string) error {
	if r.curID == id {
		return nil
	}
	ctx, _ := chromedp.NewContext(r.browserCtx, chromedp.WithTargetID(target.ID(id)))
	r.ctx = ctx
	r.curID = id
	chromedp.ListenTarget(ctx, r.onTargetEvent)
	return nil
}

// maxConsoleLines 浏览器 console/异常日志硬上限（滑动窗口保留最新 N 条；
// 原配置项 browserLogCap 已删除，改由代码内兜底，防止长会话无限累积）。
const maxConsoleLines = 2000

// appendConsole 追加一条 console 日志（超上限时原地丢弃最旧一条；
// 由 chromedp 事件回调 goroutine 调用，与 DSL 主 goroutine 并发，须持 mu）。
func (r *Runner) appendConsole(line string) {
	r.mu.Lock()
	if len(r.console) >= maxConsoleLines {
		r.console = append(r.console[:0], r.console[1:]...)
	}
	r.console = append(r.console, line)
	r.mu.Unlock()
}

func (r *Runner) onTargetEvent(ev interface{}) {
	switch e := ev.(type) {
	case *runtime.EventConsoleAPICalled:
		var parts []string
		for _, a := range e.Args {
			parts = append(parts, formatRemoteObject(a))
		}
		r.appendConsole(fmt.Sprintf("[console.%s] %s", e.Type, strings.Join(parts, " ")))
	case *runtime.EventExceptionThrown:
		msg := "exception"
		if e.ExceptionDetails != nil {
			msg = e.ExceptionDetails.Text
			if e.ExceptionDetails.Exception != nil {
				msg += " " + formatRemoteObject(e.ExceptionDetails.Exception)
			}
		}
		r.appendConsole("[page.error] " + msg)
	}
}

// stepTab TAB list|wait new|switch|close。
func (r *Runner) stepTab(st *Step) error {
	if len(st.Args) == 0 {
		return stepErr(st.Line, st.Raw, "syntax", "TAB 需要操作 list|wait new|switch <t>|close")
	}
	op := strings.ToLower(st.Args[0])
	switch op {
	case "list":
		tabs, err := r.listTargets()
		if err != nil {
			return stepErr(st.Line, st.Raw, "js", "列 tab 失败: "+err.Error())
		}
		var lines []string
		for i, t := range tabs {
			mark := " "
			if t.ID == r.curID {
				mark = "*"
			}
			lines = append(lines, fmt.Sprintf("%s[%d] %s | %s", mark, i, t.Title, t.URL))
		}
		r.appendOut("TAB LIST:\n" + strings.Join(lines, "\n"))
		return nil

	case "wait":
		if len(st.Args) < 2 {
			return stepErr(st.Line, st.Raw, "syntax", "TAB wait 需要 new")
		}
		if !strings.EqualFold(st.Args[1], "new") {
			return stepErr(st.Line, st.Raw, "syntax", "TAB wait 仅支持 new")
		}
		return r.waitNewTab(st)

	case "switch":
		if len(st.Args) < 2 {
			return stepErr(st.Line, st.Raw, "syntax", "TAB switch 需要标题/URL/序号")
		}
		tabs, err := r.listTargets()
		if err != nil {
			return stepErr(st.Line, st.Raw, "js", "列 tab 失败: "+err.Error())
		}
		want := st.Args[1]
		var matched *tabInfo
		if idx, err := strconv.Atoi(want); err == nil && idx >= 0 && idx < len(tabs) {
			matched = &tabs[idx]
		} else {
			for i := range tabs {
				if strings.Contains(tabs[i].Title, want) || strings.Contains(tabs[i].URL, want) {
					matched = &tabs[i]
					break
				}
			}
		}
		if matched == nil {
			return stepErr(st.Line, st.Raw, "not_found", "未找到 tab: "+want)
		}
		if err := r.activate(matched.ID); err != nil {
			return stepErr(st.Line, st.Raw, "js", "切换 tab 失败: "+err.Error())
		}
		return nil

	case "close":
		if r.curID == "" {
			return stepErr(st.Line, st.Raw, "js", "无当前 tab")
		}
		id := r.curID
		tabs, err := r.listTargets()
		if err != nil {
			return stepErr(st.Line, st.Raw, "js", err.Error())
		}
		// 切到其它第一个 tab，再关
		for _, t := range tabs {
			if t.ID != id {
				_ = r.activate(t.ID)
				break
			}
		}
		if r.curID == id { // 无其它 tab
			r.curID = ""
		}
		if err := chromedp.Run(r.browserCtx, chromedp.ActionFunc(func(c context.Context) error {
			return target.CloseTarget(target.ID(id)).Do(c)
		})); err != nil {
			return stepErr(st.Line, st.Raw, "js", "关闭 tab 失败: "+err.Error())
		}
		return nil

	default:
		return stepErr(st.Line, st.Raw, "syntax", "未知 TAB 操作 "+op)
	}
}

// waitNewTab 等待出现新 tab（window.open/新链接）并切换。
func (r *Runner) waitNewTab(st *Step) error {
	base, err := r.listTargets()
	if err != nil {
		return stepErr(st.Line, st.Raw, "js", err.Error())
	}
	known := map[string]bool{}
	for _, t := range base {
		known[t.ID] = true
	}
	timeout := time.Duration(r.watTimeout()) * time.Millisecond
	deadline := time.Now().Add(timeout)
	for {
		tabs, err := r.listTargets()
		if err == nil {
			for _, t := range tabs {
				if !known[t.ID] {
					if err := r.activate(t.ID); err != nil {
						return stepErr(st.Line, st.Raw, "js", "激活新 tab 失败: "+err.Error())
					}
					r.appendOut(fmt.Sprintf("TAB NEW: %s | %s", t.Title, t.URL))
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return stepErr(st.Line, st.Raw, "timeout", "等待新 tab 超时")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (r *Runner) watTimeout() int64 {
	if r.opt.WatTimeoutMs <= 0 {
		return 10000
	}
	return r.opt.WatTimeoutMs
}
