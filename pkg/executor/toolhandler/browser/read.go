package browser

import (
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleWebGetText gets text content of an element.
func HandleWebGetText(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	sel, _ := args["selector"].(string)
	if sel == "" {
		return &types.ToolResult{Success: false, Error: "selector is required", Output: "❌ 获取文本失败：selector 参数缺失", Tool: "web_get_text"}
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_get_text"}
	}

	var text string
	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_get_text"}
	}

	if err := chromedp.Run(ctx, chromedp.Text(sel, &text, chromedp.BySearch)); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("element '%s' not found: %s", sel, err.Error()), Output: fmt.Sprintf("❌ 获取文本失败：%s", err.Error()), Tool: "web_get_text"}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("📄 已获取文本（%d 字符）", len(text)),
		Tool:    "web_get_text",
		RawResult: map[string]interface{}{
			"action": "gettext",
			"text":   text,
			"length": len(text),
		},
	}
}

// HandleWebGetHTML gets HTML content of an element or full page.
func HandleWebGetHTML(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_get_html"}
	}

	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_get_html"}
	}

	sel, hasSel := args["selector"].(string)
	if hasSel && sel != "" {
		var html string
		if err := chromedp.Run(ctx, chromedp.OuterHTML(sel, &html, chromedp.BySearch)); err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("element '%s' not found: %s", sel, err.Error()), Output: fmt.Sprintf("❌ 获取 HTML 失败：%s", err.Error()), Tool: "web_get_html"}
		}
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("📄 已获取 HTML（%d 字符）", len(html)),
			Tool:    "web_get_html",
			RawResult: map[string]interface{}{
				"action": "gethtml",
				"html":   html,
				"length": len(html),
			},
		}
	}

	var html string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.documentElement.outerHTML`, &html)); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("get HTML failed: %s", err.Error()), Output: fmt.Sprintf("❌ 获取 HTML 失败：%s", err.Error()), Tool: "web_get_html"}
	}
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("📄 已获取 HTML（%d 字符）", len(html)),
		Tool:    "web_get_html",
		RawResult: map[string]interface{}{
			"action": "gethtml",
			"html":   html,
			"length": len(html),
		},
	}
}

// HandleWebGetStyle gets a CSS property value of an element.
func HandleWebGetStyle(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	sel, _ := args["selector"].(string)
	prop, _ := args["property"].(string)
	if sel == "" || prop == "" {
		return &types.ToolResult{Success: false, Error: "selector and property are required", Output: "❌ 获取样式失败：需要 selector 和 property 参数", Tool: "web_get_style"}
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_get_style"}
	}

	var value string
	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_get_style"}
	}

	if err := chromedp.Run(ctx,
		chromedp.Evaluate(fmt.Sprintf(
			`(() => { const el = document.querySelector('%s'); return el ? window.getComputedStyle(el).getPropertyValue('%s') : ''; })()`,
			sel, prop,
		), &value),
	); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("get style failed: %s", err.Error()), Output: fmt.Sprintf("❌ 获取样式失败：%s", err.Error()), Tool: "web_get_style"}
	}

	return &types.ToolResult{
		Success: true,
		Output:  "📄 已获取样式",
		Tool:    "web_get_style",
		RawResult: map[string]interface{}{
			"action": "getstyle",
			"name":   prop,
			"value":  value,
		},
	}
}

// HandleWebGetURL gets the current URL of the page.
func HandleWebGetURL(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_get_url"}
	}

	var url string
	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_get_url"}
	}

	if err := chromedp.Run(ctx, chromedp.Location(&url)); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("get URL failed: %s", err.Error()), Output: fmt.Sprintf("❌ 获取 URL 失败：%s", err.Error()), Tool: "web_get_url"}
	}
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("🔗 当前 URL：%s", url),
		Tool:    "web_get_url",
		RawResult: map[string]interface{}{
			"action": "geturl",
			"url":    url,
		},
	}
}

// HandleWebGetTitle gets the page title.
func HandleWebGetTitle(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_get_title"}
	}

	var title string
	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_get_title"}
	}

	if err := chromedp.Run(ctx, chromedp.Title(&title)); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("get title failed: %s", err.Error()), Output: fmt.Sprintf("❌ 获取页面标题失败：%s", err.Error()), Tool: "web_get_title"}
	}
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("📄 页面标题：%s", title),
		Tool:    "web_get_title",
		RawResult: map[string]interface{}{
			"action": "gettitle",
			"title":  title,
		},
	}
}

// HandleWebGetConsole gets console logs from the browser.
func HandleWebGetConsole(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_get_console"}
	}

	inst.mu.Lock()
	defer inst.mu.Unlock()

	if len(inst.consoleLogs) == 0 {
		return &types.ToolResult{Success: true, Output: "📋 控制台日志为空", Tool: "web_get_console", RawResult: map[string]interface{}{"action": "getconsole", "entries": []interface{}{}}}
	}

	logs := inst.consoleLogs
	inst.consoleLogs = nil

	// Build structured entries
	entries := make([]map[string]interface{}, len(logs))
	for i, line := range logs {
		entries[i] = map[string]interface{}{
			"text": line,
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("📋 获取到 %d 条控制台日志", len(logs)),
		Tool:    "web_get_console",
		RawResult: map[string]interface{}{
			"action":  "getconsole",
			"entries": entries,
		},
	}
}

// HandleWebGetRequests gets recorded network requests.
func HandleWebGetRequests(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_get_requests"}
	}

	inst.mu.Lock()
	defer inst.mu.Unlock()

	filter, _ := args["filter"].(string)

	var records []RequestRecord
	for _, r := range inst.requestLogs {
		if filter != "" && !strings.Contains(r.URL, filter) {
			continue
		}
		records = append(records, r)
	}

	if len(records) == 0 {
		return &types.ToolResult{Success: true, Output: "📡 没有匹配的网络请求", Tool: "web_get_requests", RawResult: map[string]interface{}{"action": "getrequests", "requests": []interface{}{}}}
	}

	// Build structured request entries
	type reqEntry struct {
		URL    string `json:"url"`
		Method string `json:"method"`
		Status int    `json:"status"`
		Type   string `json:"type"`
	}
	reqs := make([]reqEntry, len(records))
	for i, r := range records {
		reqs[i] = reqEntry{URL: r.URL, Method: r.Method, Status: r.Status, Type: r.Type}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("📡 获取到 %d 条网络请求", len(records)),
		Tool:    "web_get_requests",
		RawResult: map[string]interface{}{
			"action":   "getrequests",
			"requests": reqs,
		},
	}
}
