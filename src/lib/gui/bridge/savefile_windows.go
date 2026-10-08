//go:build windows

// Win32「另存为」对话框（gui.file.save 的 mode=dialog 路径）。
//
// GetSaveFileNameW（comdlg32）+ OPENFILENAMEW —— 结构体字段顺序与
// `chonkpilot-gui/internal/folder/picker_windows.go` 的 OPENFILENAMEW（GetOpenFileNameW 用）
// **逐字段同构**（LStructSize 必须等于真实布局大小，少一字段会被 API 拒绝）。
package bridge

import (
	"fmt"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modComdlg32          = windows.NewLazySystemDLL("comdlg32.dll")
	procGetSaveFileNameW = modComdlg32.NewProc("GetSaveFileNameW")
)

// OPENFILENAMEW（与 internal/folder 同构）。
type openFileNameW struct {
	LStructSize       uint32
	HwndOwner         uintptr
	HInstance         uintptr
	LpstrFilter       *uint16
	LpstrCustomFilter *uint16
	NMaxCustFilter    uint32
	NFilterIndex      uint32
	LpstrFile         *uint16
	NMaxFile          uint32
	LpstrFileTitle    *uint16
	NMaxFileTitle     uint32
	LpstrInitialDir   *uint16
	LpstrTitle        *uint16
	Flags             uint32
	NFileOffset       uint16
	NFileExtension    uint16
	LpstrDefExt       *uint16
	LCustData         uintptr
	LpfnHook          uintptr
	LpTemplateName    *uint16
	PvReserved        uintptr
	DwReserved        uint32
	FlagsEx           uint32
}

const (
	ofnOverwritePrompt = 0x00000002
	ofnHideReadOnly    = 0x00000004
	ofnNoChangeDir     = 0x00000008
	ofnPathMustExist   = 0x00000800
)

// pickSaveFileName 弹系统「另存为」对话框：
// 成功 → 用户选定路径；用户取消 → ""（与 folder.PickFile 的取消口径一致）。
// defaultName 作为初始文件名（前端已含时间戳），filter 形如
// "JSON files\x00*.json\x00All files\x00*.*\x00\x00"。
func pickSaveFileName(title, defaultName, filter string) (string, error) {
	if title == "" {
		title = "Save File"
	}
	if filter == "" {
		filter = "All files\x00*.*\x00\x00"
	}

	// 文件名缓冲区：初始填入 defaultName（NUL 结尾；API 就地被用户改写）。
	// 必须按 **UTF-16 code unit** 写入（utf16.Encode 会正确处理非 BMP 字符的代理对），
	// 并以 code unit 数判界；**不可**用 `for i, r := range` 的字节下标逐字符填 uint16
	// （中文等多字节字符会错位、且留下未初始化 NUL → 被 API 当作字符串结尾）。
	// 保留 1 个 code unit 给结尾 NUL（make 已零值初始化）。
	fileBuf := make([]uint16, 260)
	enc := utf16.Encode([]rune(defaultName))
	if len(enc) > len(fileBuf)-1 {
		enc = enc[:len(fileBuf)-1]
	}
	copy(fileBuf, enc)

	// 过滤器：UTF-16 序列（含结尾双 NUL）
	var filterBuf []uint16
	for _, r := range filter {
		filterBuf = append(filterBuf, uint16(r))
	}
	for len(filterBuf) < 2 || filterBuf[len(filterBuf)-1] != 0 || filterBuf[len(filterBuf)-2] != 0 {
		filterBuf = append(filterBuf, 0)
	}

	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return "", fmt.Errorf("convert title to utf16: %w", err)
	}

	ofn := &openFileNameW{
		LStructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		LpstrFilter: &filterBuf[0],
		LpstrFile:   &fileBuf[0],
		NMaxFile:    260,
		LpstrTitle:  titlePtr,
		Flags:       ofnOverwritePrompt | ofnHideReadOnly | ofnNoChangeDir | ofnPathMustExist,
	}

	ret, _, _ := procGetSaveFileNameW.Call(uintptr(unsafe.Pointer(ofn)))
	if ret == 0 {
		return "", nil // 用户取消
	}
	return windows.UTF16ToString(fileBuf), nil
}
