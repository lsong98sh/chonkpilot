package desktop

import (
	"fmt"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleMouseMove handles mouse_move tool.
func HandleMouseMove(args map[string]interface{}) *types.ToolResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	x, ok1 := args["x"].(float64)
	y, ok2 := args["y"].(float64)
	if !ok1 || !ok2 {
		return &types.ToolResult{Success: false, Error: "x and y required", Tool: "mouse_move"}
	}

	modRaw, _ := args["modifiers"].([]interface{})
	modVks := DesktopParseMods(modRaw)
	releaseMods := DesktopPressMods(modVks)
	defer releaseMods()

	fullRect := GetFullScreenRect()
	SendMouseEvent(MouseEventMove|MouseEventAbsolute,
		int32(x*65535.0/float64(fullRect.Right)),
		int32(y*65535.0/float64(fullRect.Bottom)), 0)
	return &types.ToolResult{Success: true, Output: fmt.Sprintf("mouse moved to (%.0f, %.0f)", x, y), Tool: "mouse_move"}
}

// HandleMouseDown handles mouse_down tool.
func HandleMouseDown(args map[string]interface{}) *types.ToolResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	btnStr, _ := args["button"].(string)
	modRaw, _ := args["modifiers"].([]interface{})
	modVks := DesktopParseMods(modRaw)
	releaseMods := DesktopPressMods(modVks)
	defer releaseMods()

	var flags uint32
	switch strings.ToLower(btnStr) {
	case "right":
		flags = MouseEventRightDown
	case "middle":
		flags = MouseEventMiddleDown
	default:
		flags = MouseEventLeftDown
	}
	SendMouseEvent(flags, 0, 0, 0)
	return &types.ToolResult{Success: true, Output: fmt.Sprintf("mouse %s down", btnStr), Tool: "mouse_down"}
}

// HandleMouseUp handles mouse_up tool.
func HandleMouseUp(args map[string]interface{}) *types.ToolResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	btnStr, _ := args["button"].(string)
	modRaw, _ := args["modifiers"].([]interface{})
	modVks := DesktopParseMods(modRaw)
	releaseMods := DesktopPressMods(modVks)
	defer releaseMods()

	var flags uint32
	switch strings.ToLower(btnStr) {
	case "right":
		flags = MouseEventRightUp
	case "middle":
		flags = MouseEventMiddleUp
	default:
		flags = MouseEventLeftUp
	}
	SendMouseEvent(flags, 0, 0, 0)
	return &types.ToolResult{Success: true, Output: fmt.Sprintf("mouse %s up", btnStr), Tool: "mouse_up"}
}

// HandleMouseClick handles mouse_click tool.
func HandleMouseClick(args map[string]interface{}) *types.ToolResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	x, hasX := args["x"].(float64)
	y, hasY := args["y"].(float64)
	btnStr, _ := args["button"].(string)
	doubleClick, _ := args["double"].(bool)
	modRaw, _ := args["modifiers"].([]interface{})
	modVks := DesktopParseMods(modRaw)
	releaseMods := DesktopPressMods(modVks)
	defer releaseMods()

	hwndVal, hasHWND := args["hwnd"].(float64)
	winTitle, hasTitle := args["window"].(string)

	if hasHWND || hasTitle {
		hwnd, err := ResolveWindow(hwndVal, winTitle)
		if err != nil {
			return &types.ToolResult{Success: false, Error: err.Error(), Tool: "mouse_click"}
		}
		ForceForegroundWindow(uintptr(hwnd))
		time.Sleep(time.Millisecond * 200)

		var wr Rect
		GetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wr)))

		if !hasX || !hasY {
			x = float64(wr.Left+wr.Right) / 2.0
			y = float64(wr.Top+wr.Bottom) / 2.0
			hasX, hasY = true, true
		} else {
			x = float64(wr.Left) + x
			y = float64(wr.Top) + y
		}
	}

	if hasX && hasY {
		fullRect := GetFullScreenRect()
		SendMouseEvent(MouseEventMove|MouseEventAbsolute,
			int32(x*65535.0/float64(fullRect.Right)),
			int32(y*65535.0/float64(fullRect.Bottom)), 0)
	}

	var downFlag, upFlag uint32
	switch strings.ToLower(btnStr) {
	case "right":
		downFlag, upFlag = MouseEventRightDown, MouseEventRightUp
	case "middle":
		downFlag, upFlag = MouseEventMiddleDown, MouseEventMiddleUp
	default:
		downFlag, upFlag = MouseEventLeftDown, MouseEventLeftUp
	}

	clicks := 1
	if doubleClick {
		clicks = 2
	}
	for i := 0; i < clicks; i++ {
		SendMouseEvent(downFlag, 0, 0, 0)
		SendMouseEvent(upFlag, 0, 0, 0)
	}

	posStr := ""
	if hasX && hasY {
		posStr = fmt.Sprintf(" at (%.0f, %.0f)", x, y)
	}
	clickType := "click"
	if doubleClick {
		clickType = "double-click"
	}
	return &types.ToolResult{Success: true, Output: fmt.Sprintf("mouse %s %s%s", btnStr, clickType, posStr), Tool: "mouse_click"}
}

// HandleScrollWheel handles scroll_wheel tool.
func HandleScrollWheel(args map[string]interface{}) *types.ToolResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	deltaX, _ := args["delta_x"].(float64)
	deltaY, _ := args["delta_y"].(float64)

	modRaw, _ := args["modifiers"].([]interface{})
	modVks := DesktopParseMods(modRaw)
	releaseMods := DesktopPressMods(modVks)
	defer releaseMods()

	if deltaX != 0 {
		SendMouseEvent(MouseEventHWheel, 0, 0, uint32(int32(deltaX)))
	}
	if deltaY != 0 {
		SendMouseEvent(MouseEventWheel, 0, 0, uint32(int32(deltaY)))
	}
	return &types.ToolResult{Success: true, Output: fmt.Sprintf("scrolled (%.0f, %.0f)", deltaX, deltaY), Tool: "scroll_wheel"}
}

func init() {
	types.RegisterSimplify("mouse_click", types.SimpleAction("mouse_click"))
	types.RegisterSimplify("mouse_down", types.SimpleAction("mouse_down"))
	types.RegisterSimplify("mouse_up", types.SimpleAction("mouse_up"))
	types.RegisterSimplify("mouse_move", types.SimpleAction("mouse_move"))
	types.RegisterSimplify("scroll_wheel", types.SimpleAction("scroll_wheel"))
}
