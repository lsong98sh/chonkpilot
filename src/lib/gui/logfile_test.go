// GUI 文件日志（logfile.go）白盒：挂载路径、文件 sink 生效、按大小滚动并保留 N 份。
package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAttachFileLogEnablesFileSink：attachFileLog 建 <dataDir>/logs 并把日志同时写入文件。
func TestAttachFileLogEnablesFileSink(t *testing.T) {
	dir := t.TempDir()
	logDir, err := attachFileLog(dir)
	if err != nil {
		t.Fatalf("attachFileLog: %v", err)
	}
	if want := filepath.Join(dir, "logs"); logDir != want {
		t.Fatalf("logDir = %q，want %q", logDir, want)
	}
	t.Cleanup(detachFileLog) // 关闭文件句柄并还原全局 sink（避免影响其它用例）

	const line = "gui-log-test-line\n"
	if _, err := logWriter.Write([]byte(line)); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(logDir, guiLogFileName))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(b), strings.TrimSpace(line)) {
		t.Fatalf("日志文件未写入内容: %q", string(b))
	}
}

// TestRotateWriterRollsAndKeepsN：写满即滚动，备份保留 N 份（最旧丢弃）。
func TestRotateWriterRollsAndKeepsN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gui.log")
	const maxBytes, maxFiles = 16, 3
	rw, err := newRotateWriter(path, maxBytes, maxFiles)
	if err != nil {
		t.Fatalf("newRotateWriter: %v", err)
	}
	t.Cleanup(func() { _ = rw.Close() })
	// 每行 11 字节：maxBytes=16 → 每两次写入触发一次滚动；10 行 ≈ 5 次滚动。
	for i := 0; i < 10; i++ {
		if _, err := rw.Write([]byte("0123456789\n")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	for _, p := range []string{path, path + ".1", path + ".2", path + ".3"} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("滚动后应存在 %s: %v", p, err)
		}
	}
	if _, err := os.Stat(path + ".4"); err == nil {
		t.Fatalf("备份份数应受限于 %d（不应存在 %s.4）", maxFiles, path)
	}
	// 原始证据：滚动后文件清单与行数（供人工核对）
	matches, _ := filepath.Glob(path + "*")
	for _, p := range matches {
		b, _ := os.ReadFile(p)
		t.Logf("rolling file: %s bytes=%d lines=%d", filepath.Base(p), len(b), strings.Count(string(b), "\n"))
	}
}
