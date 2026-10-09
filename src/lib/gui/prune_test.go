//go:build windows

// C 修复：孤儿 WebView2 profile 清理 —— 只清 `gui-*` 且 LastWriteTime 早于 24h 的目录；
// 活跃（<24h）/ 非 gui-* 前缀的目录不被误删；返回（清理数量, 释放字节数）。
// D-34：增补活跃标记防护 —— 目录内 `.active` 标记（含 pid）指向存活进程 → 占用即跳过。
package gui

import (
	"os"
	"path/filepath"
	"strconv"
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
	oldGUI := mk("gui-111", 25*time.Hour)     // 超期 gui-*（无标记）→ 应删
	freshGUI := mk("gui-222", 1*time.Hour)    // 活跃 gui-* → 保留
	oldOther := mk("other-333", 25*time.Hour) // 非 gui-* 前缀 → 保留

	// D-34：超期但带**存活 pid** 活跃标记 → 占用即跳过，绝不删除。
	liveGUI := mk("gui-444", 25*time.Hour)
	if err := os.WriteFile(filepath.Join(liveGUI, webviewProfileMarker),
		[]byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	// 超期且标记 pid 非法（不可用）→ 视为残留，应删。
	badMarkGUI := mk("gui-555", 25*time.Hour)
	if err := os.WriteFile(filepath.Join(badMarkGUI, webviewProfileMarker),
		[]byte("not-a-pid"), 0o644); err != nil {
		t.Fatalf("write bad marker: %v", err)
	}
	// 写标记会刷新目录 mtime → 重新置回超期（否则两目录被当作活跃而跳过，测不到下列分支）。
	old := time.Now().Add(-25 * time.Hour)
	for _, d := range []string{liveGUI, badMarkGUI} {
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatalf("chtimes %s: %v", d, err)
		}
	}

	removed, freed := pruneWebviewProfiles(root)
	// 应删 2 个：oldGUI（100B）+ badMarkGUI（100B 文件 + "not-a-pid" 标记 9B）。
	wantFreed := int64(100 + 100 + len("not-a-pid"))
	if removed != 2 || freed != wantFreed {
		t.Fatalf("应清理 2 个 / 释放 %d 字节：removed=%d freed=%d", wantFreed, removed, freed)
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
	if _, err := os.Stat(liveGUI); err != nil {
		t.Fatalf("被占用的 profile（存活 pid 标记）不应被删（D-34）：err=%v", err)
	}
	if _, err := os.Stat(badMarkGUI); !os.IsNotExist(err) {
		t.Fatalf("标记不可用的超期 gui-* 应被删：err=%v", err)
	}
}

// TestPruneWebviewProfilesMissingRoot 根目录不存在 → 静默返回（不 panic、不计清理）。
func TestPruneWebviewProfilesMissingRoot(t *testing.T) {
	removed, freed := pruneWebviewProfiles(filepath.Join(t.TempDir(), "nope"))
	if removed != 0 || freed != 0 {
		t.Fatalf("根不存在应返回 0/0：removed=%d freed=%d", removed, freed)
	}
}
