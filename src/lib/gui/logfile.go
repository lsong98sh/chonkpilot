// GUI 文件日志（可诊断性补齐，2026-09-19）：
//
// 现状问题：`-H windowsgui` 模式下进程无控制台，`slog` 只写 stderr → **用户/远程排查无日志可看**。
// 本文件给默认 slog logger 增加**文件 sink**（`<prjusr 数据根>/logs/gui.log`，**按大小滚动**、
// 保留 N 份），与既有 stderr 输出**并存**，并沿用同一 `slog.LevelVar`（prj `logLevel` 运行时改级别 → 文件同受控）。
//
// 装配顺序（main.go）：initLogging() 先装（stderr，级别缺省 info）→ prjusr 数据根解析后
// attachFileLog() 挂上文件 sink（此前日志只进 stderr，不丢文件以外的任何东西）。
package gui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// GUI 文件日志参数：单文件上限 2MiB；滚动备份保留 5 份（gui.log.1 … gui.log.5）。
const (
	guiLogFileName = "gui.log"
	guiLogMaxBytes = 2 << 20
	guiLogMaxFiles = 5
)

// dualWriter 是 slog 的输出目标：**始终**写 stderr；attachFileLog 挂上文件后**同时**写文件。
// 单次 Write 一个日志行（TextHandler 每记录一次 Write），加锁保证多 goroutine 下不交错。
type dualWriter struct {
	mu   sync.Mutex
	file io.Writer
}

func (w *dualWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		_, _ = w.file.Write(p) // 文件写失败不吞 stderr：诊断优先
	}
	return os.Stderr.Write(p)
}

// setFile 挂上/替换文件 sink（nil = 仅 stderr）。
func (w *dualWriter) setFile(f io.Writer) {
	w.mu.Lock()
	w.file = f
	w.mu.Unlock()
}

// fileSink 返回当前文件 sink（未挂上 → nil）。装配方（main）用它把**同一个** sink 注入
// 内嵌库（chonkpilot-llm/server 的 `Options.LogWriter`，2026-09-20）——使 server/插件的
// 诊断输出与宿主 slog 日志**同落** `<dataDir>/logs/gui.log`（库侧只收 io.Writer，不依赖 gui 包）。
func (w *dualWriter) fileSink() io.Writer {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file
}

// logWriter 是全局日志输出目标（initLogging 与该文件共用；包级单例）。
var logWriter = &dualWriter{}

// rotateWriter 是按大小滚动的文件写入器：写满 guiLogMaxBytes 时把 gui.log → gui.log.1
// （依次后移，最旧的 gui.log.<maxFiles> 丢弃），保证单文件有界、历史保留 maxFiles 份。
type rotateWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxFiles int
	f        *os.File
	size     int64
}

// newRotateWriter 打开（或新建）日志文件，size 取现有文件大小（重启后接着滚动）。
func newRotateWriter(path string, maxBytes int64, maxFiles int) (*rotateWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &rotateWriter{path: path, maxBytes: maxBytes, maxFiles: maxFiles, f: f, size: st.Size()}, nil
}

func (w *rotateWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, errors.New("rotate writer closed")
	}
	if w.maxBytes > 0 && w.size+int64(len(p)) > w.maxBytes && w.size > 0 {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate 关闭当前文件并后移备份（**须持锁**）：删最旧 → .(n-1)→.n … → gui.log→gui.log.1 → 重开。
func (w *rotateWriter) rotate() error {
	_ = w.f.Close()
	_ = os.Remove(fmt.Sprintf("%s.%d", w.path, w.maxFiles))
	for i := w.maxFiles - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	w.f = f
	w.size = 0
	return nil
}

// Close 关闭底层文件（幂等；测试/退出清理用，避免 Windows 下句柄占用阻止删除）。
func (w *rotateWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// attachFileLog 在 **prjusr 数据根**的 `logs/` 下挂上滚动文件 sink（`<root>/logs/gui.log`；
// root = `data.PrjUsrDir`，见 12-数据层 §3 / 24 §3.2），返回日志**目录**（供前端/用户定位）。
// 失败只返回错误，调用方降级为「仅 stderr」（不阻断启动）。
func attachFileLog(root string) (string, error) {
	if root == "" {
		return "", errors.New("prjusr 数据根为空")
	}
	logDir := filepath.Join(root, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", err
	}
	rw, err := newRotateWriter(filepath.Join(logDir, guiLogFileName), guiLogMaxBytes, guiLogMaxFiles)
	if err != nil {
		return "", err
	}
	logWriter.setFile(rw)
	return logDir, nil
}

// detachFileLog 关闭并摘除文件 sink（幂等；测试/退出清理用，**不改** stderr 输出）。
func detachFileLog() {
	logWriter.mu.Lock()
	defer logWriter.mu.Unlock()
	if rw, ok := logWriter.file.(*rotateWriter); ok {
		_ = rw.Close()
	}
	logWriter.file = nil
}
