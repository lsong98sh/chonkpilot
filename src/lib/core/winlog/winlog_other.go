//go:build !windows

// 非 Windows 平台 stub（项目目标平台为 Windows，此文件保证跨平台编译）：
// 无 Windows 事件日志 → 恒写 console 落点。
package winlog

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Writer 实现 io.Writer：非 Windows 恒写 console 落点。
type Writer struct {
	mu  sync.Mutex
	out io.Writer
}

// NewWriter 创建日志 writer（非 Windows：useEventLog 忽略，恒写 out）。
func NewWriter(_ string, _ bool, out io.Writer) *Writer {
	if out == nil {
		out = os.Stderr
	}
	return &Writer{out: out}
}

// Write 写入一条日志行（console 透传）。
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.out.Write(p)
}

// Error 写错误级日志。
func (w *Writer) Error(v ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fmt.Fprintf(w.out, "%s ERROR %s\n", nowPrefix(), fmt.Sprint(v...))
}

// Warn 写警告级日志。
func (w *Writer) Warn(v ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fmt.Fprintf(w.out, "%s WARN %s\n", nowPrefix(), fmt.Sprint(v...))
}

// Info 写信息级日志。
func (w *Writer) Info(v ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fmt.Fprintf(w.out, "%s INFO %s\n", nowPrefix(), fmt.Sprint(v...))
}

// Close 空实现（无事件日志句柄）。
func (w *Writer) Close() error { return nil }
