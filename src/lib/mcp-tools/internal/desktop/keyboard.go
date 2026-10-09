package desktop

import (
	"fmt"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

// HandleTypeText handles type_text tool.
func HandleTypeText(args map[string]interface{}) *ToolResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	text, _ := args["text"].(string)
	if text == "" {
		return &ToolResult{Success: false, Error: "text is required", Output: "❌ 输入文本失败：缺少文本内容", Tool: "text_type"}
	}

	hwndVal, hasHWND := args["hwnd"].(float64)
	winTitle, hasTitle := args["window"].(string)
	if hasHWND || hasTitle {
		hwnd, err := ResolveWindow(hwndVal, winTitle)
		if err != nil {
			return &ToolResult{Success: false, Error: err.Error(), Output: fmt.Sprintf("❌ 输入文本失败：%s", err.Error()), Tool: "text_type"}
		}
		ForceForegroundWindow(uintptr(hwnd))
		time.Sleep(time.Millisecond * 200)
	}

	for _, r := range text {
		if r < 0x20 {
			// 控制类（换行/制表/回车）：VK 注入
			vk, needsShift := CharToVK(byte(r))
			if needsShift {
				SendKey(0x10, false)
			}
			SendKey(vk, false)
			SendKey(vk, true)
			if needsShift {
				SendKey(0x10, true)
			}
			continue
		}
		// 可打印字符（含 ASCII 标点）：Unicode 注入，绕过 IME（避免中文态全角化）
		SendKeyUnicode(uint16(r), false)
		SendKeyUnicode(uint16(r), true)
	}
	// 字符数按 rune 计（C-46）：注入循环本身按 rune 遍历，提示/回报亦须一致
	//（len(text) 为 UTF-8 字节数，中文会翻倍）。
	n := utf8.RuneCountInString(text)
	return &ToolResult{Success: true, Output: fmt.Sprintf("⌨️ 已输入 %d 字符", n), Tool: "text_type", RawResult: map[string]interface{}{"action": "typetext", "text": text, "char_count": n}}
}

// HandleKeyPress handles key_press tool.
func HandleKeyPress(args map[string]interface{}) *ToolResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	key, _ := args["key"].(string)
	if key == "" {
		return &ToolResult{Success: false, Error: "key is required", Output: "❌ 按键失败：缺少按键名称", Tool: "key_press"}
	}

	vk, ok := VKMap[strings.ToLower(key)]
	if !ok {
		return &ToolResult{Success: false, Error: fmt.Sprintf("unknown key: %s", key), Output: fmt.Sprintf("❌ 按键失败：未知按键 %s", key), Tool: "key_press"}
	}

	hwndVal, hasHWND := args["hwnd"].(float64)
	winTitle, hasTitle := args["window"].(string)
	if hasHWND || hasTitle {
		hwnd, err := ResolveWindow(hwndVal, winTitle)
		if err != nil {
			return &ToolResult{Success: false, Error: err.Error(), Output: fmt.Sprintf("❌ 按键失败：%s", err.Error()), Tool: "key_press"}
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
			return &ToolResult{Success: false, Error: "SendInput failed to inject modifier key event", Output: "❌ 按键失败：发送修饰键事件失败", Tool: "key_press"}
		}
		time.Sleep(time.Millisecond * 30)
	}

	if !SendKey(vk, false) {
		for i := len(modVks) - 1; i >= 0; i-- {
			SendKey(modVks[i], true)
		}
		return &ToolResult{Success: false, Error: "SendInput failed to inject key event", Output: "❌ 按键失败：发送按键事件失败", Tool: "key_press"}
	}
	time.Sleep(time.Millisecond * 30)

	if !SendKey(vk, true) {
		// 主键释放失败也须先释放已按下的修饰键再报错，避免修饰键卡住（C-31）
		for i := len(modVks) - 1; i >= 0; i-- {
			SendKey(modVks[i], true)
		}
		return &ToolResult{Success: false, Error: "SendInput failed to inject key up event", Output: "❌ 按键失败：发送按键释放事件失败", Tool: "key_press"}
	}
	time.Sleep(time.Millisecond * 30)

	modUpFailed := false
	for i := len(modVks) - 1; i >= 0; i-- {
		if !SendKey(modVks[i], true) {
			// 单个修饰键释放失败不中断循环，继续释放其余修饰键（避免卡键），循环后统一报错
			modUpFailed = true
			continue
		}
		time.Sleep(time.Millisecond * 30)
	}
	if modUpFailed {
		return &ToolResult{Success: false, Error: "SendInput failed to inject modifier key up event", Output: "❌ 按键失败：发送修饰键释放事件失败", Tool: "key_press"}
	}

	modRaw, _ = args["modifiers"].([]interface{})
	modList := make([]string, 0, len(modRaw))
	for _, m := range modRaw {
		if s, ok := m.(string); ok {
			modList = append(modList, s)
		}
	}

	return &ToolResult{Success: true, Output: fmt.Sprintf("⌨️ 按键已按下：%s", key), Tool: "key_press", RawResult: map[string]interface{}{"action": "keypress", "key": key, "modifiers": modList}}
}

// HandleKeyDown handles key_down tool.
func HandleKeyDown(args map[string]interface{}) *ToolResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	key, _ := args["key"].(string)
	if key == "" {
		return &ToolResult{Success: false, Error: "key is required", Output: "❌ 按键按住失败：缺少按键名称", Tool: "key_down"}
	}

	vk, ok := VKMap[strings.ToLower(key)]
	if !ok {
		return &ToolResult{Success: false, Error: fmt.Sprintf("unknown key: %s", key), Output: fmt.Sprintf("❌ 按键按住失败：未知按键 %s", key), Tool: "key_down"}
	}

	hwndVal, hasHWND := args["hwnd"].(float64)
	winTitle, hasTitle := args["window"].(string)
	if hasHWND || hasTitle {
		hwnd, err := ResolveWindow(hwndVal, winTitle)
		if err != nil {
			return &ToolResult{Success: false, Error: err.Error(), Output: fmt.Sprintf("❌ 按键按住失败：%s", err.Error()), Tool: "key_down"}
		}
		ForceForegroundWindow(uintptr(hwnd))
		time.Sleep(time.Millisecond * 200)
	}

	if !SendKey(vk, false) {
		return &ToolResult{Success: false, Error: "SendInput failed to inject key down event", Output: "❌ 按键按住失败：发送按键事件失败", Tool: "key_down"}
	}
	return &ToolResult{Success: true, Output: fmt.Sprintf("⌨️ 按键已按住：%s", key), Tool: "key_down", RawResult: map[string]interface{}{"action": "keydown", "key": key}}
}

// HandleKeyUp handles key_up tool.
func HandleKeyUp(args map[string]interface{}) *ToolResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	key, _ := args["key"].(string)
	if key == "" {
		return &ToolResult{Success: false, Error: "key is required", Output: "❌ 按键释放失败：缺少按键名称", Tool: "key_up"}
	}

	vk, ok := VKMap[strings.ToLower(key)]
	if !ok {
		return &ToolResult{Success: false, Error: fmt.Sprintf("unknown key: %s", key), Output: fmt.Sprintf("❌ 按键释放失败：未知按键 %s", key), Tool: "key_up"}
	}

	hwndVal, hasHWND := args["hwnd"].(float64)
	winTitle, hasTitle := args["window"].(string)
	if hasHWND || hasTitle {
		hwnd, err := ResolveWindow(hwndVal, winTitle)
		if err != nil {
			return &ToolResult{Success: false, Error: err.Error(), Output: fmt.Sprintf("❌ 按键释放失败：%s", err.Error()), Tool: "key_up"}
		}
		ForceForegroundWindow(uintptr(hwnd))
		time.Sleep(time.Millisecond * 200)
	}

	if !SendKey(vk, true) {
		return &ToolResult{Success: false, Error: "SendInput failed to inject key up event", Output: "❌ 按键释放失败：发送按键事件失败", Tool: "key_up"}
	}
	return &ToolResult{Success: true, Output: fmt.Sprintf("⌨️ 按键已释放：%s", key), Tool: "key_up", RawResult: map[string]interface{}{"action": "keyup", "key": key}}
}
