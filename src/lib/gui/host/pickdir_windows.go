//go:build windows

package host

import (
	"runtime"
	"syscall"
	"unsafe"
)

var (
	shell32Pick             = syscall.NewLazyDLL("shell32.dll")
	ole32Pick               = syscall.NewLazyDLL("ole32.dll")
	procSHBrowseForFolder   = shell32Pick.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDList = shell32Pick.NewProc("SHGetPathFromIDListW")
	procCoInitializeExPick  = ole32Pick.NewProc("CoInitializeEx")
	procCoUninitializePick  = ole32Pick.NewProc("CoUninitialize")
)

// browseInfoW 是 BROWSEINFOW（字段与 shlobj.h 逐字对应）。
type browseInfoW struct {
	HwndOwner      uintptr
	PidlRoot       uintptr
	PszDisplayName *uint16
	LpszTitle      *uint16
	UlFlags        uint32
	Lpfn           uintptr
	LParam         uintptr
	IImage         int32
}

const (
	bifReturnOnlyFSDirs = 0x0001
	bifNewDialogStyle   = 0x0040
	coInitApartment     = 0x2
)

// pickDirectory 弹出系统「选择文件夹」对话框（托盘「打开项目」用）；取消 → 空串。
//
// SHBrowseForFolderW 需要 COM（STA）环境，故本函数锁定 OS 线程并显式 CoInitializeEx。
func pickDirectory(title string) string {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := procCoInitializeExPick.Call(0, coInitApartment)
	if hr == 0 || hr == 1 { // S_OK / S_FALSE
		defer procCoUninitializePick.Call()
	}

	buf := make([]uint16, syscall.MAX_PATH)
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	b := browseInfoW{
		PszDisplayName: &buf[0],
		LpszTitle:      titlePtr,
		UlFlags:        bifReturnOnlyFSDirs | bifNewDialogStyle,
	}
	pidl, _, _ := procSHBrowseForFolder.Call(uintptr(unsafe.Pointer(&b)))
	if pidl == 0 {
		return ""
	}
	// pidl 由 shell 分配（CoTaskMemAlloc）；一次性对话框，进程级泄漏极小，不额外释放。
	if r, _, _ := procSHGetPathFromIDList.Call(pidl, uintptr(unsafe.Pointer(&buf[0]))); r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}
