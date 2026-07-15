package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"go.uber.org/zap"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/task"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// FormatErr converts an error to its string form, or empty string if nil.
func FormatErr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// BrowserManager manages a single headless/full browser instance.
type BrowserManager struct {
	mu            sync.Mutex
	allocCtx      context.Context
	allocCancel   context.CancelFunc
	tabCtx        context.Context
	tabCancel     context.CancelFunc
	consoleLogs   []string
	requestLogs   []RequestRecord
	ready         bool
}

// RequestRecord stores a single network request.
type RequestRecord struct {
	URL       string `json:"url"`
	Method    string `json:"method"`
	Status    int    `json:"status,omitempty"`
	Type      string `json:"type,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// NewBrowserManager creates a new BrowserManager.
func NewBrowserManager() *BrowserManager {
	return &BrowserManager{}
}

// EnsureTab returns a tab context; creates one lazily.
func (bm *BrowserManager) EnsureTab() (context.Context, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.allocCtx == nil {
		return nil, fmt.Errorf("browser not started")
	}
	if !bm.ready {
		ctx, cancel := chromedp.NewContext(bm.allocCtx, chromedp.WithLogf(func(format string, args ...interface{}) {
		}))
		bm.tabCtx = ctx
		bm.tabCancel = cancel
		bm.ready = true

		chromedp.ListenTarget(ctx, func(ev interface{}) {
			switch e := ev.(type) {
			case *runtime.EventConsoleAPICalled:
				var parts []string
				for _, arg := range e.Args {
					val := string(arg.Value)
					parts = append(parts, val)
				}
				line := fmt.Sprintf("[%s] %s", e.Type.String(), strings.Join(parts, " "))
				bm.mu.Lock()
				bm.consoleLogs = append(bm.consoleLogs, line)
				if len(bm.consoleLogs) > 500 {
					bm.consoleLogs = bm.consoleLogs[len(bm.consoleLogs)-500:]
				}
				bm.mu.Unlock()

			case *network.EventRequestWillBeSent:
				rec := RequestRecord{
					URL:       e.Request.URL,
					Method:    e.Request.Method,
					Type:      string(e.Type),
					Timestamp: time.Now().UnixMilli(),
				}
				bm.mu.Lock()
				bm.requestLogs = append(bm.requestLogs, rec)
				if len(bm.requestLogs) > 500 {
					bm.requestLogs = bm.requestLogs[len(bm.requestLogs)-500:]
				}
				bm.mu.Unlock()

			case *network.EventResponseReceived:
				url := e.Response.URL
				statusCode := e.Response.Status
				bm.mu.Lock()
				for i := len(bm.requestLogs) - 1; i >= 0; i-- {
					if bm.requestLogs[i].URL == url && bm.requestLogs[i].Status == 0 {
						bm.requestLogs[i].Status = int(statusCode)
						break
					}
				}
				bm.mu.Unlock()
			}
		})
	}
	return bm.tabCtx, nil
}

// ─── Actions for mouse operations ────────────

// MouseClickAction performs a mouse click at (x, y).
func MouseClickAction(x, y float64, button input.MouseButton, clickCount int64) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if err := input.DispatchMouseEvent(input.MousePressed, x, y).
			WithButton(button).
			WithClickCount(clickCount).
			Do(ctx); err != nil {
			return err
		}
		return input.DispatchMouseEvent(input.MouseReleased, x, y).
			WithButton(button).
			WithClickCount(clickCount).
			Do(ctx)
	})
}

// MouseMoveAction moves mouse to (x, y).
func MouseMoveAction(x, y float64) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return input.DispatchMouseEvent(input.MouseMoved, x, y).Do(ctx)
	})
}

// MouseDownAction presses a mouse button at (x, y).
func MouseDownAction(x, y float64, button input.MouseButton) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return input.DispatchMouseEvent(input.MousePressed, x, y).
			WithButton(button).
			Do(ctx)
	})
}

// MouseUpAction releases a mouse button at (x, y).
func MouseUpAction(x, y float64, button input.MouseButton) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return input.DispatchMouseEvent(input.MouseReleased, x, y).
			WithButton(button).
			Do(ctx)
	})
}

// ScrollWheelAction dispatches a wheel event at (x, y).
func ScrollWheelAction(x, y, deltaX, deltaY float64) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return input.DispatchMouseEvent(input.MouseWheel, x, y).
			WithDeltaX(deltaX).
			WithDeltaY(deltaY).
			Do(ctx)
	})
}

// KeyAction sends a single key event (keyDown + keyUp).
func KeyAction(key string) chromedp.Action {
	return chromedp.KeyEvent(key)
}

// KeyDownAction presses a key down (no release).
func KeyDownAction(key string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return input.DispatchKeyEvent(input.KeyDown).
			WithKey(key).
			WithCode(MapKeyToCode(key)).
			Do(ctx)
	})
}

// KeyUpAction releases a key (no press).
func KeyUpAction(key string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return input.DispatchKeyEvent(input.KeyUp).
			WithKey(key).
			WithCode(MapKeyToCode(key)).
			Do(ctx)
	})
}

// KeyCodeMap provides a rough DOM code mapping for common keys.
var KeyCodeMap = map[string]string{
	"Enter":     "Enter",
	"Tab":       "Tab",
	"Backspace": "Backspace",
	"Delete":    "Delete",
	"Escape":    "Escape",
	"ArrowUp":   "ArrowUp",
	"Up":        "ArrowUp",
	"ArrowDown": "ArrowDown",
	"Down":      "ArrowDown",
	"ArrowLeft": "ArrowLeft",
	"Left":      "ArrowLeft",
	"ArrowRight": "ArrowRight",
	"Right":     "ArrowRight",
	"Home":      "Home",
	"End":       "End",
	"PageUp":    "PageUp",
	"PageDown":  "PageDown",
}

// MapKeyToCode maps a key name to its DOM code.
func MapKeyToCode(key string) string {
	return KeyCodeMap[key]
}

// TextInputAction types text via insertText (handles all Unicode including CJK).
func TextInputAction(text string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return input.InsertText(text).Do(ctx)
	})
}

// GetElementCenterFromNode computes the center point of a DOM node using its box model.
// The node must have a valid NodeID (obtained from a previous navigation or query).
func GetElementCenterFromNode(ctx context.Context, node *cdp.Node) (float64, float64, error) {
	if node == nil {
		return 0, 0, fmt.Errorf("node is nil")
	}
	model, err := dom.GetBoxModel().WithNodeID(node.NodeID).Do(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("get box model for node %d: %w", node.NodeID, err)
	}
	if len(model.Content) < 8 {
		return 0, 0, fmt.Errorf("node %d has incomplete content quad (len=%d)", node.NodeID, len(model.Content))
	}
	// Content is a Quad of 8 floats [x1,y1,x2,y2,x3,y3,x4,y4] (clockwise from top-left).
	// Center ≈ midpoint of diagonal corners (x1,y1) and (x3,y3).
	quad := model.Content
	cx := (quad[0] + quad[4]) / 2
	cy := (quad[1] + quad[5]) / 2
	return cx, cy, nil
}

// ─── Handler functions ───────────────────────

// HandleWebStart starts the browser.
func HandleWebStart(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.allocCtx != nil {
		return &types.ToolResult{Success: false, Error: "browser already running, call web_close first", Tool: "web_start"}
	}

	headless := true
	if v, ok := args["headless"].(bool); ok {
		headless = v
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", headless),
		chromedp.Flag("disable-gpu", false),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("mute-audio", true),
	)
	if !headless {
		opts = append(opts,
			chromedp.Flag("window-size", "1280,800"),
		)
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	bm.allocCtx = allocCtx
	bm.allocCancel = allocCancel
	bm.ready = false
	bm.consoleLogs = nil
	bm.requestLogs = nil

	return &types.ToolResult{Success: true, Output: fmt.Sprintf("browser started (headless: %v)", headless), Tool: "web_start"}
}

// HandleWebOpen navigates to a URL.
func HandleWebOpen(bm *BrowserManager, tm *task.TaskManager, logger *zap.Logger, args map[string]interface{}) *types.ToolResult {
	url, _ := args["url"].(string)
	if url == "" {
		return &types.ToolResult{Success: false, Error: "url is required", Tool: "web_open"}
	}

	_, output, err := tm.SyncOperation("web_open", func(cancel <-chan struct{}) (string, error) {
		ctx, err := bm.EnsureTab()
		if err != nil {
			return "", err
		}

		domains := []chromedp.Action{
			runtime.Enable(),
			network.Enable(),
		}
		if err := chromedp.Run(ctx, domains...); err != nil {
			return "", fmt.Errorf("enable domains failed: %s", err.Error())
		}

		if err := chromedp.Run(ctx, chromedp.Navigate(url)); err != nil {
			return "", fmt.Errorf("navigation failed: %s", err.Error())
		}

		if err := chromedp.Run(ctx, chromedp.WaitReady("body")); err != nil {
			logger.Warn("wait ready after navigation failed", zap.String("url", url), zap.Error(err))
		}

		var title string
		_ = chromedp.Run(ctx, chromedp.Title(&title))
		return fmt.Sprintf("navigated to %s, title: %s", url, title), nil
	})

	return &types.ToolResult{
		Success: err == nil,
		Output:  output,
		Error:   FormatErr(err),
		Tool:    "web_open",
	}
}

// HandleWebScreenshot takes a screenshot of the current page.
func HandleWebScreenshot(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	ctx, err := bm.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_screenshot"}
	}

	fullPage := false
	if v, ok := args["full_page"].(bool); ok {
		fullPage = v
	}

	maxHeight := 10000
	if v, ok := args["max_height"].(float64); ok && v > 0 {
		maxHeight = int(v)
	}

	var buf []byte
	if fullPage {
		_ = chromedp.Run(ctx,
			chromedp.ActionFunc(func(ctx context.Context) error {
				_, _, err := runtime.Evaluate(fmt.Sprintf(
					`document.documentElement.style.minHeight = Math.min(document.documentElement.scrollHeight, %d) + 'px'`, maxHeight,
				)).Do(ctx)
				return err
			}),
		)
		if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			buf, err = page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatPng).
				WithCaptureBeyondViewport(true).
				Do(ctx)
			return err
		})); err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("full page screenshot failed: %s", err.Error()), Tool: "web_screenshot"}
		}
	} else {
		if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			buf, err = page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatPng).
				Do(ctx)
			return err
		})); err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("screenshot failed: %s", err.Error()), Tool: "web_screenshot"}
		}
	}

	encoded := base64.StdEncoding.EncodeToString(buf)
	dataURL := "data:image/png;base64," + encoded
	return &types.ToolResult{
		Success: true,
		Output:  dataURL,
		Tool:    "web_screenshot",
		RawResult: map[string]interface{}{
			"mime_type": "image/png",
			"encoding":  "base64",
			"size":      len(buf),
		},
	}
}

// HandleWebClose closes the browser.
func HandleWebClose(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.allocCtx == nil {
		return &types.ToolResult{Success: true, Output: "browser not running", Tool: "web_close"}
	}

	if bm.tabCancel != nil {
		bm.tabCancel()
		bm.tabCancel = nil
	}
	bm.tabCtx = nil
	bm.ready = false

	if bm.allocCancel != nil {
		bm.allocCancel()
		bm.allocCancel = nil
	}
	bm.allocCtx = nil
	bm.consoleLogs = nil
	bm.requestLogs = nil

	return &types.ToolResult{Success: true, Output: "browser closed", Tool: "web_close"}
}

// ─── Simplify Functions ───

func init() {
	types.RegisterSimplify("web_start", simplifyWebStart)
	types.RegisterSimplify("web_open", simplifyWebOpen)
	types.RegisterSimplify("web_click", simplifyWebClick)
	types.RegisterSimplify("web_type", simplifyWebType)
	types.RegisterSimplify("web_hover", simplifyWebHover)
	types.RegisterSimplify("web_drag", simplifyWebDrag)
	types.RegisterSimplify("web_mouse_down", types.SimpleAction("web_mouse_down"))
	types.RegisterSimplify("web_mouse_up", types.SimpleAction("web_mouse_up"))
	types.RegisterSimplify("web_mouse_move", types.SimpleAction("web_mouse_move"))
	types.RegisterSimplify("web_scroll_wheel", types.SimpleAction("web_scroll_wheel"))
	types.RegisterSimplify("web_scroll_to", simplifyWebScrollTo)
	types.RegisterSimplify("web_screenshot", types.SimpleAction("web_screenshot"))
	types.RegisterSimplify("web_evaluate", simplifyWebEvaluate)
	types.RegisterSimplify("web_get_text", simplifyWebGetText)
	types.RegisterSimplify("web_get_html", types.SimpleAction("web_get_html"))
	types.RegisterSimplify("web_get_style", types.Simplifynothing)
	types.RegisterSimplify("web_get_url", types.SimpleAction("web_get_url"))
	types.RegisterSimplify("web_get_title", types.SimpleAction("web_get_title"))
	types.RegisterSimplify("web_get_console", simplifyWebGetConsole)
	types.RegisterSimplify("web_get_requests", types.SimpleAction("web_get_requests"))
	types.RegisterSimplify("web_wait_selector", simplifyWebWaitSelector)
	types.RegisterSimplify("web_wait_navigation", types.SimpleAction("web_wait_navigation"))
	types.RegisterSimplify("web_set_viewport", simplifyWebSetViewport)
	types.RegisterSimplify("web_close", types.SimpleAction("web_close"))
	types.RegisterSimplify("web_set_geolocation", simplifyWebSetGeolocation)
	types.RegisterSimplify("web_grant_permission", simplifyWebGrantPermission)
}

type urlArg struct {
	URL string `json:"url"`
}

func simplifyWebStart(argsJSON json.RawMessage, result string) string {
	var a urlArg
	if err := json.Unmarshal(argsJSON, &a); err != nil || a.URL == "" {
		return "web_start"
	}
	return fmt.Sprintf("web_start(%s)", a.URL)
}

func simplifyWebOpen(argsJSON json.RawMessage, result string) string {
	var a urlArg
	if err := json.Unmarshal(argsJSON, &a); err != nil || a.URL == "" {
		return "web_open"
	}
	return fmt.Sprintf("web_open(%s)", a.URL)
}

type selectorArg struct {
	Selector string `json:"selector"`
}

func simplifyWebClick(argsJSON json.RawMessage, result string) string {
	var a selectorArg
	if err := json.Unmarshal(argsJSON, &a); err != nil || a.Selector == "" {
		return "web_click"
	}
	return fmt.Sprintf("web_click(%s)", a.Selector)
}

type typeArgs struct {
	Selector string `json:"selector"`
	Text     string `json:"text"`
}

func simplifyWebType(argsJSON json.RawMessage, result string) string {
	var a typeArgs
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return "web_type"
	}
	if a.Selector != "" {
		return fmt.Sprintf("web_type(%s): %d chars", a.Selector, len(a.Text))
	}
	return fmt.Sprintf("web_type: %d chars", len(a.Text))
}

func simplifyWebHover(argsJSON json.RawMessage, result string) string {
	var a selectorArg
	if err := json.Unmarshal(argsJSON, &a); err != nil || a.Selector == "" {
		return "web_hover"
	}
	return fmt.Sprintf("web_hover(%s)", a.Selector)
}

func simplifyWebDrag(argsJSON json.RawMessage, result string) string {
	return "web_drag"
}

type scrollToArgs struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func simplifyWebScrollTo(argsJSON json.RawMessage, result string) string {
	var a scrollToArgs
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return "web_scroll_to"
	}
	return fmt.Sprintf("web_scroll_to(x=%.0f, y=%.0f)", a.X, a.Y)
}

type evaluateArgs struct {
	JS string `json:"js"`
}

func simplifyWebEvaluate(argsJSON json.RawMessage, result string) string {
	var a evaluateArgs
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return "web_evaluate"
	}
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil || !tr.Success {
		return "web_evaluate: failed"
	}
	return fmt.Sprintf("web_evaluate: evaluated JS expression (%d chars in result)", len(tr.Output))
}

func simplifyWebGetText(argsJSON json.RawMessage, result string) string {
	var a selectorArg
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return "web_get_text"
	}
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil || !tr.Success {
		return "web_get_text: failed"
	}
	if a.Selector != "" {
		return fmt.Sprintf("web_get_text(%s): %d chars", a.Selector, len(tr.Output))
	}
	return fmt.Sprintf("web_get_text: %d chars", len(tr.Output))
}

func simplifyWebGetConsole(argsJSON json.RawMessage, result string) string {
	var tr types.ToolResult
	if err := json.Unmarshal([]byte(result), &tr); err != nil || !tr.Success {
		return "web_get_console"
	}
	lines := strings.Count(tr.Output, "\n")
	return fmt.Sprintf("web_get_console: %d entries", lines)
}

type waitSelectorArgs struct {
	Selector string  `json:"selector"`
	Timeout  float64 `json:"timeout,omitempty"`
}

func simplifyWebWaitSelector(argsJSON json.RawMessage, result string) string {
	var a waitSelectorArgs
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return "web_wait_selector"
	}
	return fmt.Sprintf("web_wait_selector(%s)", a.Selector)
}

type viewportArgs struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func simplifyWebSetViewport(argsJSON json.RawMessage, result string) string {
	var a viewportArgs
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return "web_set_viewport"
	}
	return fmt.Sprintf("web_set_viewport(%.0fx%.0f)", a.Width, a.Height)
}

type geoArgs struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

func simplifyWebSetGeolocation(argsJSON json.RawMessage, result string) string {
	var a geoArgs
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return "web_set_geolocation"
	}
	return fmt.Sprintf("web_set_geolocation(%.4f, %.4f)", a.Latitude, a.Longitude)
}

func simplifyWebGrantPermission(argsJSON json.RawMessage, result string) string {
	return "web_grant_permission"
}
