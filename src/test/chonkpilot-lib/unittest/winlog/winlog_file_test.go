// winlog 文件日志落点（按大小滚动）的纯逻辑验证：
// `-H windowsgui` 常驻进程（chonkpilot-server.exe）无控制台 → 日志须落文件才可诊断；
// 本测试覆盖写入 / 追加 / 滚动（含备份保留上限）/ 建父目录 / 关闭语义（不含任何系统调用桩）。
package winlog_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/winlog"
)

// TestNewFileWriterCreatesParentDir：父目录不存在时自动创建，文件可用。
func TestNewFileWriterCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "server.log")
	w, err := winlog.NewFileWriter(path, 0, 0)
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer w.Close()
	if w.Path() != path {
		t.Fatalf("Path() = %q, want %q", w.Path(), path)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := readFile(t, path); got != "hello" {
		t.Fatalf("文件内容 = %q, want %q", got, "hello")
	}
}

// TestNewFileWriterEmptyPath：空路径报错。
func TestNewFileWriterEmptyPath(t *testing.T) {
	if _, err := winlog.NewFileWriter("", 0, 0); err == nil {
		t.Fatal("空路径应报错")
	}
}

// TestFileWriterAppendAcrossReopen：同一路径重开为**追加**（重启后接着写，不截断）。
func TestFileWriterAppendAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	w1, err := winlog.NewFileWriter(path, 0, 0)
	if err != nil {
		t.Fatalf("NewFileWriter #1: %v", err)
	}
	if _, err := w1.Write([]byte("a")); err != nil {
		t.Fatalf("Write #1: %v", err)
	}
	if err := w1.Close(); err != nil {
		t.Fatalf("Close #1: %v", err)
	}
	w2, err := winlog.NewFileWriter(path, 0, 0)
	if err != nil {
		t.Fatalf("NewFileWriter #2: %v", err)
	}
	defer w2.Close()
	if _, err := w2.Write([]byte("b")); err != nil {
		t.Fatalf("Write #2: %v", err)
	}
	if got := readFile(t, path); got != "ab" {
		t.Fatalf("追加后内容 = %q, want %q", got, "ab")
	}
}

// TestFileWriterRotates：写满上限即滚动，备份保留上限 = maxFiles（不产生 .maxFiles+1）。
func TestFileWriterRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.log")
	const maxBytes, maxFiles = 10, 2
	w, err := winlog.NewFileWriter(path, maxBytes, maxFiles)
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer w.Close()
	for i := 0; i < 4; i++ {
		if _, err := w.Write([]byte("1234567890")); err != nil {
			t.Fatalf("Write #%d: %v", i, err)
		}
	}
	// 主文件 + 2 份备份均存在，各含一行（10 字节）。
	for _, p := range []string{path, path + ".1", path + ".2"} {
		if got := readFile(t, p); got != "1234567890" {
			t.Fatalf("%s 内容 = %q, want %q", p, got, "1234567890")
		}
	}
	// 超出保留上限的备份不得存在。
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("不应存在 %s.3（err=%v）", path, err)
	}
}

// TestFileWriterWriteAfterClose：关闭后再写返回错误（不 panic）。
func TestFileWriterWriteAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	w, err := winlog.NewFileWriter(path, 0, 0)
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close 应幂等: %v", err)
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("关闭后 Write 应返回错误")
	}
}

// readFile 读取文件原文（测试辅助）。
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	return string(b)
}
