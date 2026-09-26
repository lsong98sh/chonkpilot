# Go WebView2 去标题栏并保持拖动/缩放

[meta]
uri=mcp://chonkpilot/resources/go/webview2-frameless
mimetype=text/markdown

[description]
Go + WebView2 无边框窗口实现（去 WS_CAPTION 保 move/resize）：窗口样式位操作 + WM_NCHITTEST 子类化，含完整 Go 代码、八方向边缘热区、拖动区与常见坑。开发桌面工具/自绘标题栏窗口时使用。

[content]
# Go + WebView2 无边框窗口（去 caption 保 move/resize）

## 目标

去掉原生标题栏（无 caption），但**保留**：
- 窗口拖动（move）——按住自绘标题区/空白区可移动
- 八方向缩放（resize）——边缘/角落拖拽改变尺寸

## 核心思路

- 窗口样式：**去掉 `WS_CAPTION`，保留 `WS_THICKFRAME`（= `WS_SIZEBOX`）+ `WS_MINIMIZEBOX` + `WS_MAXIMIZEBOX`**
- 系统按样式自动提供：resize 边框、最大化/最小化（任务栏行为）、双击（若保留 `WS_SYSMENU` 双击 title 区最小化/最大化）
- **move 与边缘 resize 都不再自动命中**（无 caption → 无系统拖动区；无边框 → 无系统边缘热区）→ 用 `WM_NCHITTEST` 自绘命中：
  - 自绘标题区/整窗空白 → 返回 `HTCAPTION`，系统接管拖动（无闪烁，最省事）
  - 边缘 4px 热区 → 返回 `HTLEFT/HTRIGHT/HTTOP/HTBOTTOM/HTTOPLEFT/...`，系统接管八方向缩放
  - 其余 → `HTCLIENT`

## 实现（Go + x/sys/windows）

```go
package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

const (
	gwlStyle      = -16
	gwlpWndProc   = -4
	wsCaption     = 0x00C00000
	wsThickFrame  = 0x00040000 // = WS_SIZEBOX，保留以支持系统 resize
	wsMinimizeBox = 0x00020000
	wsMaximizeBox = 0x00010000

	htClient    = 1
	htCaption   = 2
	htLeft      = 10
	htRight     = 11
	htTop       = 12
	htTopLeft   = 13
	htTopRight  = 14
	htBottom    = 15
	htBottomLft = 16
	htBottomRgt = 17
)

var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	pGetWindowLongPtr        = user32.NewProc("GetWindowLongPtrW")
	pSetWindowLongPtr        = user32.NewProc("SetWindowLongPtrW")
	pSetWindowPos            = user32.NewProc("SetWindowPos")
	pCallWindowProc          = user32.NewProc("CallWindowProcW")
)

func getWindowLongPtr(hwnd uintptr, idx int32) uintptr {
	r, _, _ := pGetWindowLongPtr.Call(hwnd, uintptr(idx))
	return r
}

func setWindowLongPtr(hwnd uintptr, idx int32, val uintptr) uintptr {
	r, _, _ := pSetWindowLongPtr.Call(hwnd, uintptr(idx), val)
	return r
}

// wndProc 子类化：接管 WM_NCHITTEST 提供拖动区与边缘热区。
type frame struct {
	origWndProc uintptr
}

func (f *frame) wndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	const wmNcHitTest = 0x0084
	if msg == wmNcHitTest {
		if ht := f.hitTest(lParam); ht != htClient {
			return ht // 返回 HTCAPTION / HT* → 系统接管 move/resize
		}
	}
	return pCallWindowProc.Call(f.origWndProc, hwnd, msg, wParam, lParam)[0]
}

// hitTest 按坐标返回命中区：顶部 32px 拖动，边缘 4px 缩放。
func (f *frame) hitTest(lParam uintptr) uintptr {
	x := int32(lParam & 0xFFFF)
	y := int32(lParam >> 16)
	// 需要当前窗口 rect（GetWindowRect），此处示意：
	//   edge := int32(4); title := int32(32)
	//   上下左右边界判定 → 角落优先（HTTOPLEFT 等）
	return htClient // 占位：完整实现见下
}

func main() {
	w := webview2.New(false)
	defer w.Destroy()
	w.SetTitle("Frameless")
	w.SetSize(1200, 800, webview2.HintNone)
	w.Navigate("data:text/html,<body style='margin:0'><div style='height:32px;background:#333' id='title'>drag me</div></body>")
	w.Run()
	// 真实场景在 Run 前先取 hwnd（WM_CREATE 后 SetWindowLongPtr 子类化 + 去 caption）
}
```

## 去 caption（拿到 hwnd 后）

```go
style := getWindowLongPtr(hwnd, gwlStyle)
style &^= uintptr(wsCaption) // 去掉标题栏
style |= uintptr(wsThickFrame | wsMinimizeBox | wsMaximizeBox) // 保留缩放/最小最大
setWindowLongPtr(hwnd, gwlStyle, style)
// 立即生效重绘
pSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, 0x0002|0x0004|0x0020) // SWP_NOMOVE|SWP_NOSIZE|SWP_FRAMECHANGED
```

## 完整 hitTest

```go
func (f *frame) hitTest(hwnd, lParam uintptr) uintptr {
	pt := windows.POINT{X: int32(lParam & 0xFFFF), Y: int32(lParam >> 16)}
	var r windows.RECT
	windows.GetWindowRect(windows.HWND(hwnd), &r)
	x, y := int32(pt.X), int32(pt.Y)
	w, h := r.Right-r.Left, r.Bottom-r.Top
	const edge = int32(4)
	const title = int32(32) // 自绘标题区高度
	top := y-r.Top <= edge
	bottom := r.Bottom-y <= edge
	left := x-r.Left <= edge
	right := r.Right-x <= edge
	switch {
	case top && left:
		return htTopLeft
	case top && right:
		return htTopRight
	case bottom && left:
		return htBottomLft
	case bottom && right:
		return htBottomRgt
	case top:
		return htTop
	case bottom:
		return htBottom
	case left:
		return htLeft
	case right:
		return htRight
	case y-r.Top <= title: // 自绘标题区 → 拖动
		return htCaption
	}
	return htClient
}
```

## 要点与坑

1. **`WM_NCHITTEST` 的 lParam 是屏幕坐标**（不是客户区），必须用 `GetWindowRect` 换算
2. 子类化（`GWLP_WNDPROC`）必须保存原 `WndProc` 并转发所有未处理消息，否则窗口行为损坏
3. 去 caption 后**最大化时边框会溢出屏幕**——需处理 `WM_GETMINMAXINFO` 留出 8px（`ptMaxTrackSize` 调整）
4. WebView2 的键盘焦点/IME 不受影响（消息仍转发）
5. `jchv/go-webview2` 的 `Run()` 是阻塞的；取 `hwnd` 用 `w.Window()`（若提供）或 `WM_CREATE`/`MainWindowHandle` 回调
6. DPI 感知：`SetProcessDpiAwareness` 后坐标才是物理像素，否则缩放按虚拟像素算
7. 可选：不整窗拖动，只自绘 32px 标题条为拖动区（命中 `HTCAPTION`），内容区正常响应点击

## 替代方案

- 若只是"隐藏标题栏但保留系统边框拖动"：WebView2 controller 无原生开关，仍走 `WS_CAPTION` 位操作（同上）
- 纯前端模拟 move（mousedown + `SetWindowPos` 手动搬）：闪烁、慢，不推荐；`HTCAPTION` 系统拖动无闪烁
