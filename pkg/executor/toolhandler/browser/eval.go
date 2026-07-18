package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/task"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleWebEvaluate executes JavaScript in the browser.
func HandleWebEvaluate(bm *BrowserManager, tm *task.TaskManager, args map[string]interface{}) *types.ToolResult {
	js, _ := args["js"].(string)
	if js == "" {
		return &types.ToolResult{Success: false, Error: "js is required", Tool: "web_evaluate"}
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_evaluate"}
	}

	_, output, err := tm.SyncOperation("web_evaluate", func(cancel <-chan struct{}) (string, error) {
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

		var result interface{}
		if err := chromedp.Run(runCtx, chromedp.Evaluate(js, &result)); err != nil {
			return "", fmt.Errorf("js eval failed: %s", err.Error())
		}

		resultJSON, _ := json.Marshal(result)
		return string(resultJSON), nil
	})

	return &types.ToolResult{
		Success: err == nil,
		Output:  output,
		Error:   FormatErr(err),
		Tool:    "web_evaluate",
	}
}

// HandleWebWaitSelector waits for an element to appear in the DOM.
func HandleWebWaitSelector(bm *BrowserManager, tm *task.TaskManager, args map[string]interface{}) *types.ToolResult {
	sel, _ := args["selector"].(string)
	if sel == "" {
		return &types.ToolResult{Success: false, Error: "selector is required", Tool: "web_wait_selector"}
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_wait_selector"}
	}

	_, output, err := tm.SyncOperation("web_wait_selector", func(cancel <-chan struct{}) (string, error) {
		timeout := 10.0
		if v, ok := args["timeout"].(float64); ok && v > 0 {
			timeout = v
		}

		ctx, err := inst.EnsureTab()
		if err != nil {
			return "", err
		}

		// Derive from ctx so cancelling the task also triggers the timeout
		runCtx, runCancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer runCancel()
		go func() {
			select {
			case <-cancel:
				runCancel()
			case <-runCtx.Done():
			}
		}()

		var found bool
		if err := chromedp.Run(runCtx,
			chromedp.ActionFunc(func(ctx context.Context) error {
				var exists bool
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
					}
					if err := chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(
						`document.querySelector('%s') !== null`, sel,
					), &exists)); err != nil {
						return err
					}
					if exists {
						return nil
					}
					time.Sleep(100 * time.Millisecond)
				}
			}),
		); err != nil {
			return "", fmt.Errorf("waiting for '%s' timed out after %.0fs: %s", sel, timeout, err.Error())
		}

		var visible bool
		_ = chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(
			`(() => { const el = document.querySelector('%s'); return el && el.offsetParent !== null; })()`, sel,
		), &visible))

		_ = found
		return fmt.Sprintf("element '%s' visible: %v", sel, visible), nil
	})

	return &types.ToolResult{
		Success: err == nil,
		Output:  output,
		Error:   FormatErr(err),
		Tool:    "web_wait_selector",
	}
}

// HandleWebWaitNavigation waits for page navigation to complete.
func HandleWebWaitNavigation(bm *BrowserManager, tm *task.TaskManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_wait_navigation"}
	}

	_, output, err := tm.SyncOperation("web_wait_navigation", func(cancel <-chan struct{}) (string, error) {
		timeout := 10.0
		if v, ok := args["timeout"].(float64); ok && v > 0 {
			timeout = v
		}

		ctx, err := inst.EnsureTab()
		if err != nil {
			return "", err
		}

		runCtx, runCancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer runCancel()
		go func() {
			select {
			case <-cancel:
				runCancel()
			case <-runCtx.Done():
			}
		}()

		if err := chromedp.Run(runCtx,
			chromedp.WaitReady("body"),
		); err != nil {
			return "", fmt.Errorf("navigation did not complete within %.0fs: %s", timeout, err.Error())
		}

		var url string
		_ = chromedp.Run(ctx, chromedp.Location(&url))
		return fmt.Sprintf("navigation complete, current url: %s", url), nil
	})

	return &types.ToolResult{
		Success: err == nil,
		Output:  output,
		Error:   FormatErr(err),
		Tool:    "web_wait_navigation",
	}
}

// HandleWebSetViewport sets the browser viewport size.
func HandleWebSetViewport(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	width, ok1 := args["width"].(float64)
	height, ok2 := args["height"].(float64)
	if !ok1 || !ok2 {
		return &types.ToolResult{Success: false, Error: "width and height are required", Tool: "web_set_viewport"}
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_set_viewport"}
	}

	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_set_viewport"}
	}

	if err := chromedp.Run(ctx,
		emulation.SetDeviceMetricsOverride(int64(width), int64(height), 1.0, false),
	); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("set viewport failed: %s", err.Error()), Tool: "web_set_viewport"}
	}

	return &types.ToolResult{Success: true, Output: fmt.Sprintf("viewport set to %.0fx%.0f", width, height), Tool: "web_set_viewport"}
}

// HandleWebSetGeolocation sets geolocation override.
func HandleWebSetGeolocation(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_set_geolocation"}
	}

	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_set_geolocation"}
	}

	lat, ok1 := args["latitude"].(float64)
	lon, ok2 := args["longitude"].(float64)
	if !ok1 || !ok2 {
		return &types.ToolResult{Success: false, Error: "latitude and longitude are required", Tool: "web_set_geolocation"}
	}

	accuracy := 100.0
	if v, ok := args["accuracy"].(float64); ok && v > 0 {
		accuracy = v
	}

	if err := chromedp.Run(ctx,
		emulation.SetGeolocationOverride().
			WithLatitude(lat).
			WithLongitude(lon).
			WithAccuracy(accuracy),
	); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("set geolocation failed: %s", err.Error()), Tool: "web_set_geolocation"}
	}

	return &types.ToolResult{Success: true, Output: fmt.Sprintf("geolocation set to (%.4f, %.4f) accuracy=%.0f", lat, lon, accuracy), Tool: "web_set_geolocation"}
}

// ModifierKeyCodes maps modifier names to CDP key identifiers.
var ModifierKeyCodes = map[string]string{
	"ctrl":    "Control",
	"control": "Control",
	"shift":   "Shift",
	"alt":     "Alt",
	"meta":    "Meta",
}

// HandleWebGrantPermission grants browser permissions.
func HandleWebGrantPermission(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_grant_permission"}
	}

	inst.mu.Lock()
	allocCtx := inst.allocCtx
	inst.mu.Unlock()

	if allocCtx == nil {
		return &types.ToolResult{Success: false, Error: "browser not started", Tool: "web_grant_permission"}
	}

	permRaw, ok := args["permissions"].([]interface{})
	if !ok || len(permRaw) == 0 {
		return &types.ToolResult{Success: false, Error: "permissions array is required (e.g. ['geolocation', 'videoCapture', 'audioCapture'])", Tool: "web_grant_permission"}
	}

	var perms []browser.PermissionType
	for _, p := range permRaw {
		s, _ := p.(string)
		switch strings.ToLower(s) {
		case "geolocation":
			perms = append(perms, browser.PermissionTypeGeolocation)
		case "video", "videocapture":
			perms = append(perms, browser.PermissionTypeVideoCapture)
		case "audio", "audiocapture", "microphone":
			perms = append(perms, browser.PermissionTypeAudioCapture)
		case "notifications":
			perms = append(perms, browser.PermissionTypeNotifications)
		case "midi":
			perms = append(perms, browser.PermissionTypeMidiSysex)
		case "clipboard":
			perms = append(perms, browser.PermissionTypeClipboardReadWrite)
		default:
			perms = append(perms, browser.PermissionType(s))
		}
	}

	if err := chromedp.Run(allocCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return browser.GrantPermissions(perms).WithOrigin("").Do(ctx)
		}),
	); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("grant permissions failed: %s", err.Error()), Tool: "web_grant_permission"}
	}

	return &types.ToolResult{Success: true, Output: fmt.Sprintf("granted %d permissions", len(perms)), Tool: "web_grant_permission"}
}
