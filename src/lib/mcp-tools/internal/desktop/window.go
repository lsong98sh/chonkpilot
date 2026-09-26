package desktop

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

// ResolveWindow finds a window by hwnd or title.
func ResolveWindow(hwndVal float64, title string) (syscall.Handle, error) {
	if hwndVal > 0 {
		return syscall.Handle(uintptr(int32(hwndVal))), nil
	}
	if title != "" {
		return FindWindowByTitle(title)
	}
	return 0, fmt.Errorf("either hwnd or window title required")
}

// FindWindowByTitle finds a window by its title.
func FindWindowByTitle(title string) (syscall.Handle, error) {
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	ret, _, _ := FindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
	if ret == 0 {
		return EnumWindowsFind(func(hwnd syscall.Handle) bool {
			return WindowTitleMatches(hwnd, title)
		})
	}
	return syscall.Handle(ret), nil
}

// FindWindowByClass finds a top-level window by its class name.
func FindWindowByClass(class string) (syscall.Handle, error) {
	classPtr, _ := syscall.UTF16PtrFromString(class)
	ret, _, _ := FindWindowW.Call(uintptr(unsafe.Pointer(classPtr)), 0)
	if ret == 0 {
		return EnumWindowsFind(func(hwnd syscall.Handle) bool {
			classBuf := make([]uint16, 256)
			GetClassNameW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&classBuf[0])), 256)
			return syscall.UTF16ToString(classBuf) == class
		})
	}
	return syscall.Handle(ret), nil
}

// ClientScreenRect 返回窗口客户区在屏幕坐标系下的 rect（GetClientRect + ClientToScreen）。
func ClientScreenRect(hwnd syscall.Handle) Rect {
	var cr Rect
	GetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	var pt struct{ X, Y int32 }
	ClientToScreen.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pt)))
	return Rect{Left: pt.X, Top: pt.Y, Right: pt.X + cr.Right, Bottom: pt.Y + cr.Bottom}
}

// WindowTitleMatches checks if window title contains substr.
func WindowTitleMatches(hwnd syscall.Handle, substr string) bool {
	length, _, _ := GetWindowTextLengthW.Call(uintptr(hwnd))
	if length == 0 {
		return false
	}
	buf := make([]uint16, length+1)
	GetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(length+1))
	return strings.Contains(syscall.UTF16ToString(buf), substr)
}

// GetWindowTitle returns the title of a window.
func GetWindowTitle(hwnd syscall.Handle) string {
	length, _, _ := GetWindowTextLengthW.Call(uintptr(hwnd))
	if length == 0 {
		return ""
	}
	buf := make([]uint16, length+1)
	GetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(length+1))
	return syscall.UTF16ToString(buf)
}

// EnumWindowsFind finds a window by matching function.
func EnumWindowsFind(match func(syscall.Handle) bool) (syscall.Handle, error) {
	var found syscall.Handle
	cb := syscall.NewCallback(func(hwnd syscall.Handle, lparam uintptr) uintptr {
		if match(hwnd) {
			found = hwnd
			return 0
		}
		return 1
	})
	EnumWindows.Call(cb, 0)
	if found == 0 {
		return 0, fmt.Errorf("no matching window found")
	}
	return found, nil
}

// EnumWindowsList returns a list of all top-level window handles.
func EnumWindowsList() []syscall.Handle {
	var result []syscall.Handle
	cb := syscall.NewCallback(func(hwnd syscall.Handle, lparam uintptr) uintptr {
		result = append(result, hwnd)
		return 1
	})
	EnumWindows.Call(cb, 0)
	return result
}

// WindowInfo holds information about a window.
type WindowInfo struct {
	Title   string  `json:"title"`
	Class   string  `json:"class"`
	Hwnd    uintptr `json:"hwnd,omitempty"`
	Left    int32   `json:"left"`
	Top     int32   `json:"top"`
	Right   int32   `json:"right"`
	Bottom  int32   `json:"bottom"`
	Width   int32   `json:"width"`
	Height  int32   `json:"height"`
	Visible bool    `json:"visible"`
}

// GetWindowInfo gets information about a window.
func GetWindowInfo(hwnd syscall.Handle) *WindowInfo {
	title := GetWindowTitle(hwnd)
	var r Rect
	GetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&r)))

	classBuf := make([]uint16, 256)
	GetClassNameW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&classBuf[0])), 256)
	className := syscall.UTF16ToString(classBuf)

	isVisible := uint32(0)
	proc := User32.NewProc("IsWindowVisible")
	ret, _, _ := proc.Call(uintptr(hwnd))
	isVisible = uint32(ret)

	return &WindowInfo{
		Title:   title,
		Class:   className,
		Hwnd:    uintptr(hwnd),
		Left:    r.Left,
		Top:     r.Top,
		Right:   r.Right,
		Bottom:  r.Bottom,
		Width:   r.Right - r.Left,
		Height:  r.Bottom - r.Top,
		Visible: isVisible != 0,
	}
}

// ForceForegroundWindow brings the target window to the foreground.
func ForceForegroundWindow(hwnd uintptr) {
	ShowWindow.Call(hwnd, 9)

	foreHwnd, _, _ := GetForegroundWindow.Call()
	if foreHwnd == hwnd {
		return
	}

	foreThread, _, _ := GetWindowThreadProcID.Call(foreHwnd, 0)
	targetThread, _, _ := GetWindowThreadProcID.Call(hwnd, 0)

	if foreThread != targetThread {
		AttachThreadInput.Call(foreThread, targetThread, 1)
		SetForegroundWindow.Call(hwnd)
		AttachThreadInput.Call(foreThread, targetThread, 0)
	} else {
		SetForegroundWindow.Call(hwnd)
	}
}

// HandleFindWindow finds a window by hwnd, title, or returns foreground window.
func HandleFindWindow(args map[string]interface{}) *ToolResult {
	hwndVal, hasHWND := args["hwnd"].(float64)
	title, _ := args["title"].(string)

	var hwnd syscall.Handle
	var err error
	if hasHWND && hwndVal > 0 {
		hwnd = syscall.Handle(uintptr(int32(hwndVal)))
	} else if title != "" {
		hwnd, err = FindWindowByTitle(title)
		if err != nil {
			return &ToolResult{Success: false, Error: fmt.Sprintf("window not found: %s", err.Error()), Output: fmt.Sprintf("❌ 查找窗口失败：%s", err.Error()), Tool: "window_find"}
		}
	} else {
		ret, _, _ := GetForegroundWindow.Call()
		hwnd = syscall.Handle(ret)
	}

	info := GetWindowInfo(hwnd)
	return &ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("🔍 已找到窗口：%s", info.Title),
		Tool:      "window_find",
		RawResult: map[string]interface{}{"title": info.Title, "hwnd": info.Hwnd, "class": info.Class, "rect": map[string]interface{}{"x": info.Left, "y": info.Top, "width": info.Width, "height": info.Height}},
	}
}

// HandleListWindows lists all visible windows.
func HandleListWindows(args map[string]interface{}) *ToolResult {
	hwnds := EnumWindowsList()
	var infos []*WindowInfo
	for _, hwnd := range hwnds {
		info := GetWindowInfo(hwnd)
		if info.Title != "" {
			infos = append(infos, info)
		}
	}
	if len(infos) > 50 {
		infos = infos[:50]
	}

	type windowItem struct {
		Title   string  `json:"title"`
		Hwnd    uintptr `json:"hwnd"`
		Width   int32   `json:"width"`
		Height  int32   `json:"height"`
		X       int32   `json:"x"`
		Y       int32   `json:"y"`
		Visible bool    `json:"visible"`
	}
	winList := make([]windowItem, 0, len(infos))
	for _, info := range infos {
		winList = append(winList, windowItem{Title: info.Title, Hwnd: info.Hwnd, Width: info.Width, Height: info.Height, X: info.Left, Y: info.Top, Visible: info.Visible})
	}

	return &ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("📋 列出 %d 个窗口", len(infos)),
		Tool:      "windows_list",
		RawResult: map[string]interface{}{"windows": winList},
	}
}

// HandleGetWindowRect gets the rectangle of a window.
func HandleGetWindowRect(args map[string]interface{}) *ToolResult {
	hwndVal, _ := args["hwnd"].(float64)
	title, _ := args["window"].(string)

	hwnd, err := ResolveWindow(hwndVal, title)
	if err != nil {
		return &ToolResult{Success: false, Error: err.Error(), Output: fmt.Sprintf("❌ 获取窗口位置失败：%s", err.Error()), Tool: "window_rect_get"}
	}

	info := GetWindowInfo(hwnd)
	return &ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("📐 %s：%dx%d+%d+%d", info.Title, info.Width, info.Height, info.Left, info.Top),
		Tool:      "window_rect_get",
		RawResult: map[string]interface{}{"title": info.Title, "hwnd": info.Hwnd, "x": info.Left, "y": info.Top, "width": info.Width, "height": info.Height},
	}
}

// HandleSetWindowRect sets the position and size of a window.
func HandleSetWindowRect(args map[string]interface{}) *ToolResult {
	hwndVal, _ := args["hwnd"].(float64)
	title, _ := args["window"].(string)

	hwnd, err := ResolveWindow(hwndVal, title)
	if err != nil {
		return &ToolResult{Success: false, Error: err.Error(), Output: fmt.Sprintf("❌ 设置窗口位置失败：%s", err.Error()), Tool: "window_rect_set"}
	}

	x, ok1 := args["x"].(float64)
	y, ok2 := args["y"].(float64)
	w, ok3 := args["width"].(float64)
	hgt, ok4 := args["height"].(float64)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return &ToolResult{Success: false, Error: "x, y, width, height required", Output: "❌ 设置窗口位置失败：缺少坐标或尺寸参数", Tool: "window_rect_set"}
	}

	SetWindowPos.Call(uintptr(hwnd), 0, uintptr(int32(x)), uintptr(int32(y)),
		uintptr(int32(w)), uintptr(int32(hgt)), SwpNoZOrder)
	titleStr := GetWindowTitle(hwnd)
	return &ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("📐 窗口位置已设置：%dx%d+%d+%d", int32(w), int32(hgt), int32(x), int32(y)),
		Tool:      "window_rect_set",
		RawResult: map[string]interface{}{"title": titleStr, "hwnd": uintptr(hwnd), "x": int32(x), "y": int32(y), "width": int32(w), "height": int32(hgt)},
	}
}

// HandleFocusWindow focuses a window.
func HandleFocusWindow(args map[string]interface{}) *ToolResult {
	hwndVal, _ := args["hwnd"].(float64)
	title, _ := args["window"].(string)

	hwnd, err := ResolveWindow(hwndVal, title)
	if err != nil {
		return &ToolResult{Success: false, Error: err.Error(), Output: fmt.Sprintf("❌ 窗口聚焦失败：%s", err.Error()), Tool: "window_focus"}
	}

	SetForegroundWindow.Call(uintptr(hwnd))
	titleStr := GetWindowTitle(hwnd)
	return &ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("🎯 窗口已聚焦：%s", titleStr),
		Tool:      "window_focus",
		RawResult: map[string]interface{}{"title": titleStr, "hwnd": uintptr(hwnd)},
	}
}

// HandleMinimizeWindow minimizes a window.
func HandleMinimizeWindow(args map[string]interface{}) *ToolResult {
	hwndVal, _ := args["hwnd"].(float64)
	title, _ := args["window"].(string)

	hwnd, err := ResolveWindow(hwndVal, title)
	if err != nil {
		return &ToolResult{Success: false, Error: err.Error(), Output: fmt.Sprintf("❌ 窗口最小化失败：%s", err.Error()), Tool: "window_minimize"}
	}

	ShowWindow.Call(uintptr(hwnd), SwMinimize)
	titleStr := GetWindowTitle(hwnd)
	return &ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("🗕️ 窗口已最小化：%s", titleStr),
		Tool:      "window_minimize",
		RawResult: map[string]interface{}{"title": titleStr, "hwnd": uintptr(hwnd)},
	}
}

// HandleMaximizeWindow maximizes a window.
func HandleMaximizeWindow(args map[string]interface{}) *ToolResult {
	hwndVal, _ := args["hwnd"].(float64)
	title, _ := args["window"].(string)

	hwnd, err := ResolveWindow(hwndVal, title)
	if err != nil {
		return &ToolResult{Success: false, Error: err.Error(), Output: fmt.Sprintf("❌ 窗口最大化失败：%s", err.Error()), Tool: "window_maximize"}
	}

	ShowWindow.Call(uintptr(hwnd), SwMaximize)
	titleStr := GetWindowTitle(hwnd)
	return &ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("🗖️ 窗口已最大化：%s", titleStr),
		Tool:      "window_maximize",
		RawResult: map[string]interface{}{"title": titleStr, "hwnd": uintptr(hwnd)},
	}
}

// HandleRestoreWindow restores a window.
func HandleRestoreWindow(args map[string]interface{}) *ToolResult {
	hwndVal, _ := args["hwnd"].(float64)
	title, _ := args["window"].(string)

	hwnd, err := ResolveWindow(hwndVal, title)
	if err != nil {
		return &ToolResult{Success: false, Error: err.Error(), Output: fmt.Sprintf("❌ 窗口恢复失败：%s", err.Error()), Tool: "window_restore"}
	}

	ShowWindow.Call(uintptr(hwnd), SwRestore)
	titleStr := GetWindowTitle(hwnd)
	return &ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("🗗️ 窗口已恢复：%s", titleStr),
		Tool:      "window_restore",
		RawResult: map[string]interface{}{"title": titleStr, "hwnd": uintptr(hwnd)},
	}
}
