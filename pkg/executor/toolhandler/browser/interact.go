package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/task"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleWebClick clicks on an element or at coordinates.
func HandleWebClick(bm *BrowserManager, tm *task.TaskManager, args map[string]interface{}) *types.ToolResult {
	sel, _ := args["selector"].(string)
	x, hasX := args["x"].(float64)
	y, hasY := args["y"].(float64)

	if sel == "" && (!hasX || !hasY) {
		return &types.ToolResult{Success: false, Error: "either selector or (x,y) required", Output: "❌ 点击失败：需要选择器或坐标参数", Tool: "web_click"}
	}

	btnStr, _ := args["button"].(string)
	var cdpButton input.MouseButton
	switch strings.ToLower(btnStr) {
	case "right":
		cdpButton = input.Right
	case "middle":
		cdpButton = input.Middle
	default:
		cdpButton = input.Left
	}

	clickCount := int64(1)
	if v, ok := args["click_count"].(float64); ok && v >= 1 {
		clickCount = int64(v)
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_click"}
	}

	_, _, err = tm.SyncOperation("web_click", func(cancel <-chan struct{}) (string, error) {
		ctx, err := inst.EnsureTab()
		if err != nil {
			return "", err
		}

		const clickTimeout = 15 * time.Second
		runCtx, runCancel := context.WithTimeout(ctx, clickTimeout)
		defer runCancel()
		go func() {
			select {
			case <-cancel:
				runCancel()
			case <-runCtx.Done():
			}
		}()

		if sel != "" {
			if err := chromedp.Run(runCtx,
				chromedp.Click(sel, chromedp.BySearch),
			); err != nil {
				return "", fmt.Errorf("click '%s' failed: %s", sel, err.Error())
			}
			return fmt.Sprintf("clicked %s", sel), nil
		}

		if err := chromedp.Run(runCtx, MouseClickAction(x, y, cdpButton, clickCount)); err != nil {
			return "", fmt.Errorf("click at (%.0f, %.0f) failed: %s", x, y, err.Error())
		}
		return fmt.Sprintf("clicked at (%.0f, %.0f)", x, y), nil
	})

	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   FormatErr(err),
			Output:  fmt.Sprintf("❌ 点击失败：%s", FormatErr(err)),
			Tool:    "web_click",
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  "🖱️ 已点击",
		Tool:    "web_click",
		RawResult: map[string]interface{}{
			"action":   "click",
			"selector": sel,
			"x":        x,
			"y":        y,
		},
	}
}

// HandleWebType types text into an element.
func HandleWebType(bm *BrowserManager, tm *task.TaskManager, args map[string]interface{}) *types.ToolResult {
	sel, _ := args["selector"].(string)
	text, _ := args["text"].(string)
	if sel == "" {
		return &types.ToolResult{Success: false, Error: "selector is required", Output: "❌ 输入失败：selector 参数缺失", Tool: "web_type"}
	}

	delay := 50.0
	if v, ok := args["delay"].(float64); ok && v >= 0 {
		delay = v
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_type"}
	}

	_, _, err = tm.SyncOperation("web_type", func(cancel <-chan struct{}) (string, error) {
		ctx, err := inst.EnsureTab()
		if err != nil {
			return "", err
		}

		const opTimeout = 15 * time.Second
		runCtx, runCancel := context.WithTimeout(ctx, opTimeout)
		defer runCancel()
		go func() {
			select {
			case <-cancel:
				runCancel()
			case <-runCtx.Done():
			}
		}()

		if err := chromedp.Run(runCtx,
			chromedp.Click(sel, chromedp.BySearch),
			chromedp.ActionFunc(func(ctx context.Context) error {
				_, _, err := runtime.Evaluate(`document.activeElement.select()`).Do(ctx)
				return err
			}),
		); err != nil {
			return "", fmt.Errorf("focus element '%s' failed: %s", sel, err.Error())
		}

		if delay < 10 {
			if err := chromedp.Run(runCtx, TextInputAction(text)); err != nil {
				return "", fmt.Errorf("type text failed: %s", err.Error())
			}
		} else {
			for _, ch := range text {
				select {
				case <-cancel:
					return "", fmt.Errorf("cancelled")
				default:
				}
				if err := chromedp.Run(runCtx, TextInputAction(string(ch))); err != nil {
					return "", fmt.Errorf("type char failed: %s", err.Error())
				}
				time.Sleep(time.Duration(delay) * time.Millisecond)
			}
		}

		return fmt.Sprintf("typed %d characters into %s", len(text), sel), nil
	})

	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   FormatErr(err),
			Output:  fmt.Sprintf("❌ 输入失败：%s", FormatErr(err)),
			Tool:    "web_type",
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("⌨️ 已输入 %d 字符", len(text)),
		Tool:    "web_type",
		RawResult: map[string]interface{}{
			"action":      "type",
			"text_length": len(text),
			"selector":    sel,
		},
	}
}

// HandleWebHover hovers over an element or at coordinates.
func HandleWebHover(bm *BrowserManager, tm *task.TaskManager, args map[string]interface{}) *types.ToolResult {
	sel, _ := args["selector"].(string)
	x, hasX := args["x"].(float64)
	y, hasY := args["y"].(float64)

	if sel == "" && (!hasX || !hasY) {
		return &types.ToolResult{Success: false, Error: "either selector or (x,y) required", Output: "❌ 悬停失败：需要选择器或坐标参数", Tool: "web_hover"}
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_hover"}
	}

	var clickX, clickY float64
	_, _, err = tm.SyncOperation("web_hover", func(cancel <-chan struct{}) (string, error) {
		ctx, err := inst.EnsureTab()
		if err != nil {
			return "", err
		}

		runCtx, runCancel := context.WithTimeout(ctx, 15*time.Second)
		defer runCancel()
		go func() {
			select {
			case <-cancel:
				runCancel()
			case <-runCtx.Done():
			}
		}()

		if sel != "" {
			var rectStr string
			if err := chromedp.Run(runCtx,
				chromedp.Evaluate(fmt.Sprintf(`(() => {
					const el = document.querySelector('%s');
					if (!el) return '';
					const r = el.getBoundingClientRect();
					return JSON.stringify({x: r.x + r.width/2, y: r.y + r.height/2});
				})()`, sel), &rectStr),
			); err != nil || rectStr == "" {
				if err != nil {
					return "", fmt.Errorf("element '%s' not found: %s", sel, err.Error())
				}
				return "", fmt.Errorf("element '%s' not found", sel)
			}

			var pos struct {
				X float64 `json:"x"`
				Y float64 `json:"y"`
			}
			if err := json.Unmarshal([]byte(rectStr), &pos); err != nil {
				return "", fmt.Errorf("parse element position failed: %s", err.Error())
			}

			clickX = pos.X
			clickY = pos.Y
			if err := chromedp.Run(runCtx, MouseMoveAction(pos.X, pos.Y)); err != nil {
				return "", fmt.Errorf("hover failed: %s", err.Error())
			}
			return fmt.Sprintf("hovered %s at (%.0f, %.0f)", sel, pos.X, pos.Y), nil
		}

		clickX = x
		clickY = y
		if err := chromedp.Run(runCtx, MouseMoveAction(x, y)); err != nil {
			return "", fmt.Errorf("hover at (%.0f, %.0f) failed: %s", x, y, err.Error())
		}
		return fmt.Sprintf("hovered at (%.0f, %.0f)", x, y), nil
	})

	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   FormatErr(err),
			Output:  fmt.Sprintf("❌ 悬停失败：%s", FormatErr(err)),
			Tool:    "web_hover",
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  "🖱️ 已悬停",
		Tool:    "web_hover",
		RawResult: map[string]interface{}{
			"action":   "hover",
			"selector": sel,
			"x":        clickX,
			"y":        clickY,
		},
	}
}

// HandleWebDrag drags from one point to another.
func HandleWebDrag(bm *BrowserManager, tm *task.TaskManager, args map[string]interface{}) *types.ToolResult {
	fromX, ok1 := args["from_x"].(float64)
	fromY, ok2 := args["from_y"].(float64)
	toX, ok3 := args["to_x"].(float64)
	toY, ok4 := args["to_y"].(float64)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return &types.ToolResult{Success: false, Error: "from_x, from_y, to_x, to_y required", Output: "❌ 拖拽失败：需要 from_x, from_y, to_x, to_y 参数", Tool: "web_drag"}
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_drag"}
	}

	_, _, err = tm.SyncOperation("web_drag", func(cancel <-chan struct{}) (string, error) {
		ctx, err := inst.EnsureTab()
		if err != nil {
			return "", err
		}

		runCtx, runCancel := context.WithTimeout(ctx, 15*time.Second)
		defer runCancel()
		go func() {
			select {
			case <-cancel:
				runCancel()
			case <-runCtx.Done():
			}
		}()

		if err := chromedp.Run(runCtx,
			MouseMoveAction(fromX, fromY),
			MouseDownAction(fromX, fromY, input.Left),
			MouseMoveAction(toX, toY),
			MouseUpAction(toX, toY, input.Left),
		); err != nil {
			return "", fmt.Errorf("drag failed: %s", err.Error())
		}
		return fmt.Sprintf("dragged from (%.0f,%.0f) to (%.0f,%.0f)", fromX, fromY, toX, toY), nil
	})

	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   FormatErr(err),
			Output:  fmt.Sprintf("❌ 拖拽失败：%s", FormatErr(err)),
			Tool:    "web_drag",
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  "🖱️ 已拖拽",
		Tool:    "web_drag",
		RawResult: map[string]interface{}{
			"action": "drag",
			"from_x": fromX,
			"from_y": fromY,
			"to_x":   toX,
			"to_y":   toY,
		},
	}
}

// HandleWebMouseDown handles mouse down event in browser.
func HandleWebMouseDown(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_mouse_down"}
	}

	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_mouse_down"}
	}

	btnStr, ok := args["button"].(string)
	if !ok || btnStr == "" {
		btnStr = "left"
	}

	x, _ := args["x"].(float64)
	y, _ := args["y"].(float64)

	if err := chromedp.Run(ctx, MouseDownAction(x, y, buttonFromString(btnStr))); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("mouse down failed: %s", err.Error()), Output: fmt.Sprintf("❌ 鼠标按下失败：%s", err.Error()), Tool: "web_mouse_down"}
	}
	return &types.ToolResult{
		Success: true,
		Output:  "🖱️ 鼠标已按下",
		Tool:    "web_mouse_down",
		RawResult: map[string]interface{}{
			"action": "mousedown",
			"x":      x,
			"y":      y,
		},
	}
}

// HandleWebMouseUp handles mouse up event in browser.
func HandleWebMouseUp(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_mouse_up"}
	}

	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_mouse_up"}
	}

	btnStr, ok := args["button"].(string)
	if !ok || btnStr == "" {
		btnStr = "left"
	}

	x, _ := args["x"].(float64)
	y, _ := args["y"].(float64)

	if err := chromedp.Run(ctx, MouseUpAction(x, y, buttonFromString(btnStr))); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("mouse up failed: %s", err.Error()), Output: fmt.Sprintf("❌ 鼠标释放失败：%s", err.Error()), Tool: "web_mouse_up"}
	}
	return &types.ToolResult{
		Success: true,
		Output:  "🖱️ 鼠标已释放",
		Tool:    "web_mouse_up",
		RawResult: map[string]interface{}{
			"action": "mouseup",
			"x":      x,
			"y":      y,
		},
	}
}

// HandleWebMouseMove moves mouse to coordinates in browser.
func HandleWebMouseMove(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_mouse_move"}
	}

	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_mouse_move"}
	}

	x, ok1 := args["x"].(float64)
	y, ok2 := args["y"].(float64)
	if !ok1 || !ok2 {
		return &types.ToolResult{Success: false, Error: "x and y required", Output: "❌ 鼠标移动失败：x 和 y 参数必填", Tool: "web_mouse_move"}
	}

	if err := chromedp.Run(ctx, MouseMoveAction(x, y)); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("mouse move failed: %s", err.Error()), Output: fmt.Sprintf("❌ 鼠标移动失败：%s", err.Error()), Tool: "web_mouse_move"}
	}
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("🖱️ 鼠标已移动 (%.0f, %.0f)", x, y),
		Tool:    "web_mouse_move",
		RawResult: map[string]interface{}{
			"action": "mousemove",
			"x":      x,
			"y":      y,
		},
	}
}

// HandleWebScrollWheel scrolls the browser page.
func HandleWebScrollWheel(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_scroll_wheel"}
	}

	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_scroll_wheel"}
	}

	deltaX, _ := args["delta_x"].(float64)
	deltaY, _ := args["delta_y"].(float64)

	steps := 1.0
	if v, ok := args["steps"].(float64); ok && v > 0 {
		steps = v
	}

	x, _ := args["x"].(float64)
	y, _ := args["y"].(float64)

	stepDeltaX := deltaX / steps
	stepDeltaY := deltaY / steps

	for i := 0; i < int(steps); i++ {
		if err := chromedp.Run(ctx, ScrollWheelAction(x, y, stepDeltaX, stepDeltaY)); err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("scroll failed at step %d: %s", i+1, err.Error()), Output: fmt.Sprintf("❌ 滚动失败：%s", err.Error()), Tool: "web_scroll_wheel"}
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  "🖱️ 已滚动",
		Tool:    "web_scroll_wheel",
		RawResult: map[string]interface{}{
			"action":  "scrollwheel",
			"delta_x": deltaX,
			"delta_y": deltaY,
		},
	}
}

// HandleWebScrollTo scrolls to an element or coordinates.
func HandleWebScrollTo(bm *BrowserManager, tm *task.TaskManager, args map[string]interface{}) *types.ToolResult {
	sel, hasSel := args["selector"].(string)
	x, hasX := args["x"].(float64)
	y, hasY := args["y"].(float64)

	if !hasSel && (!hasX || !hasY) {
		return &types.ToolResult{Success: false, Error: "either selector or (x,y) required", Output: "❌ 滚动失败：需要选择器或坐标参数", Tool: "web_scroll_to"}
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: "❌ " + err.Error(), Tool: "web_scroll_to"}
	}

	_, _, err = tm.SyncOperation("web_scroll_to", func(cancel <-chan struct{}) (string, error) {
		ctx, err := inst.EnsureTab()
		if err != nil {
			return "", err
		}

		runCtx, runCancel := context.WithTimeout(ctx, 15*time.Second)
		defer runCancel()
		go func() {
			select {
			case <-cancel:
				runCancel()
			case <-runCtx.Done():
			}
		}()

		if hasSel && sel != "" {
			if err := chromedp.Run(runCtx,
				chromedp.ActionFunc(func(ctx context.Context) error {
					_, _, err := runtime.Evaluate(fmt.Sprintf(
						`document.querySelector('%s').scrollIntoView({behavior:'instant',block:'center'})`, sel,
					)).Do(ctx)
					return err
				}),
			); err != nil {
				return "", fmt.Errorf("scroll to element '%s' failed: %s", sel, err.Error())
			}
			return "scrolled to " + sel, nil
		}

		if err := chromedp.Run(runCtx,
			chromedp.ActionFunc(func(ctx context.Context) error {
				_, _, err := runtime.Evaluate(fmt.Sprintf(`window.scrollTo(%.0f, %.0f)`, x, y)).Do(ctx)
				return err
			}),
		); err != nil {
			return "", fmt.Errorf("scroll to (%.0f, %.0f) failed: %s", x, y, err.Error())
		}
		return fmt.Sprintf("scrolled to (%.0f, %.0f)", x, y), nil
	})

	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   FormatErr(err),
			Output:  fmt.Sprintf("❌ 滚动失败：%s", FormatErr(err)),
			Tool:    "web_scroll_to",
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("📜 已滚动到 (%.0f, %.0f)", x, y),
		Tool:    "web_scroll_to",
		RawResult: map[string]interface{}{
			"action": "scrollto",
			"x":      x,
			"y":      y,
		},
	}
}

// buttonFromString converts a button string name to CDP MouseButton.
func buttonFromString(s string) input.MouseButton {
	switch strings.ToLower(s) {
	case "right":
		return input.Right
	case "middle":
		return input.Middle
	default:
		return input.Left
	}
}
