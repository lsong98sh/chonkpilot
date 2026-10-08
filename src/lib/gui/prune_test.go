//go:build windows

// C 修复：孤儿 WebView2 profile 清理 —— 只清 `gui-*` 且 LastWriteTime 早于 24h 的目录；
// 活跃（<24h）/ 非 gui-* 前缀的目录不被误删；返回（清理数量, 释放字节数）。
package gui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPruneWebviewProfiles 覆盖：超期 gui-* 被删；活跃 gui-* 保留；非 gui-* 前缀保留；统计正确。
func TestPruneWebviewProfiles(t *testing.T) {
	root := t.TempDir()
	mk := func(name string, age time.Duration) string {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, 100), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		ts := time.Now().Add(-age)
		if err := os.Chtimes(dir, ts, ts); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
		return dir
	}
	oldGUI := mk("gui-111", 25*time.Hour)     // 超期 gui-* → 应删
	freshGUI := mk("gui-222", 1*time.Hour)    // 活跃 gui-* → 保留
	oldOther := mk("other-333", 25*time.Hour) // 非 gui-* 前缀 → 保留

	removed, freed := pruneWebviewProfiles(root)
	if removed != 1 || freed != 100 {
		t.Fatalf("应清理 1 个 / 释放 100 字节：removed=%d freed=%d", removed, freed)
	}
	if _, err := os.Stat(oldGUI); !os.IsNotExist(err) {
		t.Fatalf("超期 gui-* 应被删：err=%v", err)
	}
	if _, err := os.Stat(freshGUI); err != nil {
		t.Fatalf("活跃 gui-* 不应被删：err=%v", err)
	}
	if _, err := os.Stat(oldOther); err != nil {
		t.Fatalf("非 gui-* 前缀不应被删：err=%v", err)
	}
}

// TestPruneWebviewProfilesMissingRoot 根目录不存在 → 静默返回（不 panic、不计清理）。
func TestPruneWebviewProfilesMissingRoot(t *testing.T) {
	removed, freed := pruneWebviewProfiles(filepath.Join(t.TempDir(), "nope"))
	if removed != 0 || freed != 0 {
		t.Fatalf("根不存在应返回 0/0：removed=%d freed=%d", removed, freed)
	}
}
