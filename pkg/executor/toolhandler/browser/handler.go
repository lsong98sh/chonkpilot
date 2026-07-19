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

// BrowserInstance holds the state for a single browser instance.
type BrowserInstance struct {
	mu          sync.Mutex
	allocCtx    context.Context
	allocCancel context.CancelFunc
	tabCtx      context.Context
	tabCancel   context.CancelFunc
	consoleLogs []string
	requestLogs []RequestRecord
	ready       bool
	WindowWidth  int // browser window width (default 1280)
	WindowHeight int // browser window height (default 800)
	LogCap       int // console log entry cap (default 500)
}

// BrowserManager manages multiple browser instances keyed by ID.
type BrowserManager struct {
	mu           sync.Mutex
	instances    map[string]*BrowserInstance
	lastID       string // tracks the most recently used instance ID
	nextID       int    // for generating unique IDs
	WindowWidth  int    // default window width for new instances
	WindowHeight int    // default window height for new instances
	LogCap       int    // default log cap for new instances
}

// RequestRecord stores a single network request.
type RequestRecord struct {
	URL       string `json:"url"`
	Method    string `json:"method"`
	Status    int    `json:"status,omitempty"`
	Type      string `json:"type,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// Package-level cancel context — set by Handler.PropagateConfig for long-running web ops.
var globalCancelCtx context.Context

// SetCancelCtx sets the cancellation context for the browser package.
func SetCancelCtx(ctx context.Context) {
	globalCancelCtx = ctx
}

// cancelCtxFor creates a context that is cancelled when either the parent or the global cancel fires.
func cancelCtxFor(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	if globalCancelCtx != nil {
		go func() {
			select {
			case <-globalCancelCtx.Done():
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	return ctx, cancel
}

// NewBrowserManager creates a new BrowserManager with sensible defaults.
func NewBrowserManager() *BrowserManager {
	return &BrowserManager{
		instances:    make(map[string]*BrowserInstance),
		WindowWidth:  1280,
		WindowHeight: 800,
		LogCap:       500,
	}
}

// browserIDFromArgs extracts optional browser_id from args.
func browserIDFromArgs(args map[string]interface{}) string {
	id, _ := args["browser_id"].(string)
	return id
}

// getInstance returns a BrowserInstance for the given browser_id.
// If browser_id is empty:
//   - If only one instance exists, returns it
//   - If zero instances, returns an error
//   - If multiple instances, returns an error asking for browser_id
func (bm *BrowserManager) getInstance(browserID string) (*BrowserInstance, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	id := browserID
	if id == "" {
		if bm.lastID != "" {
			id = bm.lastID
		} else if len(bm.instances) == 1 {
			for k := range bm.instances {
				id = k
			}
		} else if len(bm.instances) == 0 {
			return nil, fmt.Errorf("browser instance not found (call web_start first)")
		} else {
			return nil, fmt.Errorf("browser_id is required (multiple instances running)")
		}
	}

	inst, ok := bm.instances[id]
	if !ok {
		return nil, fmt.Errorf("browser instance '%s' not found", id)
	}
	return inst, nil
}

// generateID creates a unique instance ID.
func (bm *BrowserManager) generateID() string {
	bm.nextID++
	return fmt.Sprintf("b%d", bm.nextID)
}

// EnsureTab returns a tab context for the given instance; creates one lazily.
// If the existing tab context has been cancelled (e.g. due to CDP connection issues
// between tool calls), it recreates the tab automatically.
func (inst *BrowserInstance) EnsureTab() (context.Context, error) {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.allocCtx == nil {
		return nil, fmt.Errorf("browser not started")
	}

	// Check if the alloc context itself is cancelled — if so the browser is gone.
	select {
	case <-inst.allocCtx.Done():
		return nil, fmt.Errorf("browser allocator context cancelled: %w", inst.allocCtx.Err())
	default:
	}

	// If tab exists but its context is cancelled, reset so we recreate it below.
	if inst.ready && inst.tabCtx != nil {
		select {
		case <-inst.tabCtx.Done():
			// Tab context was cancelled between tool calls; clean up and recreate.
			inst.tabCancel()
			inst.ready = false
		default:
			// Tab is still healthy, return as-is.
			return inst.tabCtx, nil
		}
	}

	if !inst.ready {
		ctx, cancel := chromedp.NewContext(inst.allocCtx)
		inst.tabCtx = ctx
		inst.tabCancel = cancel
		inst.ready = true

		// ── Warm up: trigger browser allocation with long-lived tabCtx ──
		// Without this, the first chromedp.Run(runCtx, ...) in a handler
		// will call Allocate(runCtx, ...), which internally calls:
		//   exec.CommandContext(runCtx, chromePath, args...)
		//   NewBrowser(runCtx, wsURL, ...)
		//         └─ go b.run(runCtx)
		// Both the Chrome process and the browser event loop are then tied
		// to runCtx. When the handler returns and cancels runCtx, Chrome
		// gets killed by exec.CommandContext's kill-on-cancel behaviour.
		//
		// By doing a warm-up Run with the persistent tabCtx, we ensure
		// Allocate uses the long-lived tabCtx, and subsequent handler
		// Run calls (with short-lived runCtx) skip Allocate entirely
		// because c.Browser is already set.
		if err := chromedp.Run(ctx); err != nil {
			// If warmup fails, don't block — the first real Run will fail too
			// and report the error properly.
		}

		// Start a keepalive goroutine that periodically pings the browser
		// via chromedp.Run (not raw CDP) to keep the DevTools connection alive.
		// This prevents Chrome from closing when there's a gap between tool calls.
		go func() {
			ticker := time.NewTicker(8 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					pingCtx, pingCancel := context.WithTimeout(ctx, 4*time.Second)
					// Execute a lightweight real CDP command to keep the
					// DevTools WebSocket connection alive. A no-op ActionFunc
					// does NOT generate any CDP traffic, so Chrome's idle
					// timer expires and closes the browser window.
					var v interface{}
					_ = chromedp.Run(pingCtx, chromedp.Evaluate("1+1", &v))
					pingCancel()
				}
			}
		}()

		chromedp.ListenTarget(ctx, func(ev interface{}) {
			switch e := ev.(type) {
			case *runtime.EventConsoleAPICalled:
				var parts []string
				for _, arg := range e.Args {
					val := string(arg.Value)
					parts = append(parts, val)
				}
				line := fmt.Sprintf("[%s] %s", e.Type.String(), strings.Join(parts, " "))
				inst.mu.Lock()
				inst.consoleLogs = append(inst.consoleLogs, line)
				if len(inst.consoleLogs) > inst.LogCap {
					n := len(inst.consoleLogs) - inst.LogCap
					newLogs := make([]string, inst.LogCap)
					copy(newLogs, inst.consoleLogs[n:])
					inst.consoleLogs = newLogs
				}
				inst.mu.Unlock()

			case *network.EventRequestWillBeSent:
				rec := RequestRecord{
					URL:       e.Request.URL,
					Method:    e.Request.Method,
					Type:      string(e.Type),
					Timestamp: time.Now().UnixMilli(),
				}
				inst.mu.Lock()
				inst.requestLogs = append(inst.requestLogs, rec)
				if len(inst.requestLogs) > inst.LogCap {
					inst.requestLogs = inst.requestLogs[len(inst.requestLogs)-inst.LogCap:]
				}
				inst.mu.Unlock()

			case *network.EventResponseReceived:
				url := e.Response.URL
				statusCode := e.Response.Status
				inst.mu.Lock()
				for i := len(inst.requestLogs) - 1; i >= 0; i-- {
					if inst.requestLogs[i].URL == url && inst.requestLogs[i].Status == 0 {
						inst.requestLogs[i].Status = int(statusCode)
						break
					}
				}
				inst.mu.Unlock()
			}
		})
	}
	return inst.tabCtx, nil
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
	"Enter":      "Enter",
	"Tab":        "Tab",
	"Backspace":  "Backspace",
	"Delete":     "Delete",
	"Escape":     "Escape",
	"ArrowUp":    "ArrowUp",
	"Up":         "ArrowUp",
	"ArrowDown":  "ArrowDown",
	"Down":       "ArrowDown",
	"ArrowLeft":  "ArrowLeft",
	"Left":       "ArrowLeft",
	"ArrowRight": "ArrowRight",
	"Right":      "ArrowRight",
	"Home":       "Home",
	"End":        "End",
	"PageUp":     "PageUp",
	"PageDown":   "PageDown",
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

// HandleWebStart starts a new browser instance.
func HandleWebStart(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
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
		chromedp.Flag("keep-alive-for-test", true),
		chromedp.Flag("autoplay-policy", "no-user-gesture-required"),
		chromedp.Flag("disable-session-crashed-bubble", true),
		chromedp.Flag("no-crash-upload", true),
		chromedp.Flag("disable-features", "ChromeWhatsNewUI,TranslateUI,ChromeCleanup,MediaRouter"),
	)
	if !headless {
		opts = append(opts,
			chromedp.Flag("window-size", fmt.Sprintf("%d,%d", bm.WindowWidth, bm.WindowHeight)),
		)
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)

	inst := &BrowserInstance{
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
		ready:       false,
		WindowWidth:  bm.WindowWidth,
		WindowHeight: bm.WindowHeight,
		LogCap:       bm.LogCap,
	}

	bm.mu.Lock()
	id := bm.generateID()
	bm.instances[id] = inst
	bm.lastID = id
	bm.mu.Unlock()

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("browser started (headless: %v), browser_id: %s", headless, id),
		Tool:    "web_start",
		RawResult: map[string]interface{}{
			"browser_id": id,
		},
	}
}

// HandleWebOpen navigates to a URL.
func HandleWebOpen(bm *BrowserManager, tm *task.TaskManager, logger *zap.Logger, args map[string]interface{}) *types.ToolResult {
	url, _ := args["url"].(string)
	if url == "" {
		return &types.ToolResult{Success: false, Error: "url is required", Tool: "web_open"}
	}

	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_open"}
	}

	_, output, err := tm.SyncOperation("web_open", func(cancel <-chan struct{}) (string, error) {
		ctx, err := inst.EnsureTab()
		if err != nil {
			return "", err
		}

		// Wire cancel channel into chromedp context with timeout
		runCtx, runCancel := context.WithTimeout(ctx, 30*time.Second)
		defer runCancel()
		go func() {
			select {
			case <-cancel:
				runCancel()
			case <-runCtx.Done():
			}
		}()

		domains := []chromedp.Action{
			runtime.Enable(),
			network.Enable(),
		}
		if err := chromedp.Run(runCtx, domains...); err != nil {
			return "", fmt.Errorf("enable domains failed: %s", err.Error())
		}

		if err := chromedp.Run(runCtx, chromedp.Navigate(url)); err != nil {
			return "", fmt.Errorf("navigation failed: %s", err.Error())
		}

		if err := chromedp.Run(runCtx, chromedp.WaitReady("body")); err != nil {
			logger.Warn("wait ready after navigation failed", zap.String("url", url), zap.Error(err))
		}

		var title string
		_ = chromedp.Run(runCtx, chromedp.Title(&title))
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
	inst, err := bm.getInstance(browserIDFromArgs(args))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_screenshot"}
	}

	ctx, err := inst.EnsureTab()
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "web_screenshot"}
	}

	// Wrap with global cancel context so CancelChat can abort long screenshots
	screenshotCtx, screenshotCancel := cancelCtxFor(ctx)
	defer screenshotCancel()

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
		_ = chromedp.Run(screenshotCtx,
			chromedp.ActionFunc(func(ctx context.Context) error {
				_, _, err := runtime.Evaluate(fmt.Sprintf(
					`document.documentElement.style.minHeight = Math.min(document.documentElement.scrollHeight, %d) + 'px'`, maxHeight,
				)).Do(ctx)
				return err
			}),
		)
		if err := chromedp.Run(screenshotCtx, chromedp.ActionFunc(func(ctx context.Context) error {
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
		if err := chromedp.Run(screenshotCtx, chromedp.ActionFunc(func(ctx context.Context) error {
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

// HandleWebClose closes a browser instance.
func HandleWebClose(bm *BrowserManager, args map[string]interface{}) *types.ToolResult {
	browserID := browserIDFromArgs(args)

	bm.mu.Lock()
	var inst *BrowserInstance
	var ok bool
	id := browserID
	if id == "" {
		// Close all instances if no ID specified
		if len(bm.instances) == 0 {
			bm.mu.Unlock()
			return &types.ToolResult{Success: true, Output: "no browser instances running", Tool: "web_close"}
		}
		// Close last used, or if only one, close that
		if bm.lastID != "" {
			id = bm.lastID
		}
		if id != "" {
			inst, ok = bm.instances[id]
		}
		if !ok {
			// If lastID not set or not found, close the first one
			for k, v := range bm.instances {
				id = k
				inst = v
				break
			}
		}
	} else {
		inst, ok = bm.instances[id]
		if !ok {
			bm.mu.Unlock()
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("browser instance '%s' not found", id), Tool: "web_close"}
		}
	}

	delete(bm.instances, id)
	if bm.lastID == id {
		bm.lastID = ""
	}
	bm.mu.Unlock()

	// Cleanup outside lock
	if inst.tabCancel != nil {
		inst.tabCancel()
		inst.tabCancel = nil
	}
	inst.tabCtx = nil
	inst.ready = false

	if inst.allocCancel != nil {
		inst.allocCancel()
		inst.allocCancel = nil
	}
	inst.allocCtx = nil
	inst.consoleLogs = nil
	inst.requestLogs = nil

	return &types.ToolResult{Success: true, Output: fmt.Sprintf("browser instance '%s' closed", id), Tool: "web_close"}
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
