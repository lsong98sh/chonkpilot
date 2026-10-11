// 文件日志落点（按大小滚动）—— 统一日志出口的第三个落点（console / Windows 事件日志 / 文件）。
//
// 场景：以 `-H windowsgui` 构建的常驻进程（如 `chonkpilot-server.exe`）无控制台，stdout/stderr
// 不可见 → 日志须**落文件**方可诊断。本文件提供按大小滚动的文件写入器（实现 io.Writer），
// 可直接 `log.SetOutput` 接管标准库 log，或作为额外 sink 注入内嵌库（`server.Options.LogWriter`）。
// 口径与 `chonkpilot-gui/logfile.go` 的滚动 sink 一致（单文件 2 MiB / 保留 5 份）。
package winlog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// DefaultMaxBytes 是单文件字节上限（2 MiB）；DefaultMaxFiles 是滚动备份保留份数。
	DefaultMaxBytes int64 = 2 << 20
	DefaultMaxFiles int   = 5

	// rotateBackoff 是滚动失败后的退避时长：期间跳过滚动，避免每条日志都重试整轮
	// rename（文件被占用等场景下 rename 会持续失败）。退避到期后再试一次。
	rotateBackoff = 30 * time.Second
)

// FileWriter 是按大小滚动的文件写入器（并发安全，实现 io.Writer）。
// 写满 maxBytes 时把 <path> → <path>.1（依次后移，最旧的 <path>.<maxFiles> 丢弃），
// 保证单文件有界、历史保留 maxFiles 份。
type FileWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxFiles int
	f        *os.File
	size     int64
	// rotateBackoffUntil：上次滚动**未生效**（如 rename 失败降级重开）后的退避截止时刻；
	// 在此之前的 Write 跳过滚动尝试（R7-B-9 降级路径的补充：避免每条日志重试整轮 rename）。
	rotateBackoffUntil time.Time
}

// NewFileWriter 打开（或新建）path 处的滚动日志文件，必要时创建其父目录；
// maxBytes / maxFiles <=0 时按 DefaultMaxBytes / DefaultMaxFiles 取值。
// 文件以追加方式打开，size 取现有大小（重启后接着滚动）。
func NewFileWriter(path string, maxBytes int64, maxFiles int) (*FileWriter, error) {
	if path == "" {
		return nil, errors.New("winlog: empty log path")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &FileWriter{path: path, maxBytes: maxBytes, maxFiles: maxFiles, f: f, size: st.Size()}, nil
}

// Write 写入一条日志；写满 maxBytes 时先滚动（size>0 才滚，避免单条超限时无限滚动）。
func (w *FileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, errors.New("winlog: file writer closed")
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		// 退避窗口内跳过一次滚动尝试（滚动上次未生效时，rename 大概率仍失败）。
		if !time.Now().Before(w.rotateBackoffUntil) {
			rotated, err := w.rotate()
			if err != nil {
				return 0, err
			}
			if rotated {
				w.rotateBackoffUntil = time.Time{}
			} else {
				// 滚动未生效（已降级重开继续写）→ 退避，避免每条日志重试整轮 rename。
				w.rotateBackoffUntil = time.Now().Add(rotateBackoff)
			}
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate 关闭当前文件并后移备份（**须持锁**）：删最旧 → .(n-1)→.n … → path→path.1 → 重开。
// 返回 rotated=false 表示滚动**未生效**（如 path→path.1 rename 失败，已降级重开原文件，
// 调用方应退避）；返回 err 非 nil 表示文件已不可用（重开亦失败）。
func (w *FileWriter) rotate() (rotated bool, err error) {
	_ = w.f.Close()
	_ = os.Remove(fmt.Sprintf("%s.%d", w.path, w.maxFiles))
	for i := w.maxFiles - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		// R7-B-9：滚动失败（如文件被其它进程占用致 Rename 失败）→ 降级重开原文件继续写
		//（O_APPEND、size 重取），避免 w.f 已被 Close 后 Write 恒错、日志永久停摆。
		if rerr := w.reopenAppend(); rerr != nil {
			return false, rerr
		}
		return false, nil
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return false, err
	}
	w.f = f
	w.size = 0
	return true, nil
}

// reopenAppend 以追加方式重开原文件并重取 size（滚动失败时的降级路径）。
func (w *FileWriter) reopenAppend() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	w.f = f
	if st, serr := f.Stat(); serr == nil {
		w.size = st.Size()
	}
	return nil
}

// Close 关闭底层文件（幂等；关闭后 Write 返回错误）。
func (w *FileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// Path 返回日志文件路径。
func (w *FileWriter) Path() string { return w.path }

var _ io.Writer = (*FileWriter)(nil)
