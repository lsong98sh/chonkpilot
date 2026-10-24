package themefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 目录不存在 → 返回内置 embed 副本（不报错、不 404）。
func TestLoadMissingDirFallsBackToEmbedded(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "no-such-exe-dir"))
	if err != nil {
		t.Fatalf("Load 不应返回错误: %v", err)
	}
	if string(got) != string(embeddedCSS) {
		t.Fatalf("缺失时应返回内置副本（got %d bytes / embed %d bytes）", len(got), len(embeddedCSS))
	}
	if len(got) == 0 {
		t.Fatal("内置副本不应为空")
	}
}

// exeDir 为空 → 返回内置 embed 副本。
func TestLoadEmptyExeDirFallsBackToEmbedded(t *testing.T) {
	got, err := Load("")
	if err != nil {
		t.Fatalf("Load 不应返回错误: %v", err)
	}
	if string(got) != string(embeddedCSS) {
		t.Fatal("exeDir 为空时应返回内置副本")
	}
}

// 文件存在 → 返回文件内容（可编辑资产优先）。
func TestLoadFromDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, SubDir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	want := ":root{--bg-primary:#010203;}\n[data-theme=\"dark\"]{--bg-primary:#000000;}\n"
	if err := os.WriteFile(filepath.Join(dir, SubDir, FileName), []byte(want), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load 不应返回错误: %v", err)
	}
	if string(got) != want {
		t.Fatalf("应返回磁盘内容，got=%q", string(got))
	}
}

// 文件损坏（此处 = themes.css 是目录，ReadFile 必失败）→ 回落内置 embed 副本。
func TestLoadUnreadableFileFallsBackToEmbedded(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, SubDir, FileName), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load 不应返回错误: %v", err)
	}
	if string(got) != string(embeddedCSS) {
		t.Fatal("文件不可读时应回落内置副本")
	}
}

// 空文件视为不可用 → 回落内置 embed 副本（避免页面拿到空样式 = 白屏）。
func TestLoadEmptyFileFallsBackToEmbedded(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, SubDir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, SubDir, FileName), nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load 不应返回错误: %v", err)
	}
	if string(got) != string(embeddedCSS) {
		t.Fatal("空文件应回落内置副本")
	}
}

// 内置副本内容健全：三套主题块齐备。
func TestEmbeddedHasThreeThemeBlocks(t *testing.T) {
	s := string(embeddedCSS)
	for _, sel := range []string{":root", `[data-theme="dark"]`, `[data-theme="nord"]`} {
		if !strings.Contains(s, sel) {
			t.Fatalf("内置副本缺少主题块 %s", sel)
		}
	}
}
