package browser

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleWebGetText gets text content of an element.
func HandleWebGetText(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	sel, _ := args["selector"].(string)
	if sel == "" {
		return &types.ToolResult{Success: false, Error: "selector is required", Tool: "web_get_text"}
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_get_text"}
	}

	var text string
	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_get_text"}
	}

	if err := chromedp.Run(ctx, chromedp.Text(sel, &text, chromedp.BySearch)); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("element '%s' not found: %s", sel, err.Error()), Tool: "web_get_text"}
	}

	return &types.ToolResult{Success: true, Output: text, Tool: "web_get_text"}
}

// HandleWebGetHTML gets HTML content of an element or full page.
func HandleWebGetHTML(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_get_html"}
	}

	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_get_html"}
	}

	sel, hasSel := args["selector"].(string)
	if hasSel && sel != "" {
		var html string
		if err := chromedp.Run(ctx, chromedp.OuterHTML(sel, &html, chromedp.BySearch)); err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("element '%s' not found: %s", sel, err.Error()), Tool: "web_get_html"}
		}
		return &types.ToolResult{Success: true, Output: html, Tool: "web_get_html"}
	}

	var html string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.documentElement.outerHTML`, &html)); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("get HTML failed: %s", err.Error()), Tool: "web_get_html"}
	}
	return &types.ToolResult{Success: true, Output: html, Tool: "web_get_html"}
}

// HandleWebGetStyle gets a CSS property value of an element.
func HandleWebGetStyle(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	sel, _ := args["selector"].(string)
	prop, _ := args["property"].(string)
	if sel == "" || prop == "" {
		return &types.ToolResult{Success: false, Error: "selector and property are required", Tool: "web_get_style"}
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_get_style"}
	}

	var value string
	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_get_style"}
	}

	if err := chromedp.Run(ctx,
		chromedp.Evaluate(fmt.Sprintf(
			`(() => { const el = document.querySelector('%s'); return el ? window.getComputedStyle(el).getPropertyValue('%s') : ''; })()`,
			sel, prop,
		), &value),
	); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("get style failed: %s", err.Error()), Tool: "web_get_style"}
	}

	return &types.ToolResult{Success: true, Output: fmt.Sprintf("%s: %s", prop, value), Tool: "web_get_style"}
}

// HandleWebGetURL gets the current URL of the page.
func HandleWebGetURL(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_get_url"}
	}

	var url string
	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_get_url"}
	}

	if err := chromedp.Run(ctx, chromedp.Location(&url)); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("get URL failed: %s", err.Error()), Tool: "web_get_url"}
	}
	return &types.ToolResult{Success: true, Output: url, Tool: "web_get_url"}
}

// HandleWebGetTitle gets the page title.
func HandleWebGetTitle(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_get_title"}
	}

	var title string
	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_get_title"}
	}

	if err := chromedp.Run(ctx, chromedp.Title(&title)); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("get title failed: %s", err.Error()), Tool: "web_get_title"}
	}
	return &types.ToolResult{Success: true, Output: title, Tool: "web_get_title"}
}

// HandleWebGetConsole gets console logs from the browser.
func HandleWebGetConsole(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_get_console"}
	}

	inst.mu.Lock()
	defer inst.mu.Unlock()

	if len(inst.consoleLogs) == 0 {
		return &types.ToolResult{Success: true, Output: "(no console output)", Tool: "web_get_console"}
	}

	logs := strings.Join(inst.consoleLogs, "\n")
	inst.consoleLogs = nil
	return &types.ToolResult{Success: true, Output: logs, Tool: "web_get_console"}
}

// HandleWebGetRequests gets recorded network requests.
func HandleWebGetRequests(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_get_requests"}
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
		return &types.ToolResult{Success: true, Output: "(no matching requests)", Tool: "web_get_requests"}
	}

	data, _ := json.Marshal(records)
	return &types.ToolResult{
		Success: true,
		Output:  string(data),
		Tool:    "web_get_requests",
	}
}
