//go:build windows

package gui

// 具名窗口类「窗口单例」（🆕 2026-10-10；19 §8.11 / 62 §3A）——**仅 flash 远程薄壳使用**。
//
// 用户口径「单例使用 wndclass 判断」：以 Windows **具名窗口类**承担**窗口单例** ——
//   - 类名 = `flash.WindowClassName(user, workDir)`（同用户 + 同项目 → 同名）；
//   - 启动时 `FindWindow(class)`：命中 → 向既有窗口投 `wmAppBringToTop`（WM_APP+1）→
//     既有实例**置顶其主窗口**、本实例退出（不新开窗口）；
//   - 未命中 → 注册同名类 + 建**隐藏哨兵窗口**（消息循环在独占 OS 线程上）→ 持有至进程退出。
//
// 与 per-user work-dir 锁**并存、职责不同**：锁管「项目是否已被打开」（数据面独占），
// 具名窗口类管「窗口单例」（UI 面独占，且**不依赖 server 可达**）。详见 `flash/singleton.go`。
//
// 依据：Windows 窗口类名以**字符串**参与 `FindWindow` 匹配（跨进程可见），故同名类可作
// 跨进程单例判据（同进程二次 `RegisterClassEx` 亦返回 ERROR_CLASS_ALREADY_EXISTS）。

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// wmAppBringToTop 是「置顶既有主窗口」的私有消息（WM_APP + 1）。
	wmAppBringToTop = 0x8000 + 1
	// wmCloseWin / wmDestroyWin 是标准窗口消息（哨兵窗口收 WM_CLOSE 即退出消息循环）。
	wmCloseWin   = 0x0010
	wmDestroyWin = 0x0002
	// csWinSingletonDblClks 窗口类风格（哨兵窗口无输入需求，仅沿用通用风格位）。
	csWinSingletonDblClks = 0x0008
)

var (
	procFindWindowW      = user32.NewProc("FindWindowW")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procPostMessageW     = user32.NewProc("PostMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procDestroyWindowWin = user32.NewProc("DestroyWindow")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

// wndClassExW 是 WNDCLASSEXW（字段与 winuser.h 对应）。
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

// winMsg 是 MSG。
type winMsg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// windowSingleton 是一个具名窗口类哨兵窗口（每进程至多一个，类名唯一标识 (user, workDir)）。
type windowSingleton struct {
	class string
	hwnd  uintptr

	mu    sync.Mutex
	onTop func() // 收到 wmAppBringToTop 时执行（= 置顶本进程主窗口）
}

// setBringToTop 接线「置顶主窗口」回调（主窗口创建完成后设置；跨线程调用安全）。
func (s *windowSingleton) setBringToTop(fn func()) {
	s.mu.Lock()
	s.onTop = fn
	s.mu.Unlock()
}

// release 关闭哨兵窗口（投 WM_CLOSE 给其线程；跨线程安全、幂等）。
func (s *windowSingleton) release() {
	if s == nil || s.hwnd == 0 {
		return
	}
	_, _, _ = procPostMessageW.Call(s.hwnd, wmCloseWin, 0, 0)
}

// 全局注册的 WndProc 目标（syscall 回调无闭包捕获 → 经 registry 按 hwnd 分发）。
var (
	winSingletonMu     sync.Mutex
	winSingletonByHwnd = map[uintptr]*windowSingleton{}
	winSingletonProc   = windows.NewCallback(winSingletonWndProc)
)

// winSingletonWndProc 是哨兵窗口的窗口过程：置顶消息 → 回调；关闭 → 退出消息循环。
func winSingletonWndProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmAppBringToTop:
		winSingletonMu.Lock()
		s := winSingletonByHwnd[hwnd]
		winSingletonMu.Unlock()
		if s != nil {
			s.mu.Lock()
			fn := s.onTop
			s.mu.Unlock()
			if fn != nil {
				fn()
			}
		}
		return 0
	case wmCloseWin, wmDestroyWin:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
	return ret
}

// findWindowByClass 按类名查找窗口（跨进程；未命中 → 0）。
func findWindowByClass(class string) uintptr {
	p, err := windows.UTF16PtrFromString(class)
	if err != nil {
		return 0
	}
	h, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(p)), 0)
	return h
}

// bringWindowToTopByClass 查找具名类的既有窗口并请求其置顶；命中 → true。
func bringWindowToTopByClass(class string) bool {
	h := findWindowByClass(class)
	if h == 0 {
		return false
	}
	_, _, _ = procPostMessageW.Call(h, wmAppBringToTop, 0, 0)
	return true
}

// acquireWindowSingleton 获取「具名窗口类」窗口单例：
//   - 已存在同用户同项目窗口 → (nil, true, nil)（已请求既有窗口置顶，调用方应退出）；
//   - 否则注册类 + 建隐藏哨兵窗口 → (singleton, false, nil)；
//   - 建窗失败 → (nil, false, err)（调用方仅告警，不阻断启动）。
func acquireWindowSingleton(class string) (*windowSingleton, bool, error) {
	if bringWindowToTopByClass(class) {
		return nil, true, nil
	}
	s := &windowSingleton{class: class}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := s.run(ready); err != nil {
			// run 已将错误投递到 ready（此处仅保证线程收尾）
			_ = err
		}
	}()
	if err := <-ready; err != nil {
		return nil, false, err
	}
	return s, false, nil
}

// run 在锁定线程内：注册窗口类 → 建隐藏窗口 → 跑消息循环（直到 release）。
// 恰好向 ready 投递一次（nil = 就绪）。
func (s *windowSingleton) run(ready chan<- error) error {
	hInst, _, _ := procGetModuleHandleW.Call(0)
	className, err := windows.UTF16PtrFromString(s.class)
	if err != nil {
		ready <- err
		return err
	}
	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		Style:         csWinSingletonDblClks,
		LpfnWndProc:   winSingletonProc,
		HInstance:     hInst,
		LpszClassName: className,
	}
	if ret, _, e := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); ret == 0 {
		err := fmt.Errorf("RegisterClassExW(%s): %v", s.class, e)
		ready <- err
		return err
	}
	hwnd, _, e := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, 0, hInst, 0,
	)
	if hwnd == 0 {
		err := fmt.Errorf("CreateWindowExW(%s): %v", s.class, e)
		ready <- err
		return err
	}
	s.hwnd = hwnd
	winSingletonMu.Lock()
	winSingletonByHwnd[hwnd] = s
	winSingletonMu.Unlock()
	ready <- nil

	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || r == ^uintptr(0) { // WM_QUIT 或错误
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	procDestroyWindowWin.Call(hwnd)
	winSingletonMu.Lock()
	delete(winSingletonByHwnd, hwnd)
	winSingletonMu.Unlock()
	return nil
}
