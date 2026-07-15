package desktop

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleTypeText handles type_text tool.
func HandleTypeText(args map[string]interface{}) *types.ToolResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	text, _ := args["text"].(string)
	if text == "" {
		return &types.ToolResult{Success: false, Error: "text is required", Tool: "type_text"}
	}

	hwndVal, hasHWND := args["hwnd"].(float64)
	winTitle, hasTitle := args["window"].(string)
	if hasHWND || hasTitle {
		hwnd, err := ResolveWindow(hwndVal, winTitle)
		if err != nil {
			return &types.ToolResult{Success: false, Error: err.Error(), Tool: "type_text"}
		}
		ForceForegroundWindow(uintptr(hwnd))
		time.Sleep(time.Millisecond * 200)
	}

	for _, r := range text {
		if r < 128 {
			ch := byte(r)
			vk, needsShift := CharToVK(ch)
			if needsShift {
				SendKey(0x10, false)
			}
			SendKey(vk, false)
			SendKey(vk, true)
			if needsShift {
				SendKey(0x10, true)
			}
		} else {
			unichar := uint16(r)
			SendKeyUnicode(unichar, false)
			SendKeyUnicode(unichar, true)
		}
	}
	return &types.ToolResult{Success: true, Output: fmt.Sprintf("typed %d chars", len(text)), Tool: "type_text"}
}

// HandleKeyPress handles key_press tool.
func HandleKeyPress(args map[string]interface{}) *types.ToolResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	key, _ := args["key"].(string)
	if key == "" {
		return &types.ToolResult{Success: false, Error: "key is required", Tool: "key_press"}
	}

	vk, ok := VKMap[strings.ToLower(key)]
	if !ok {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("unknown key: %s", key), Tool: "key_press"}
	}

	hwndVal, hasHWND := args["hwnd"].(float64)
	winTitle, hasTitle := args["window"].(string)
	if hasHWND || hasTitle {
		hwnd, err := ResolveWindow(hwndVal, winTitle)
		if err != nil {
			return &types.ToolResult{Success: false, Error: err.Error(), Tool: "key_press"}
		}
		ForceForegroundWindow(uintptr(hwnd))
		time.Sleep(time.Millisecond * 200)
	}

	modRaw, _ := args["modifiers"].([]interface{})
	modVks := DesktopParseMods(modRaw)

	for _, mk := range modVks {
		if !SendKey(mk, false) {
			SendKey(mk, true)
			for j := len(modVks) - 1; j >= 0; j-- {
				SendKey(modVks[j], true)
			}
			return &types.ToolResult{Success: false, Error: "SendInput failed to inject modifier key event", Tool: "key_press"}
		}
		time.Sleep(time.Millisecond * 30)
	}

	if !SendKey(vk, false) {
		for i := len(modVks) - 1; i >= 0; i-- {
			SendKey(modVks[i], true)
		}
		return &types.ToolResult{Success: false, Error: "SendInput failed to inject key event", Tool: "key_press"}
	}
	time.Sleep(time.Millisecond * 30)

	if !SendKey(vk, true) {
		return &types.ToolResult{Success: false, Error: "SendInput failed to inject key up event", Tool: "key_press"}
	}
	time.Sleep(time.Millisecond * 30)

	for i := len(modVks) - 1; i >= 0; i-- {
		if !SendKey(modVks[i], true) {
			return &types.ToolResult{Success: false, Error: "SendInput failed to inject modifier key up event", Tool: "key_press"}
		}
		time.Sleep(time.Millisecond * 30)
	}

	return &types.ToolResult{Success: true, Output: fmt.Sprintf("key pressed: %s", key), Tool: "key_press"}
}

// HandleKeyDown handles key_down tool.
func HandleKeyDown(args map[string]interface{}) *types.ToolResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	key, _ := args["key"].(string)
	if key == "" {
		return &types.ToolResult{Success: false, Error: "key is required", Tool: "key_down"}
	}

	vk, ok := VKMap[strings.ToLower(key)]
	if !ok {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("unknown key: %s", key), Tool: "key_down"}
	}

	hwndVal, hasHWND := args["hwnd"].(float64)
	winTitle, hasTitle := args["window"].(string)
	if hasHWND || hasTitle {
		hwnd, err := ResolveWindow(hwndVal, winTitle)
		if err != nil {
			return &types.ToolResult{Success: false, Error: err.Error(), Tool: "key_down"}
		}
		ForceForegroundWindow(uintptr(hwnd))
		time.Sleep(time.Millisecond * 200)
	}

	SendKey(vk, false)
	return &types.ToolResult{Success: true, Output: fmt.Sprintf("key held down: %s", key), Tool: "key_down"}
}

// HandleKeyUp handles key_up tool.
func HandleKeyUp(args map[string]interface{}) *types.ToolResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	key, _ := args["key"].(string)
	if key == "" {
		return &types.ToolResult{Success: false, Error: "key is required", Tool: "key_up"}
	}

	vk, ok := VKMap[strings.ToLower(key)]
	if !ok {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("unknown key: %s", key), Tool: "key_up"}
	}

	hwndVal, hasHWND := args["hwnd"].(float64)
	winTitle, hasTitle := args["window"].(string)
	if hasHWND || hasTitle {
		hwnd, err := ResolveWindow(hwndVal, winTitle)
		if err != nil {
			return &types.ToolResult{Success: false, Error: err.Error(), Tool: "key_up"}
		}
		ForceForegroundWindow(uintptr(hwnd))
		time.Sleep(time.Millisecond * 200)
	}

	SendKey(vk, true)
	return &types.ToolResult{Success: true, Output: fmt.Sprintf("key released: %s", key), Tool: "key_up"}
}

func init() {
	types.RegisterSimplify("type_text", types.SimpleAction("type_text"))
	types.RegisterSimplify("key_press", types.SimpleAction("key_press"))
	types.RegisterSimplify("key_down", types.SimpleAction("key_down"))
	types.RegisterSimplify("key_up", types.SimpleAction("key_up"))
}
