//go:build windows

// desktop 分体常驻版 —— 系统托盘（🆕 2026-10-09；61-消息一览 §4.1 ⑥ · 62 §3A）。
//
// 用 Win32 Shell_NotifyIcon 实现**零第三方依赖**的托盘图标：
//   - 双击图标 → OnDoubleClick（打开最近实例；无实例则 spawn flash，由调用方决定）；
//   - 右键 → 弹出菜单「退出 / 打开新窗口 / 列举实例」（子窗口实例由调用方经 ListInstances 过滤）；
//
// 线程约束：Win32 消息循环必须跑在**同一 OS 线程**且该线程锁定（runtime.LockOSThread）；
// 故本文件在独立 goroutine 内创建隐藏窗口 + 跑 GetMessage 循环，stop 时 PostQuitMessage。
package main

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// TrayInstance 是托盘「列举实例」菜单项（调用方填充；已过滤子窗口实例）。
type TrayInstance struct {
	ID    string
	Title string
}

// TrayCallbacks 是托盘交互回调（全部由调用方在 main 侧实现，托盘只负责 Win32 交互）。
type TrayCallbacks struct {
	// OnDoubleClick：双击托盘图标（打开最近实例；无实例则新窗口）。
	OnDoubleClick func()
	// OnNewWindow：右键菜单「打开新窗口」。
	OnNewWindow func()
	// OnExit：右键菜单「退出」。
	OnExit func()
	// ListInstances：右键菜单展开时取顶层实例列表（每次点开菜单现取，保证新鲜）。
	ListInstances func() []TrayInstance
	// OnPickInstance：菜单点选某实例 → 置顶激活该实例。
	OnPickInstance func(id string)
}

var (
	shell32                 = syscall.NewLazyDLL("shell32.dll")
	user32T                 = syscall.NewLazyDLL("user32.dll")
	kernel32T               = syscall.NewLazyDLL("kernel32.dll")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procRegisterClassExW    = user32T.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32T.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32T.NewProc("DefWindowProcW")
	procGetMessageW         = user32T.NewProc("GetMessageW")
	procTranslateMessage    = user32T.NewProc("TranslateMessage")
	procDispatchMessageW    = user32T.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32T.NewProc("PostQuitMessage")
	procPostMessageW        = user32T.NewProc("PostMessageW")
	procDestroyWindow       = user32T.NewProc("DestroyWindow")
	procLoadIconW           = user32T.NewProc("LoadIconW")
	procCreatePopupMenu     = user32T.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32T.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32T.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32T.NewProc("DestroyMenu")
	procSetForegroundWindow = user32T.NewProc("SetForegroundWindow")
	procGetCursorPos        = user32T.NewProc("GetCursorPos")
	procGetModuleHandleW    = kernel32T.NewProc("GetModuleHandleW")
	procDestroyIcon         = user32T.NewProc("DestroyIcon")
)

const (
	nimAdd    = 0x00000000
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	wmUser          = 0x0400
	wmTrayIcon      = wmUser + 1
	wmNull          = 0x0000
	wmCommand       = 0x0111
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmRButtonUp     = 0x0205
	wmLButtonDblClk = 0x0203

	mfString       = 0x00000000
	mfSeparator    = 0x00000800
	tpmRightButton = 0x00000002
	tpmReturnCmd   = 0x00000100

	csDblClks = 0x0008

	idiApplication = 32512

	// 菜单命令 id 段。
	cmdExit         = 1
	cmdNewWindow    = 2
	cmdInstanceBase = 1000
)

// notifyIconDataW 是 NOTIFYICONDATAW（ANSI/W 版；字段与 winuser.h 逐字对应）。
type notifyIconDataW struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

// wndClassExW 是 WNDCLASSEXW。
type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

// point 是 POINT。
type point struct{ X, Y int32 }

// msgStruct 是 MSG。
type msgStruct struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

// tray 是一个托盘图标实例（持有隐藏窗口 + 回调）。
type tray struct {
	cb    TrayCallbacks
	hwnd  uintptr
	hIcon uintptr
	nid   notifyIconDataW
}

// 全局注册的 WndProc 目标（syscall 回调无闭包捕获 → 经 registry 按 hwnd 分发）。
var (
	trayMu      sync.Mutex
	trayByHWnd  = map[uintptr]*tray{}
	trayWndProc = syscall.NewCallback(trayProc)
)

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func utf16Into(dst []uint16, s string) {
	u := syscall.StringToUTF16(s)
	n := copy(dst[:len(dst)-1], u)
	dst[n] = 0
}

// startTray 启动托盘（独立锁定 OS 线程的 goroutine），返回停止函数（幂等）。
// 失败（注册窗口类 / 建窗 / Shell_NotifyIcon 失败）→ 返回错误，调用方回落为无托盘常驻。
//
// 关键：`setup` 完成（窗口 + 托盘图标就绪）即经 `ready` 回报，**随后才进入消息循环**——
// 故本函数能在 `(stop, nil)` 后**正常返回**，调用方其后的代码（`--autoexit` 循环 /
// `<-ctx.Done()` / 关闭流程）得以执行（旧实现把 `ready <- nil` 放在永不返回的消息循环之后
// → 本函数**永远阻塞**，致「退出」失效、main 卡死）。
func startTray(cb TrayCallbacks) (func(), error) {
	t := &tray{cb: cb}
	ready := make(chan error, 1)
	stopCh := make(chan struct{})

	// 消息循环必须跑在**锁定 OS 线程**的同一 goroutine（Win32 约束）。
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := t.setup(); err != nil {
			ready <- err
			return
		}
		ready <- nil
		// 停止请求早于消息循环进入（极短窗口）→ 不再进入 GetMessage，直接清理返回。
		select {
		case <-stopCh:
		default:
			t.loop() // 阻塞：GetMessage 循环，stop 经 PostMessageW(WM_CLOSE) → WM_QUIT 返回
		}
		t.cleanup()
	}()

	if err := <-ready; err != nil {
		return func() {}, err
	}

	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(stopCh)
			if t.hwnd != 0 {
				procPostMessageW.Call(t.hwnd, wmClose, 0, 0)
			}
		})
	}
	return stop, nil
}

// setup 在锁定线程内：注册窗口类 → 建隐藏窗口 → 加托盘图标（失败 → 返回错误）。
func (t *tray) setup() error {
	hInst, _, _ := procGetModuleHandleW.Call(0)
	className := utf16Ptr("ChonkPilotTrayWnd")

	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		Style:         csDblClks,
		LpfnWndProc:   trayWndProc,
		HInstance:     hInst,
		LpszClassName: className,
	}
	if ret, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); ret == 0 {
		return fmt.Errorf("RegisterClassExW: %v", err)
	}
	hwnd, _, err := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(utf16Ptr("chonkpilot-server"))),
		0, 0, 0, 0, 0, 0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW: %v", err)
	}
	t.hwnd = hwnd

	hIcon, _, _ := procLoadIconW.Call(0, idiApplication)
	t.hIcon = hIcon

	trayMu.Lock()
	trayByHWnd[hwnd] = t
	trayMu.Unlock()

	t.nid = notifyIconDataW{
		CbSize:           uint32(unsafe.Sizeof(notifyIconDataW{})),
		HWnd:             hwnd,
		UID:              1,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: wmTrayIcon,
		HIcon:            hIcon,
	}
	utf16Into(t.nid.SzTip[:], "ChonkPilot 常驻服务（chonkpilot-server）")
	if ret, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&t.nid))); ret == 0 {
		return fmt.Errorf("Shell_NotifyIconW(NIM_ADD): %v", err)
	}
	return nil
}

// loop 跑消息循环（在锁定线程的同一 goroutine；仅 WM_QUIT 返回）。
func (t *tray) loop() {
	var m msgStruct
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if ret == 0 || ret == ^uintptr(0) { // WM_QUIT 或错误
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// cleanup 移除托盘图标 + 销毁窗口 + 注销类表项（在锁定线程的同一 goroutine）。
func (t *tray) cleanup() {
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&t.nid)))
	procDestroyWindow.Call(t.hwnd)
	trayMu.Lock()
	delete(trayByHWnd, t.hwnd)
	trayMu.Unlock()
}

// trayProc 是窗口过程（经 registry 按 hwnd 找 *tray）。
func trayProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	trayMu.Lock()
	t := trayByHWnd[hwnd]
	trayMu.Unlock()

	switch msg {
	case wmTrayIcon:
		switch lparam {
		case wmLButtonDblClk:
			if t != nil && t.cb.OnDoubleClick != nil {
				go t.cb.OnDoubleClick()
			}
		case wmRButtonUp:
			if t != nil {
				t.showMenu()
			}
		}
		return 0
	case wmClose, wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
	return ret
}

// showMenu 弹出右键菜单（退出 / 打开新窗口 / 列举实例），处理点选。
func (t *tray) showMenu() {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	procAppendMenuW.Call(hMenu, mfString, cmdExit, uintptr(unsafe.Pointer(utf16Ptr("退出"))))
	procAppendMenuW.Call(hMenu, mfString, cmdNewWindow, uintptr(unsafe.Pointer(utf16Ptr("打开新窗口"))))
	procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)

	instances := []TrayInstance{}
	if t.cb.ListInstances != nil {
		instances = t.cb.ListInstances()
	}
	if len(instances) == 0 {
		procAppendMenuW.Call(hMenu, mfString|0x00000004 /*MF_GRAYED*/, 0, uintptr(unsafe.Pointer(utf16Ptr("（无实例）"))))
	} else {
		for i, inst := range instances {
			procAppendMenuW.Call(hMenu, mfString, uintptr(cmdInstanceBase+i), uintptr(unsafe.Pointer(utf16Ptr(inst.Title))))
		}
	}

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// SetForegroundWindow 是 TrackPopupMenu 的既有要求（否则菜单不消失）。
	procSetForegroundWindow.Call(t.hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(hMenu, tpmRightButton|tpmReturnCmd, uintptr(pt.X), uintptr(pt.Y), 0, t.hwnd, 0)
	// Win32 惯例：TrackPopupMenu 后补投 WM_NULL → 菜单立即消隐、避免残留残影（配合前述 SetForegroundWindow）。
	procPostMessageW.Call(t.hwnd, wmNull, 0, 0)

	switch {
	case cmd == cmdExit:
		if t.cb.OnExit != nil {
			go t.cb.OnExit()
		}
	case cmd == cmdNewWindow:
		if t.cb.OnNewWindow != nil {
			go t.cb.OnNewWindow()
		}
	case cmd >= cmdInstanceBase && int(cmd-cmdInstanceBase) < len(instances):
		id := instances[cmd-cmdInstanceBase].ID
		if t.cb.OnPickInstance != nil {
			go t.cb.OnPickInstance(id)
		}
	}
}

// errTrayUnsupported 是托盘不可用的哨兵（非 Windows 构建回落用）。
var errTrayUnsupported = errors.New("tray: unsupported on this platform")
