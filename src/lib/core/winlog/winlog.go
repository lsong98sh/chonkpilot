//go:build windows

// Package winlog 提供统一日志落点：console（stderr）或 Windows 事件日志。
// 实现 io.Writer，可直接 log.SetOutput 全局切换，供服务模式（事件日志）与前台模式（stderr）复用。
package winlog

import (
	"fmt"
	"io"
	"os"
	"sync"
	"unicode/utf8"

	"golang.org/x/sys/windows/svc/eventlog"
)

// maxEventLogMsg 是写入 Windows 事件日志的单条消息字节上限（事件日志上限 31839 字节，留余量）。
const maxEventLogMsg = 30000

// truncateEventMsg 按字节上限截断并回退到 rune 边界（B-43）：避免按字节硬切切断多字节
// UTF-8 序列（中文/emoji）→ 事件日志出现非法字节。max<=0 或未超限 → 原样返回。
func truncateEventMsg(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// Writer 实现 io.Writer：console 模式写 out；事件模式写 Windows 事件日志（需已注册事件源，失败回退 console）。
type Writer struct {
	mu   sync.Mutex
	out  io.Writer
	elog *eventlog.Log
}

// NewWriter 创建日志 writer。
//   - serviceName: 事件日志源名（需经 winsvc.Install 注册）
//   - useEventLog: true 时优先写 Windows 事件日志
//   - out: console 落点（nil = os.Stderr）
func NewWriter(serviceName string, useEventLog bool, out io.Writer) *Writer {
	if out == nil {
		out = os.Stderr
	}
	w := &Writer{out: out}
	if useEventLog {
		if el, err := eventlog.Open(serviceName); err == nil {
			w.elog = el
		}
	}
	return w
}

// Write 写入一条日志行。console：透传（日志格式由 log 包 flags 决定）；事件日志：Info 级写入。
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.elog != nil {
		// 事件日志消息长度上限 31839 字节，按 rune 边界截断保护（B-43）。
		msg := truncateEventMsg(string(p), maxEventLogMsg)
		if err := w.elog.Info(1, msg); err != nil {
			return w.out.Write(p)
		}
		return len(p), nil
	}
	return w.out.Write(p)
}

// Error 写错误级日志（console 前缀 ERROR；事件日志 Error 级）。
func (w *Writer) Error(v ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	msg := fmt.Sprint(v...)
	if w.elog != nil {
		_ = w.elog.Error(1, truncateEventMsg(msg, maxEventLogMsg))
		return
	}
	fmt.Fprintf(w.out, "%s ERROR %s\n", nowPrefix(), msg)
}

// Warn 写警告级日志。
func (w *Writer) Warn(v ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	msg := fmt.Sprint(v...)
	if w.elog != nil {
		_ = w.elog.Warning(1, truncateEventMsg(msg, maxEventLogMsg))
		return
	}
	fmt.Fprintf(w.out, "%s WARN %s\n", nowPrefix(), msg)
}

// Info 写信息级日志。
func (w *Writer) Info(v ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	msg := fmt.Sprint(v...)
	if w.elog != nil {
		_ = w.elog.Info(1, truncateEventMsg(msg, maxEventLogMsg))
		return
	}
	fmt.Fprintf(w.out, "%s INFO %s\n", nowPrefix(), msg)
}

// Close 释放事件日志句柄。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.elog != nil {
		err := w.elog.Close()
		w.elog = nil
		return err
	}
	return nil
}
