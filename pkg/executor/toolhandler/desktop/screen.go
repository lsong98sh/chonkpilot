package desktop

import (
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

var desktopMutex sync.Mutex

// CaptureRect captures a screenshot of the given rectangle.
func CaptureRect(r *Rect, hwnd uintptr) ([]byte, error) {
	width := r.Right - r.Left
	height := r.Bottom - r.Top
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid capture rect: %dx%d", width, height)
	}

	dc, _, _ := CreateDCW.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("DISPLAY"))), 0, 0, 0)
	if dc == 0 {
		return nil, fmt.Errorf("CreateDCW failed")
	}
	defer DeleteDC.Call(dc)

	memDC, _, _ := CreateCompatibleDC.Call(dc)
	if memDC == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer DeleteDC.Call(memDC)

	hbitmap, _, _ := CreateCompatibleBitmap.Call(dc, uintptr(width), uintptr(height))
	if hbitmap == 0 {
		return nil, fmt.Errorf("CreateCompatibleBitmap failed")
	}
	defer DeleteObject.Call(hbitmap)

	SelectObject.Call(memDC, hbitmap)

	ret, _, _ := BitBlt.Call(memDC, 0, 0, uintptr(width), uintptr(height),
		dc, uintptr(r.Left), uintptr(r.Top), SrcCopy)
	if ret == 0 {
		return nil, fmt.Errorf("BitBlt failed")
	}

	bmiHeaderLen := uint32(40 + 1024)
	bmi := make([]byte, bmiHeaderLen)
	*(*uint32)(unsafe.Pointer(&bmi[0])) = 40
	*(*int32)(unsafe.Pointer(&bmi[4])) = width
	*(*int32)(unsafe.Pointer(&bmi[8])) = -height
	*(*uint16)(unsafe.Pointer(&bmi[12])) = 1
	*(*uint16)(unsafe.Pointer(&bmi[14])) = 32
	*(*uint32)(unsafe.Pointer(&bmi[16])) = 0

	pixels := make([]byte, width*height*4)
	ret, _, _ = GetDIBits.Call(memDC, hbitmap, 0, uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&bmi[0])),
		DibRgbColors)
	if ret == 0 {
		if hwnd != 0 {
			BitBlt.Call(memDC, 0, 0, uintptr(width), uintptr(height),
				dc, uintptr(r.Left), uintptr(r.Top), 0x00FF0062)
			ret, _, _ = PrintWindow.Call(hwnd, memDC, PwRenderFullContent)
			if ret == 0 {
				return nil, fmt.Errorf("PrintWindow also failed after GetDIBits failed")
			}
			ret, _, _ = GetDIBits.Call(memDC, hbitmap, 0, uintptr(height),
				uintptr(unsafe.Pointer(&pixels[0])),
				uintptr(unsafe.Pointer(&bmi[0])),
				DibRgbColors)
			if ret == 0 {
				return nil, fmt.Errorf("GetDIBits failed after PrintWindow fallback")
			}
		} else {
			return nil, fmt.Errorf("GetDIBits failed")
		}
	}

	img := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := 0; y < int(height); y++ {
		for x := 0; x < int(width); x++ {
			srcIdx := (y*int(width) + x) * 4
			dstIdx := (y*int(width) + x) * 4
			img.Pix[dstIdx] = pixels[srcIdx+2]
			img.Pix[dstIdx+1] = pixels[srcIdx+1]
			img.Pix[dstIdx+2] = pixels[srcIdx]
			img.Pix[dstIdx+3] = 255
		}
	}

	var buf strings.Builder
	b64w := base64.NewEncoder(base64.StdEncoding, &buf)
	err := png.Encode(b64w, img)
	b64w.Close()
	if err != nil {
		return nil, fmt.Errorf("PNG encode failed: %s", err.Error())
	}

	return []byte("data:image/png;base64," + buf.String()), nil
}

// GetFullScreenRect returns the bounding rect of the primary monitor.
func GetFullScreenRect() *Rect {
	w := int32(GetSystemMetrics(0))
	h := int32(GetSystemMetrics(1))
	return &Rect{Left: 0, Top: 0, Right: w, Bottom: h}
}

// HandleScreenshot captures a screenshot.
func HandleScreenshot(args map[string]interface{}) *types.ToolResult {
	desktopMutex.Lock()
	defer desktopMutex.Unlock()

	hwndVal, hasHWND := args["hwnd"].(float64)
	winTitle, hasTitle := args["window"].(string)

	var r *Rect
	var hwnd uintptr
	if hasHWND && hwndVal > 0 {
		hwndValInt := int32(hwndVal)
		hwnd = uintptr(hwndValInt)
		var wr Rect
		GetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
		r = &wr
	} else if hasTitle && winTitle != "" {
		wnd, err := FindWindowByTitle(winTitle)
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("window not found: %s", err.Error()), Tool: "screenshot"}
		}
		hwnd = uintptr(wnd)
		var wr Rect
		GetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
		r = &wr
	} else {
		r = GetFullScreenRect()
	}

	data, err := CaptureRect(r, hwnd)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("screenshot failed: %s", err.Error()), Tool: "screenshot"}
	}

	return &types.ToolResult{
		Success: true,
		Output:  string(data),
		Tool:    "screenshot",
	}
}

func init() {
	types.RegisterSimplify("screenshot", types.SimpleAction("screenshot"))
}
