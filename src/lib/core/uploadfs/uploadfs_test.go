// uploadfs_test.go — 附件落盘共享 helper（61 §1 gui.upload「server 落盘、只回 name」）：
// dir/name 净化（防穿越/超限）、base64/data URL 解码与大小限制、落盘文件名（uuid+ext）。
package uploadfs

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSanitizeDir(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", DefaultDir},
		{"tmp/uploads", "tmp/uploads"},
		{"screenshot", "screenshot"},
		{"tmp\\uploads", "tmp/uploads"},
		{"screenshot\\", "screenshot"},
		{"\\abs\\path", DefaultDir},
		{"C:/Windows", DefaultDir},
		{"../../evil", DefaultDir},
		{"tmp/../uploads", "uploads"}, // 词法折叠后仍在根内且层级合法
		{"a/b/c", DefaultDir},         // 层级超限（>2）
		{".", DefaultDir},
		{"..", DefaultDir},
	}
	for _, c := range cases {
		if got := SanitizeDir(c.in); got != c.want {
			t.Errorf("SanitizeDir(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a.txt", "a.txt"},
		{"../evil.png", "evil.png"},
		{"dir/sub/x.png", "x.png"},
		{"", "attachment"},
		{".", "attachment"},
		{"..", "attachment"},
	}
	for _, c := range cases {
		if got := SanitizeName(c.in); got != c.want {
			t.Errorf("SanitizeName(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestDecodeData(t *testing.T) {
	// 纯 base64
	if b, err := DecodeData(base64.StdEncoding.EncodeToString([]byte("hi"))); err != nil || string(b) != "hi" {
		t.Fatalf("base64 解码: %q err=%v", b, err)
	}
	// data URL 前缀
	if b, err := DecodeData("data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png"))); err != nil || string(b) != "png" {
		t.Fatalf("data URL 解码: %q err=%v", b, err)
	}
	// 空 / 非法
	if _, err := DecodeData(""); err == nil {
		t.Fatal("空 data 应报错")
	}
	if _, err := DecodeData("@@not-base64@@"); err == nil {
		t.Fatal("非法 base64 应报错")
	}
	// 超限（解码前按 base64 原文长度快速拒绝）
	if _, err := DecodeData(strings.Repeat("A", MaxBase64Len+1)); err == nil {
		t.Fatal("超限 data 应报错")
	}
}

// TestEnforceKeepCap（R7-B-4）：目录总量超 cap → 按 mtime 最旧优先删除至 ≤ cap；
// 未超限 → 不动（返回 0）。
func TestEnforceKeepCap(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string, n int, mtime time.Time) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, n), 0644); err != nil {
			t.Fatalf("写 %s: %v", name, err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatalf("Chtimes %s: %v", name, err)
		}
	}
	base := time.Now()
	mk("old.bin", 10, base.Add(-3*time.Hour))
	mk("mid.bin", 10, base.Add(-2*time.Hour))
	mk("new.bin", 10, base.Add(-1*time.Hour))

	if n := EnforceKeepCap(dir, 100); n != 0 {
		t.Fatalf("未超限不应删除，got %d", n)
	}
	// cap=25 < 总量 30 → 删最旧 old.bin → 20 ≤ 25。
	if n := EnforceKeepCap(dir, 25); n != 1 {
		t.Fatalf("超限应删 1 个最旧文件，got %d", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "old.bin")); err == nil {
		t.Fatal("最旧文件应被删除")
	}
	for _, keep := range []string{"mid.bin", "new.bin"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Fatalf("%s 应保留: %v", keep, err)
		}
	}
}

func TestSave(t *testing.T) {
	root := t.TempDir()
	name, err := Save(root, "screenshot", "shot.png", []byte("body"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !strings.HasSuffix(name, ".png") {
		t.Fatalf("文件名须保留扩展名: %q", name)
	}
	got, err := os.ReadFile(filepath.Join(root, "screenshot", name))
	if err != nil || string(got) != "body" {
		t.Fatalf("落盘读回: %q err=%v", got, err)
	}
	// 越界 dir → 回落缺省目录（不逃逸数据根）
	name2, err := Save(root, "../../evil", "a.txt", []byte("x"))
	if err != nil {
		t.Fatalf("Save(越界 dir): %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tmp", "uploads", name2)); err != nil {
		t.Fatalf("越界 dir 应回落 tmp/uploads: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil")); err == nil {
		t.Fatal("越界 dir 逃出数据根")
	}
}
