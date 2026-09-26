// 截图（FP「截图」按钮）：隐藏本窗口 → GDI 全屏 BitBlt 虚拟桌面 → 恢复窗口 →
// PNG 落盘 prjusr 数据根 tmp/uploads/（复用附件上传目录）→ 返回 {name, path, url}。
// 纯 syscall（gdi32/user32），无 CGO；区域选择 UI 为后续增强，当前全屏。
//
// 2026-09-04：原 /call CaptureScreen 注册已清零；callCaptureScreen 保留为 gui.capture
// 消息面内部实现（guimsg.go）。
package bridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32GDI = syscall.NewLazyDLL("user32.dll")
	gdi32     = syscall.NewLazyDLL("gdi32.dll")

	pGetSystemMetrics   = user32GDI.NewProc("GetSystemMetrics")
	pShowWindow         = user32GDI.NewProc("ShowWindow")
	pGetDC              = user32GDI.NewProc("GetDC")
	pReleaseDC          = user32GDI.NewProc("ReleaseDC")
	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pBitBlt             = gdi32.NewProc("BitBlt")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")

	smXVirtualScreen  = uintptr(76)
	smYVirtualScreen  = uintptr(77)
	smCXVirtualScreen = uintptr(78)
	smCYVirtualScreen = uintptr(79)

	srcCopy = 0x00CC0020
	dibRGB  = uint32(0)
	swHide  = uintptr(0)
	swShow  = uintptr(5)
	biRGB   = uint32(0)
)

// bitmapInfoHeader 与 BITMAPINFOHEADER 内存布局一致（CreateDIBSection 用）。
type bitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32 // 负值 = top-down 行序
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

// callCaptureScreen 全屏截图并落盘；窗口隐藏期间 UI 冻结由调用端（HTTP goroutine）承担。
func callCaptureScreen(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	hwnd := b.hwnd
	if hwnd == 0 {
		return nil, fmt.Errorf("CaptureScreen: no window handle")
	}
	// 隐藏本窗口（截图不含自身）；结束时恢复
	r1, _, _ := pShowWindow.Call(hwnd, swHide)
	_ = r1
	time.Sleep(350 * time.Millisecond)
	defer pShowWindow.Call(hwnd, swShow)

	// 虚拟桌面几何（多显示器合并区域）
	x := int64(systemMetric(smXVirtualScreen))
	y := int64(systemMetric(smYVirtualScreen))
	w := int(systemMetric(smCXVirtualScreen))
	h := int(systemMetric(smCYVirtualScreen))
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("CaptureScreen: empty virtual desktop %dx%d", w, h)
	}

	screenDC, _, _ := pGetDC.Call(0)
	if screenDC == 0 {
		return nil, fmt.Errorf("CaptureScreen: GetDC failed")
	}
	defer pReleaseDC.Call(0, screenDC)

	memDC, _, _ := pCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, fmt.Errorf("CaptureScreen: CreateCompatibleDC failed")
	}
	defer pDeleteDC.Call(memDC)

	// CreateDIBSection：直接申请 32bpp top-down 内存位图（ppvBits 可直接读写，免 GetDIBits）
	bmi := bitmapInfoHeader{
		BiSize: 40, BiWidth: int32(w), BiHeight: -int32(h),
		BiPlanes: 1, BiBitCount: 32, BiCompression: biRGB,
	}
	var bitsPtr unsafe.Pointer
	hbm, _, _ := pCreateDIBSection.Call(screenDC, uintptr(unsafe.Pointer(&bmi)), uintptr(dibRGB), uintptr(unsafe.Pointer(&bitsPtr)), 0, 0)
	if hbm == 0 || bitsPtr == nil {
		return nil, fmt.Errorf("CaptureScreen: CreateDIBSection failed")
	}
	defer pDeleteObject.Call(hbm)

	_, _, _ = pSelectObject.Call(memDC, hbm)
	ok, _, _ := pBitBlt.Call(memDC, 0, 0, uintptr(w), uintptr(h), screenDC, uintptr(x), uintptr(y), uintptr(srcCopy))
	if ok == 0 {
		return nil, fmt.Errorf("CaptureScreen: BitBlt failed")
	}

	// bitsPtr 为 32bpp BGRA（top-down），组装 image.RGBA
	rowBytes := w * 4
	buf := unsafe.Slice((*byte)(bitsPtr), rowBytes*h)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for row := 0; row < h; row++ {
		line := buf[row*rowBytes : (row+1)*rowBytes]
		for col := 0; col < w; col++ {
			p := col * 4
			img.Pix[row*img.Stride+col*4+0] = line[p+2] // R
			img.Pix[row*img.Stride+col*4+1] = line[p+1] // G
			img.Pix[row*img.Stride+col*4+2] = line[p+0] // B
			img.Pix[row*img.Stride+col*4+3] = 0xFF
		}
	}

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return nil, fmt.Errorf("CaptureScreen: png encode: %w", err)
	}

	// 落盘（复用附件上传目录 = prjusr 数据根 tmp/uploads；见 24 §3.2 MW-8），返回 /show/ 预览 URL
	uploadDir := filepath.Join(b.uploadRoot(), "tmp", "uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		return nil, err
	}
	fileID := newUUID() + ".png"
	dest := filepath.Join(uploadDir, fileID)
	if err := os.WriteFile(dest, pngBuf.Bytes(), 0644); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"file_id": fileID,
		"name":    "screenshot.png",
		"path":    dest,
		"url":     b.showURL(dest),
		"size":    len(pngBuf.Bytes()),
		"b64":     base64.StdEncoding.EncodeToString(pngBuf.Bytes()), // 供前端预览/复用
	})
}

func systemMetric(idx uintptr) int32 {
	r, _, _ := pGetSystemMetrics.Call(idx)
	return int32(r)
}
