package desktop

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
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
	Title   string `json:"title"`
	Class   string `json:"class"`
	Hwnd    uintptr `json:"hwnd,omitempty"`
	Left    int32  `json:"left"`
	Top     int32  `json:"top"`
	Right   int32  `json:"right"`
	Bottom  int32  `json:"bottom"`
	Width   int32  `json:"width"`
	Height  int32  `json:"height"`
	Visible bool   `json:"visible"`
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
func HandleFindWindow(args map[string]interface{}) *types.ToolResult {
	hwndVal, hasHWND := args["hwnd"].(float64)
	title, _ := args["title"].(string)

	var hwnd syscall.Handle
	var err error
	if hasHWND && hwndVal > 0 {
		hwnd = syscall.Handle(uintptr(int32(hwndVal)))
	} else if title != "" {
		hwnd, err = FindWindowByTitle(title)
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("window not found: %s", err.Error()), Tool: "find_window"}
		}
	} else {
		ret, _, _ := GetForegroundWindow.Call()
		hwnd = syscall.Handle(ret)
	}

	info := GetWindowInfo(hwnd)
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("found window: %s (hwnd=%d, class=%s, rect=%dx%d+%d+%d)", info.Title, info.Hwnd, info.Class, info.Width, info.Height, info.Left, info.Top),
		Tool:    "find_window",
	}
}

// HandleListWindows lists all visible windows.
func HandleListWindows(args map[string]interface{}) *types.ToolResult {
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

	lines := make([]string, 0, len(infos))
	for _, info := range infos {
		vis := ""
		if !info.Visible {
			vis = " (hidden)"
		}
		lines = append(lines, fmt.Sprintf("hwnd=%d %s [%dx%d+%d+%d] class=%s%s", info.Hwnd, info.Title, info.Width, info.Height, info.Left, info.Top, info.Class, vis))
	}
	return &types.ToolResult{
		Success: true,
		Output:  strings.Join(lines, "\n"),
		Tool:    "list_windows",
	}
}

// HandleGetWindowRect gets the rectangle of a window.
func HandleGetWindowRectFn(args map[string]interface{}) *types.ToolResult {
	hwndVal, _ := args["hwnd"].(float64)
	title, _ := args["window"].(string)

	hwnd, err := ResolveWindow(hwndVal, title)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "get_window_rect"}
	}

	info := GetWindowInfo(hwnd)
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("%s hwnd=%d rect=%d %d %d %d", info.Title, info.Hwnd, info.Left, info.Top, info.Width, info.Height),
		Tool:    "get_window_rect",
	}
}

// HandleSetWindowRect sets the position and size of a window.
func HandleSetWindowRect(args map[string]interface{}) *types.ToolResult {
	hwndVal, _ := args["hwnd"].(float64)
	title, _ := args["window"].(string)

	hwnd, err := ResolveWindow(hwndVal, title)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "set_window_rect"}
	}

	x, ok1 := args["x"].(float64)
	y, ok2 := args["y"].(float64)
	w, ok3 := args["width"].(float64)
	hgt, ok4 := args["height"].(float64)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return &types.ToolResult{Success: false, Error: "x, y, width, height required", Tool: "set_window_rect"}
	}

	SetWindowPos.Call(uintptr(hwnd), 0, uintptr(int32(x)), uintptr(int32(y)),
		uintptr(int32(w)), uintptr(int32(hgt)), SwpNoZOrder)
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("window set to %dx%d+%d+%d", int32(w), int32(hgt), int32(x), int32(y)),
		Tool:    "set_window_rect",
	}
}

// HandleFocusWindow focuses a window.
func HandleFocusWindow(args map[string]interface{}) *types.ToolResult {
	hwndVal, _ := args["hwnd"].(float64)
	title, _ := args["window"].(string)

	hwnd, err := ResolveWindow(hwndVal, title)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "focus_window"}
	}

	SetForegroundWindow.Call(uintptr(hwnd))
	titleStr := GetWindowTitle(hwnd)
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("window focused: %s (hwnd=%d)", titleStr, hwnd),
		Tool:    "focus_window",
	}
}

// HandleMinimizeWindow minimizes a window.
func HandleMinimizeWindow(args map[string]interface{}) *types.ToolResult {
	hwndVal, _ := args["hwnd"].(float64)
	title, _ := args["window"].(string)

	hwnd, err := ResolveWindow(hwndVal, title)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "minimize_window"}
	}

	ShowWindow.Call(uintptr(hwnd), SwMinimize)
	titleStr := GetWindowTitle(hwnd)
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("window minimized: %s (hwnd=%d)", titleStr, hwnd),
		Tool:    "minimize_window",
	}
}

// HandleMaximizeWindow maximizes a window.
func HandleMaximizeWindow(args map[string]interface{}) *types.ToolResult {
	hwndVal, _ := args["hwnd"].(float64)
	title, _ := args["window"].(string)

	hwnd, err := ResolveWindow(hwndVal, title)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "maximize_window"}
	}

	ShowWindow.Call(uintptr(hwnd), SwMaximize)
	titleStr := GetWindowTitle(hwnd)
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("window maximized: %s (hwnd=%d)", titleStr, hwnd),
		Tool:    "maximize_window",
	}
}

// HandleRestoreWindow restores a window.
func HandleRestoreWindow(args map[string]interface{}) *types.ToolResult {
	hwndVal, _ := args["hwnd"].(float64)
	title, _ := args["window"].(string)

	hwnd, err := ResolveWindow(hwndVal, title)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error(), Tool: "restore_window"}
	}

	ShowWindow.Call(uintptr(hwnd), SwRestore)
	titleStr := GetWindowTitle(hwnd)
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("window restored: %s (hwnd=%d)", titleStr, hwnd),
		Tool:    "restore_window",
	}
}

func init() {
	types.RegisterSimplify("find_window", types.SimpleAction("find_window"))
	types.RegisterSimplify("list_windows", types.SimpleAction("list_windows"))
	types.RegisterSimplify("get_window_rect", types.SimpleAction("get_window_rect"))
	types.RegisterSimplify("set_window_rect", types.SimpleAction("set_window_rect"))
	types.RegisterSimplify("focus_window", types.SimpleAction("focus_window"))
	types.RegisterSimplify("minimize_window", types.SimpleAction("minimize_window"))
	types.RegisterSimplify("maximize_window", types.SimpleAction("maximize_window"))
	types.RegisterSimplify("restore_window", types.SimpleAction("restore_window"))
}
