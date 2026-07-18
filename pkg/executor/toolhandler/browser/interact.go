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
		return &types.ToolResult{Success: false, Error: "either selector or (x,y) required", Tool: "web_click"}
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
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_click"}
	}

	_, output, err := tm.SyncOperation("web_click", func(cancel <-chan struct{}) (string, error) {
		ctx, err := inst.EnsureTab()
		if err != nil {
			return "", err
		}

		runCtx, runCancel := context.WithCancel(ctx)
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

	return &types.ToolResult{
		Success: err == nil,
		Output:  output,
		Error:   FormatErr(err),
		Tool:    "web_click",
	}
}

// HandleWebType types text into an element.
func HandleWebType(bm *BrowserManager, tm *task.TaskManager, args map[string]interface{}) *types.ToolResult {
	sel, _ := args["selector"].(string)
	text, _ := args["text"].(string)
	if sel == "" {
		return &types.ToolResult{Success: false, Error: "selector is required", Tool: "web_type"}
	}

	delay := 50.0
	if v, ok := args["delay"].(float64); ok && v >= 0 {
		delay = v
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_type"}
	}

	_, output, err := tm.SyncOperation("web_type", func(cancel <-chan struct{}) (string, error) {
		ctx, err := inst.EnsureTab()
		if err != nil {
			return "", err
		}

		runCtx, runCancel := context.WithCancel(ctx)
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

	return &types.ToolResult{
		Success: err == nil,
		Output:  output,
		Error:   FormatErr(err),
		Tool:    "web_type",
	}
}

// HandleWebHover hovers over an element or at coordinates.
func HandleWebHover(bm *BrowserManager, tm *task.TaskManager, args map[string]interface{}) *types.ToolResult {
	sel, _ := args["selector"].(string)
	x, hasX := args["x"].(float64)
	y, hasY := args["y"].(float64)

	if sel == "" && (!hasX || !hasY) {
		return &types.ToolResult{Success: false, Error: "either selector or (x,y) required", Tool: "web_hover"}
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_hover"}
	}

	_, output, err := tm.SyncOperation("web_hover", func(cancel <-chan struct{}) (string, error) {
		ctx, err := inst.EnsureTab()
		if err != nil {
			return "", err
		}

		runCtx, runCancel := context.WithCancel(ctx)
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

			if err := chromedp.Run(runCtx, MouseMoveAction(pos.X, pos.Y)); err != nil {
				return "", fmt.Errorf("hover failed: %s", err.Error())
			}
			return fmt.Sprintf("hovered %s at (%.0f, %.0f)", sel, pos.X, pos.Y), nil
		}

		if err := chromedp.Run(runCtx, MouseMoveAction(x, y)); err != nil {
			return "", fmt.Errorf("hover at (%.0f, %.0f) failed: %s", x, y, err.Error())
		}
		return fmt.Sprintf("hovered at (%.0f, %.0f)", x, y), nil
	})

	return &types.ToolResult{
		Success: err == nil,
		Output:  output,
		Error:   FormatErr(err),
		Tool:    "web_hover",
	}
}

// HandleWebDrag drags from one point to another.
func HandleWebDrag(bm *BrowserManager, tm *task.TaskManager, args map[string]interface{}) *types.ToolResult {
	fromX, ok1 := args["from_x"].(float64)
	fromY, ok2 := args["from_y"].(float64)
	toX, ok3 := args["to_x"].(float64)
	toY, ok4 := args["to_y"].(float64)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return &types.ToolResult{Success: false, Error: "from_x, from_y, to_x, to_y required", Tool: "web_drag"}
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_drag"}
	}

	_, output, err := tm.SyncOperation("web_drag", func(cancel <-chan struct{}) (string, error) {
		ctx, err := inst.EnsureTab()
		if err != nil {
			return "", err
		}

		runCtx, runCancel := context.WithCancel(ctx)
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

	return &types.ToolResult{
		Success: err == nil,
		Output:  output,
		Error:   FormatErr(err),
		Tool:    "web_drag",
	}
}

// HandleWebMouseDown handles mouse down event in browser.
func HandleWebMouseDown(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_mouse_down"}
	}

	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_mouse_down"}
	}

	btnStr, ok := args["button"].(string)
	if !ok || btnStr == "" {
		btnStr = "left"
	}
	var cdpButton input.MouseButton
	switch strings.ToLower(btnStr) {
	case "right":
		cdpButton = input.Right
	case "middle":
		cdpButton = input.Middle
	default:
		cdpButton = input.Left
	}

	x, _ := args["x"].(float64)
	y, _ := args["y"].(float64)

	if err := chromedp.Run(ctx, MouseDownAction(x, y, cdpButton)); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("mouse down failed: %s", err.Error()), Tool: "web_mouse_down"}
	}
	return &types.ToolResult{Success: true, Output: fmt.Sprintf("mouse %s down at (%.0f, %.0f)", btnStr, x, y), Tool: "web_mouse_down"}
}

// HandleWebMouseUp handles mouse up event in browser.
func HandleWebMouseUp(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_mouse_up"}
	}

	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_mouse_up"}
	}

	btnStr, ok := args["button"].(string)
	if !ok || btnStr == "" {
		btnStr = "left"
	}
	var cdpButton input.MouseButton
	switch strings.ToLower(btnStr) {
	case "right":
		cdpButton = input.Right
	case "middle":
		cdpButton = input.Middle
	default:
		cdpButton = input.Left
	}

	x, _ := args["x"].(float64)
	y, _ := args["y"].(float64)

	if err := chromedp.Run(ctx, MouseUpAction(x, y, cdpButton)); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("mouse up failed: %s", err.Error()), Tool: "web_mouse_up"}
	}
	return &types.ToolResult{Success: true, Output: fmt.Sprintf("mouse %s up at (%.0f, %.0f)", btnStr, x, y), Tool: "web_mouse_up"}
}

// HandleWebMouseMove moves mouse to coordinates in browser.
func HandleWebMouseMove(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_mouse_move"}
	}

	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_mouse_move"}
	}

	x, ok1 := args["x"].(float64)
	y, ok2 := args["y"].(float64)
	if !ok1 || !ok2 {
		return &types.ToolResult{Success: false, Error: "x and y required", Tool: "web_mouse_move"}
	}

	if err := chromedp.Run(ctx, MouseMoveAction(x, y)); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("mouse move failed: %s", err.Error()), Tool: "web_mouse_move"}
	}
	return &types.ToolResult{Success: true, Output: fmt.Sprintf("mouse moved to (%.0f, %.0f)", x, y), Tool: "web_mouse_move"}
}

// HandleWebScrollWheel scrolls the browser page.
func HandleWebScrollWheel(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_scroll_wheel"}
	}

	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_scroll_wheel"}
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
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("scroll failed at step %d: %s", i+1, err.Error()), Tool: "web_scroll_wheel"}
		}
	}

	return &types.ToolResult{Success: true, Output: fmt.Sprintf("scrolled (%.0f, %.0f) in %.0f steps", deltaX, deltaY, steps), Tool: "web_scroll_wheel"}
}

// HandleWebScrollTo scrolls to an element or coordinates.
func HandleWebScrollTo(bm *BrowserManager, tm *task.TaskManager, args map[string]interface{}) *types.ToolResult {
	sel, hasSel := args["selector"].(string)
	x, hasX := args["x"].(float64)
	y, hasY := args["y"].(float64)

	if !hasSel && (!hasX || !hasY) {
		return &types.ToolResult{Success: false, Error: "either selector or (x,y) required", Tool: "web_scroll_to"}
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_scroll_to"}
	}

	_, output, err := tm.SyncOperation("web_scroll_to", func(cancel <-chan struct{}) (string, error) {
		ctx, err := inst.EnsureTab()
		if err != nil {
			return "", err
		}

		runCtx, runCancel := context.WithCancel(ctx)
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

	return &types.ToolResult{
		Success: err == nil,
		Output:  output,
		Error:   FormatErr(err),
		Tool:    "web_scroll_to",
	}
}
