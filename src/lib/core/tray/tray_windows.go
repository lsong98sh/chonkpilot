//go:build windows

// Package tray 是**系统托盘**的共用实现（PRODSIM-1：从 `src/server` 的 package main 抽出）。
//
// 用 Win32 Shell_NotifyIcon 实现**零第三方依赖**的托盘图标：
//   - 双击图标 → OnDoubleClick；
//   - 右键 → 菜单「显示日志 / 打开项目 / 项目列表 / 退出」。
//
// 线程约束：Win32 消息循环必须跑在**同一 OS 线程**且该线程锁定（runtime.LockOSThread）；
// 故本包在独立 goroutine 内创建隐藏窗口 + 跑 GetMessage 循环，stop 时 PostQuitMessage。
//
// 关键时序（历史回归点）：`setup` 完成（窗口 + 图标就绪）即经 `ready` 回报，**随后才进入
// 消息循环** —— 否则 Start 永不返回，「退出」失效、调用方卡死。
package tray

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// Instance 是托盘「项目列表」菜单项（调用方填充）。
type Instance struct {
	ID    string
	Title string
}

// Callbacks 是托盘交互回调（全部由调用方实现，托盘只负责 Win32 交互）。
type Callbacks struct {
	// OnDoubleClick：双击托盘图标（默认动作 = 显示日志窗口）。
	OnDoubleClick func()
	// OnShowLog：右键菜单「显示日志」。
	OnShowLog func()
	// OnOpenProject：右键菜单「打开项目…」。
	OnOpenProject func()
	// OnExit：右键菜单「退出」（关闭所有窗口 + 停 server）。
	OnExit func()
	// ListProjects：菜单展开时取当前已打开项目（每次现取，保证新鲜）。
	ListProjects func() []Instance
	// OnPickProject：菜单点选某项目 → 激活其主窗口。
	OnPickProject func(id string)
	// Tip 是托盘悬停提示（空 = 缺省）。
	Tip string
}

var (
	shell32                 = syscall.NewLazyDLL("shell32.dll")
	user32                  = syscall.NewLazyDLL("user32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
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
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmRButtonUp     = 0x0205
	wmLButtonDblClk = 0x0203

	mfString       = 0x00000000
	mfSeparator    = 0x00000800
	mfGrayed       = 0x00000001
	tpmRightButton = 0x00000002
	tpmReturnCmd   = 0x00000100

	csDblClks = 0x0008

	idiApplication = 32512

	// 菜单命令 id 段。
	cmdShowLog     = 1
	cmdOpenProject = 2
	cmdExit        = 3
	cmdProjectBase = 1000
)

// notifyIconDataW 是 NOTIFYICONDATAW（字段与 winuser.h 逐字对应）。
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

type point struct {
	X int32
	Y int32
}

type msgStruct struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type tray struct {
	cb    Callbacks
	hwnd  uintptr
	hIcon uintptr
	nid   notifyIconDataW
	// cleanupOnce 使 cleanup 幂等（正常收尾 / setup 中段失败两条路径可能各调一次）。
	cleanupOnce sync.Once
}

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

// Start 启动托盘（独立锁定 OS 线程的 goroutine），返回停止函数（幂等）。
// 失败（注册窗口类 / 建窗 / Shell_NotifyIcon 失败）→ 返回错误，调用方回落为无托盘常驻。
func Start(cb Callbacks) (func(), error) {
	t := &tray{cb: cb}
	ready := make(chan error, 1)
	stopCh := make(chan struct{})

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := t.setup(); err != nil {
			ready <- err
			return
		}
		ready <- nil
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

// setup 在锁定线程内：注册窗口类 → 建隐藏窗口 → 加托盘图标（失败 → cleanup 后返回错误）。
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
		t.cleanup()
		return fmt.Errorf("RegisterClassExW: %v", err)
	}
	hwnd, _, err := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(utf16Ptr("chonkpilot"))),
		0, 0, 0, 0, 0, 0, 0, hInst, 0,
	)
	if hwnd == 0 {
		t.cleanup()
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
	tip := t.cb.Tip
	if tip == "" {
		tip = "ChonkPilot"
	}
	utf16Into(t.nid.SzTip[:], tip)
	if ret, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&t.nid))); ret == 0 {
		t.cleanup()
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

// cleanup 移除托盘图标 + 销毁窗口 + 注销类表项（幂等）。
func (t *tray) cleanup() {
	t.cleanupOnce.Do(func() {
		procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&t.nid)))
		procDestroyWindow.Call(t.hwnd)
		trayMu.Lock()
		delete(trayByHWnd, t.hwnd)
		trayMu.Unlock()
	})
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

// showMenu 弹出右键菜单（显示日志 / 打开项目 / 项目列表 / 退出），处理点选。
func (t *tray) showMenu() {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	if t.cb.OnShowLog != nil {
		procAppendMenuW.Call(hMenu, mfString, cmdShowLog, uintptr(unsafe.Pointer(utf16Ptr("显示日志"))))
	}
	if t.cb.OnOpenProject != nil {
		procAppendMenuW.Call(hMenu, mfString, cmdOpenProject, uintptr(unsafe.Pointer(utf16Ptr("打开项目…"))))
	}
	procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)

	projects := []Instance{}
	if t.cb.ListProjects != nil {
		projects = t.cb.ListProjects()
	}
	if len(projects) == 0 {
		procAppendMenuW.Call(hMenu, mfString|mfGrayed, 0, uintptr(unsafe.Pointer(utf16Ptr("（无已打开项目）"))))
	} else {
		for i, p := range projects {
			procAppendMenuW.Call(hMenu, mfString, uintptr(cmdProjectBase+i), uintptr(unsafe.Pointer(utf16Ptr(p.Title))))
		}
	}
	procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)
	procAppendMenuW.Call(hMenu, mfString, cmdExit, uintptr(unsafe.Pointer(utf16Ptr("退出"))))

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// SetForegroundWindow 是 TrackPopupMenu 的既有要求（否则菜单不消失）。
	procSetForegroundWindow.Call(t.hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(hMenu, tpmRightButton|tpmReturnCmd, uintptr(pt.X), uintptr(pt.Y), 0, t.hwnd, 0)
	// Win32 惯例：TrackPopupMenu 后补投 WM_NULL → 菜单立即消隐、避免残影。
	procPostMessageW.Call(t.hwnd, wmNull, 0, 0)

	switch {
	case cmd == cmdShowLog:
		if t.cb.OnShowLog != nil {
			go t.cb.OnShowLog()
		}
	case cmd == cmdOpenProject:
		if t.cb.OnOpenProject != nil {
			go t.cb.OnOpenProject()
		}
	case cmd == cmdExit:
		if t.cb.OnExit != nil {
			go t.cb.OnExit()
		}
	case cmd >= cmdProjectBase && int(cmd-cmdProjectBase) < len(projects):
		id := projects[cmd-cmdProjectBase].ID
		if t.cb.OnPickProject != nil {
			go t.cb.OnPickProject(id)
		}
	}
}
