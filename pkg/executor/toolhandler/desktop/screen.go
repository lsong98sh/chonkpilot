package desktop

import (
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

var desktopMutex sync.Mutex

// BITMAPINFOHEADER is the standard 40-byte bitmap info header.
type BITMAPINFOHEADER struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

// captureRectDIB captures a screenshot using CreateDIBSection + BitBlt.
// This avoids the unreliable GetDIBits API entirely.
func captureRectDIB(srcDC uintptr, r *Rect) ([]byte, error) {
	img, err := captureRectToImage(srcDC, r)
	if err != nil {
		return nil, err
	}

	var buf strings.Builder
	b64w := base64.NewEncoder(base64.StdEncoding, &buf)
	err = png.Encode(b64w, img)
	b64w.Close()
	if err != nil {
		return nil, fmt.Errorf("PNG encode failed: %s", err.Error())
	}

	return []byte("data:image/png;base64," + buf.String()), nil
}

// captureRectToImage captures pixels from srcDC into *image.RGBA (no encoding).
func captureRectToImage(srcDC uintptr, r *Rect) (*image.RGBA, error) {
	width := r.Right - r.Left
	height := r.Bottom - r.Top
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid capture rect: %dx%d", width, height)
	}

	memDC, _, _ := CreateCompatibleDC.Call(srcDC)
	if memDC == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer DeleteDC.Call(memDC)

	bmiSize := 40 + 1024
	bmi := make([]byte, bmiSize)
	header := (*BITMAPINFOHEADER)(unsafe.Pointer(&bmi[0]))
	header.BiSize = 40
	header.BiWidth = int32(width)
	header.BiHeight = -int32(height) // top-down
	header.BiPlanes = 1
	header.BiBitCount = 32
	header.BiCompression = 0 // BI_RGB

	var bitsPtr uintptr
	hbitmap, _, _ := CreateDIBSection.Call(
		memDC,
		uintptr(unsafe.Pointer(&bmi[0])),
		DibRgbColors,
		uintptr(unsafe.Pointer(&bitsPtr)),
		0, 0)
	if hbitmap == 0 {
		return nil, fmt.Errorf("CreateDIBSection failed")
	}
	defer DeleteObject.Call(hbitmap)

	SelectObject.Call(memDC, hbitmap)

	ret, _, _ := BitBlt.Call(memDC, 0, 0, uintptr(width), uintptr(height),
		srcDC, uintptr(r.Left), uintptr(r.Top), SrcCopy)
	if ret == 0 {
		return nil, fmt.Errorf("BitBlt failed")
	}

	// Read pixels from the DIB section's memory (BGRA, 4 bytes per pixel)
	pixels := make([]byte, width*height*4)
	src := (*[1 << 30]byte)(unsafe.Pointer(bitsPtr))[: width*height*4 : width*height*4]
	copy(pixels, src)

	// Convert BGRA → RGBA
	img := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := 0; y < int(height); y++ {
		for x := 0; x < int(width); x++ {
			srcIdx := (y*int(width) + x) * 4
			dstIdx := (y*int(width) + x) * 4
			img.Pix[dstIdx] = pixels[srcIdx+2]   // R
			img.Pix[dstIdx+1] = pixels[srcIdx+1] // G
			img.Pix[dstIdx+2] = pixels[srcIdx]   // B
			img.Pix[dstIdx+3] = 255              // A
		}
	}

	return img, nil
}

// CaptureRect captures a screenshot of the given rectangle.
// Uses GetDC(GetDesktopWindow()) as primary DC source.
func CaptureRect(r *Rect, hwnd uintptr) ([]byte, error) {
	width := r.Right - r.Left
	height := r.Bottom - r.Top
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid capture rect: %dx%d", width, height)
	}

	// Method 1 (primary): GetDC(GetDesktopWindow()) — most compatible
	hwndDesktop, _, _ := GetDesktopWindow.Call()
	dc, _, _ := GetDC.Call(hwndDesktop)
	if dc != 0 {
		data, err := captureRectDIB(dc, r)
		ReleaseDC.Call(hwndDesktop, dc)
		if err == nil {
			return data, nil
		}
		primaryErr := err.Error()

		// Method 2 (hwnd available): try PrintWindow for window-specific capture
		if hwnd != 0 {
			hwndDesktop2, _, _ := GetDesktopWindow.Call()
			dc2, _, _ := GetDC.Call(hwndDesktop2)
			if dc2 != 0 {
				memDC, _, _ := CreateCompatibleDC.Call(dc2)
				if memDC != 0 {
					bmiSize := 40 + 1024
					bmi := make([]byte, bmiSize)
					header := (*BITMAPINFOHEADER)(unsafe.Pointer(&bmi[0]))
					header.BiSize = 40
					header.BiWidth = int32(width)
					header.BiHeight = -int32(height)
					header.BiPlanes = 1
					header.BiBitCount = 32
					header.BiCompression = 0
					var bitsPtr uintptr
					hbitmap, _, _ := CreateDIBSection.Call(memDC, uintptr(unsafe.Pointer(&bmi[0])), DibRgbColors,
						uintptr(unsafe.Pointer(&bitsPtr)), 0, 0)
					if hbitmap != 0 {
						SelectObject.Call(memDC, hbitmap)
						pwRet, _, _ := PrintWindow.Call(hwnd, memDC, PwRenderFullContent)
						if pwRet != 0 {
							pixels := make([]byte, width*height*4)
							src := (*[1 << 30]byte)(unsafe.Pointer(bitsPtr))[: width*height*4 : width*height*4]
							copy(pixels, src)
							DeleteObject.Call(hbitmap)
							DeleteDC.Call(memDC)
							ReleaseDC.Call(hwndDesktop2, dc2)

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
							_ = png.Encode(b64w, img)
							b64w.Close()
							return []byte("data:image/png;base64," + buf.String()), nil
						}
						DeleteObject.Call(hbitmap)
					}
					DeleteDC.Call(memDC)
				}
				ReleaseDC.Call(hwndDesktop2, dc2)
			}
		}

		// Method 3 (fallback): CreateDCW("DISPLAY") — works in some environments
		dc3, _, _ := CreateDCW.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("DISPLAY"))), 0, 0, 0)
		if dc3 != 0 {
			data3, err3 := captureRectDIB(dc3, r)
			DeleteDC.Call(dc3)
			if err3 == nil {
				return data3, nil
			}
			return nil, fmt.Errorf("all capture methods failed: GetDC: %s; CreateDCW: %s", primaryErr, err3.Error())
		}
		return nil, fmt.Errorf("capture failed: %s (CreateDCW also failed)", primaryErr)
	}

	// Fallback when GetDC fails entirely: try CreateDCW directly
	dc4, _, _ := CreateDCW.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("DISPLAY"))), 0, 0, 0)
	if dc4 == 0 {
		return nil, fmt.Errorf("GetDC and CreateDCW both failed")
	}
	data, err := captureRectDIB(dc4, r)
	DeleteDC.Call(dc4)
	return data, err
}

// GetFullScreenRect returns the bounding rect of the primary monitor.
func GetFullScreenRect() *Rect {
	w := int32(GetSystemMetrics(0))
	h := int32(GetSystemMetrics(1))
	return &Rect{Left: 0, Top: 0, Right: w, Bottom: h}
}

// captureImage captures a rect to *image.RGBA with fallback logic.
func captureImage(r *Rect, hwnd uintptr) (*image.RGBA, error) {
	width := r.Right - r.Left
	height := r.Bottom - r.Top
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid capture rect: %dx%d", width, height)
	}

	// Method 1 (primary): GetDC(GetDesktopWindow()) — most compatible
	hwndDesktop, _, _ := GetDesktopWindow.Call()
	dc, _, _ := GetDC.Call(hwndDesktop)
	if dc != 0 {
		img, err := captureRectToImage(dc, r)
		ReleaseDC.Call(hwndDesktop, dc)
		if err == nil {
			return img, nil
		}
		primaryErr := err.Error()

		// Method 2 (hwnd available): try PrintWindow for window-specific capture
		if hwnd != 0 {
			hwndDesktop2, _, _ := GetDesktopWindow.Call()
			dc2, _, _ := GetDC.Call(hwndDesktop2)
			if dc2 != 0 {
				memDC, _, _ := CreateCompatibleDC.Call(dc2)
				if memDC != 0 {
					bmiSize := 40 + 1024
					bmi := make([]byte, bmiSize)
					header := (*BITMAPINFOHEADER)(unsafe.Pointer(&bmi[0]))
					header.BiSize = 40
					header.BiWidth = int32(width)
					header.BiHeight = -int32(height)
					header.BiPlanes = 1
					header.BiBitCount = 32
					header.BiCompression = 0
					var bitsPtr uintptr
					hbitmap, _, _ := CreateDIBSection.Call(memDC, uintptr(unsafe.Pointer(&bmi[0])), DibRgbColors,
						uintptr(unsafe.Pointer(&bitsPtr)), 0, 0)
					if hbitmap != 0 {
						SelectObject.Call(memDC, hbitmap)
						pwRet, _, _ := PrintWindow.Call(hwnd, memDC, PwRenderFullContent)
						if pwRet != 0 {
							pixels := make([]byte, width*height*4)
							src := (*[1 << 30]byte)(unsafe.Pointer(bitsPtr))[: width*height*4 : width*height*4]
							copy(pixels, src)
							DeleteObject.Call(hbitmap)
							DeleteDC.Call(memDC)
							ReleaseDC.Call(hwndDesktop2, dc2)

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
							return img, nil
						}
						DeleteObject.Call(hbitmap)
					}
					DeleteDC.Call(memDC)
				}
				ReleaseDC.Call(hwndDesktop2, dc2)
			}
		}

		// Method 3 (fallback): CreateDCW("DISPLAY") — works in some environments
		dc3, _, _ := CreateDCW.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("DISPLAY"))), 0, 0, 0)
		if dc3 != 0 {
			img3, err3 := captureRectToImage(dc3, r)
			DeleteDC.Call(dc3)
			if err3 == nil {
				return img3, nil
			}
			return nil, fmt.Errorf("all capture methods failed: GetDC: %s; CreateDCW: %s", primaryErr, err3.Error())
		}
		return nil, fmt.Errorf("capture failed: %s (CreateDCW also failed)", primaryErr)
	}

	// Fallback when GetDC fails entirely: try CreateDCW directly
	dc4, _, _ := CreateDCW.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("DISPLAY"))), 0, 0, 0)
	if dc4 == 0 {
		return nil, fmt.Errorf("GetDC and CreateDCW both failed")
	}
	img, err := captureRectToImage(dc4, r)
	DeleteDC.Call(dc4)
	return img, err
}

// scaleImage scales an image by the given factor using nearest-neighbor interpolation.
func scaleImage(img *image.RGBA, scale float64) *image.RGBA {
	bounds := img.Bounds()
	newWidth := int(float64(bounds.Dx()) * scale)
	newHeight := int(float64(bounds.Dy()) * scale)
	if newWidth <= 0 || newHeight <= 0 || newWidth == bounds.Dx() && newHeight == bounds.Dy() {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	for y := 0; y < newHeight; y++ {
		for x := 0; x < newWidth; x++ {
			srcX := int(float64(x) / scale)
			srcY := int(float64(y) / scale)
			if srcX >= bounds.Dx() {
				srcX = bounds.Dx() - 1
			}
			if srcY >= bounds.Dy() {
				srcY = bounds.Dy() - 1
			}
			srcIdx := (srcY*bounds.Dx() + srcX) * 4
			dstIdx := (y*newWidth + x) * 4
			dst.Pix[dstIdx] = img.Pix[srcIdx]
			dst.Pix[dstIdx+1] = img.Pix[srcIdx+1]
			dst.Pix[dstIdx+2] = img.Pix[srcIdx+2]
			dst.Pix[dstIdx+3] = img.Pix[srcIdx+3]
		}
	}
	return dst
}

// annotateImage draws overlay annotations on an image.
//   - true or "grid": draws grid lines every 100px
//   - "crosshair": draws crosshair at the center
func annotateImage(img *image.RGBA, annotate interface{}) *image.RGBA {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	if w <= 0 || h <= 0 {
		return img
	}

	gridColor := color.RGBA{R: 0, G: 255, B: 255, A: 180}   // cyan
	crossColor := color.RGBA{R: 255, G: 0, B: 0, A: 200}            // red

	switch v := annotate.(type) {
	case bool:
		if !v {
			return img
		}
		// Grid lines every 100px
		drawGrid(img, w, h, 100, gridColor)
	case string:
		if v == "grid" {
			drawGrid(img, w, h, 100, gridColor)
		} else if v == "crosshair" {
			drawCrosshair(img, w, h, crossColor)
		}
	}

	return img
}

// drawGrid draws grid lines at the given interval on the image.
func drawGrid(img *image.RGBA, w, h, interval int, c color.RGBA) {
	// Vertical lines
	for x := interval; x < w; x += interval {
		for y := 0; y < h; y++ {
			idx := (y*w + x) * 4
			img.Pix[idx] = c.R
			img.Pix[idx+1] = c.G
			img.Pix[idx+2] = c.B
			img.Pix[idx+3] = c.A
		}
	}
	// Horizontal lines
	for y := interval; y < h; y += interval {
		for x := 0; x < w; x++ {
			idx := (y*w + x) * 4
			img.Pix[idx] = c.R
			img.Pix[idx+1] = c.G
			img.Pix[idx+2] = c.B
			img.Pix[idx+3] = c.A
		}
	}
}

// drawCrosshair draws crosshair lines at the center of the image.
func drawCrosshair(img *image.RGBA, w, h int, c color.RGBA) {
	cx := w / 2
	cy := h / 2

	// Horizontal line
	for x := 0; x < w; x++ {
		idx := (cy*w + x) * 4
		img.Pix[idx] = c.R
		img.Pix[idx+1] = c.G
		img.Pix[idx+2] = c.B
		img.Pix[idx+3] = c.A
	}
	// Vertical line
	for y := 0; y < h; y++ {
		idx := (y*w + cx) * 4
		img.Pix[idx] = c.R
		img.Pix[idx+1] = c.G
		img.Pix[idx+2] = c.B
		img.Pix[idx+3] = c.A
	}
}

// HandleScreenshot captures a screenshot with optional region cropping, scaling,
// annotation overlay, and output format control.
func HandleScreenshot(args map[string]interface{}) *types.ToolResult {
	desktopMutex.Lock()
	defer desktopMutex.Unlock()

	hwndVal, hasHWND := args["hwnd"].(float64)
	winTitle, hasTitle := args["window"].(string)

	// 1. Determine target area (full screen or window)
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
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("window not found: %s", err.Error()), Output: fmt.Sprintf("❌ 截图失败：%s", err.Error()), Tool: "screenshot"}
		}
		hwnd = uintptr(wnd)
		var wr Rect
		GetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
		r = &wr
	} else {
		r = GetFullScreenRect()
	}

	// 2. Region cropping (x/y/width/height relative to capture rect)
	if cropWidth, ok := getFloatArg(args, "width"); ok && cropWidth > 0 {
		cropHeight, _ := getFloatArg(args, "height")
		x, _ := getFloatArg(args, "x")
		y, _ := getFloatArg(args, "y")
		if cropHeight > 0 {
			r.Left += int32(x)
			r.Top += int32(y)
			r.Right = r.Left + int32(cropWidth)
			r.Bottom = r.Top + int32(cropHeight)
		}
	}

	// 3. Capture raw image
	img, err := captureImage(r, hwnd)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("screenshot failed: %s", err.Error()), Output: fmt.Sprintf("❌ 截图失败：%s", err.Error()), Tool: "screenshot"}
	}

	// 4. Scaling
	if scale, ok := getFloatArg(args, "scale"); ok && scale > 0 && scale != 1.0 {
		img = scaleImage(img, scale)
	}

	// 5. Annotation overlay
	if annotateVal, ok := args["annotate"]; ok {
		img = annotateImage(img, annotateVal)
	}

	// 6. Output format
	format := "png"
	if f, ok := args["format"].(string); ok && f != "" {
		format = f
	}
	quality := 90
	if q, ok := getFloatArg(args, "quality"); ok {
		quality = int(q)
	}

	var buf strings.Builder
	b64w := base64.NewEncoder(base64.StdEncoding, &buf)

	if format == "jpeg" {
		err = jpeg.Encode(b64w, img, &jpeg.Options{Quality: quality})
	} else {
		err = png.Encode(b64w, img)
	}
	b64w.Close()
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("image encode failed: %s", err.Error()), Output: fmt.Sprintf("❌ 截图失败：%s", err.Error()), Tool: "screenshot"}
	}

	mimeType := "image/png"
	if format == "jpeg" {
		mimeType = "image/jpeg"
	}

	b64Data := "data:" + mimeType + ";base64," + buf.String()

	return &types.ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("📸 截图完成（%dx%d，%s，%d bytes）", img.Bounds().Dx(), img.Bounds().Dy(), format, buf.Len()),
		Tool:      "screenshot",
		RawResult: map[string]interface{}{"image_base64": b64Data, "width": img.Bounds().Dx(), "height": img.Bounds().Dy(), "format": format, "size_bytes": buf.Len()},
	}
}

// getFloatArg reads a float64 argument from args, supporting both float64 (JSON number)
// and int-like conversions.
func getFloatArg(args map[string]interface{}, key string) (float64, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	default:
		return 0, false
	}
}

func init() {
	types.RegisterSimplify("screenshot", types.SimpleAction("screenshot"))
}
