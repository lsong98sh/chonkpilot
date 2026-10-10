// flash_test.go — flash 薄壳客户端的**纯逻辑**（打开语义 / 最近项目 / 项目占用锁 / data-dir 约定）。
//
// 口径来源：[62 §3A](../../../../docs/spec/60-reference/62-命令行与参数.md) 与
// [19 §8.11](../../../../docs/spec/10-architecture/19-多租户服务端形态.md)。
package flash_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-gui/flash"
)

// TestDataDirForConvention：flash 本地数据根 = `<work_dir>/.chonkpilot`（恒非空）。
func TestDataDirForConvention(t *testing.T) {
	wd := filepath.Join(t.TempDir(), "proj")
	got := flash.DataDirFor(wd)
	if want := filepath.Join(wd, ".chonkpilot"); got != want {
		t.Fatalf("DataDirFor = %q, want %q", got, want)
	}
}

// TestResolveOpenDirExplicit：显式 --work-dir 且目录未被打开 → 直接采用（绝对化）。
func TestResolveOpenDirExplicit(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "p1")
	mustMkdir(t, dir)

	r := flash.DirResolver{BaseDir: base, Exists: dirExists, Free: func(string) bool { return true }}
	got, err := r.Resolve("p1", nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != dir {
		t.Fatalf("Resolve = %q, want %q", got, dir)
	}
}

// TestResolveOpenDirExplicitBusyPicks：显式目录**已被本用户打开** → 让用户选目录（62 §3A）。
func TestResolveOpenDirExplicitBusyPicks(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "p1")
	mustMkdir(t, dir)
	picked := filepath.Join(base, "p2")
	mustMkdir(t, picked)

	r := flash.DirResolver{
		BaseDir: base,
		Exists:  dirExists,
		Free:    func(d string) bool { return !samePath(d, dir) }, // 仅显式目录被占用
		Picker:  func() (string, error) { return picked, nil },
	}
	got, err := r.Resolve("p1", nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != picked {
		t.Fatalf("Resolve = %q, want picked %q", got, picked)
	}
}

// TestResolveOpenDirRecentsOrder：未给显式目录 → 按最近顺序取第一个**存在且可用**的目录；
// 不存在 / 已打开的都跳过（62 §3A「最近关闭 → 次近」）。
func TestResolveOpenDirRecentsOrder(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "gone")
	busy := filepath.Join(base, "busy")
	free := filepath.Join(base, "free")
	mustMkdir(t, busy)
	mustMkdir(t, free)

	r := flash.DirResolver{
		BaseDir: base,
		Exists:  dirExists,
		Free:    func(d string) bool { return !samePath(d, busy) },
	}
	got, err := r.Resolve("", []string{missing, busy, free})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != free {
		t.Fatalf("Resolve = %q, want %q", got, free)
	}
}

// TestResolveOpenDirNoProject：无显式、无可用最近、无 picker → ErrNoProject（不静默绑定空目录）。
func TestResolveOpenDirNoProject(t *testing.T) {
	r := flash.DirResolver{BaseDir: t.TempDir(), Exists: dirExists, Free: func(string) bool { return true }}
	_, err := r.Resolve("", nil)
	if !errors.Is(err, flash.ErrNoProject) {
		t.Fatalf("err = %v, want ErrNoProject", err)
	}
}

// TestSaveRecentDedupeCap：最近项目去重 + 最近在前 + 上限截断，落盘-读取往返一致。
func TestSaveRecentDedupeCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flash-recents.json")
	for i := 0; i < flash.RecentsCap+3; i++ {
		if err := flash.SaveRecent(path, filepath.Join("C:", "d", string(rune('a'+i)))); err != nil {
			t.Fatalf("SaveRecent: %v", err)
		}
	}
	// 重复保存已存在项 → 去重并前移（总长不变）。
	top := filepath.Join("C:", "d", "a")
	if err := flash.SaveRecent(path, top); err != nil {
		t.Fatalf("SaveRecent dup: %v", err)
	}
	got := flash.LoadRecents(path)
	if len(got) != flash.RecentsCap {
		t.Fatalf("len = %d, want %d（上限截断）", len(got), flash.RecentsCap)
	}
	if got[0] != top {
		t.Fatalf("head = %q, want %q（去重后前移）", got[0], top)
	}
	seen := map[string]bool{}
	for _, d := range got {
		if seen[d] {
			t.Fatalf("重复项: %q", d)
		}
		seen[d] = true
	}
}

// TestLoadRecentsMissing：文件缺失 / 损坏 → nil（不阻断启动）。
func TestLoadRecentsMissing(t *testing.T) {
	if got := flash.LoadRecents(filepath.Join(t.TempDir(), "nope.json")); got != nil {
		t.Fatalf("LoadRecents(missing) = %v, want nil", got)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := flash.LoadRecents(bad); got != nil {
		t.Fatalf("LoadRecents(bad) = %v, want nil", got)
	}
}

// TestAcquireProjectBusy：同 work-dir 第二次获取 → busy（per-user 单例判据）。
func TestAcquireProjectBusy(t *testing.T) {
	dir := t.TempDir()
	l, busy, err := flash.AcquireProject(dir)
	if err != nil || busy || l == nil {
		t.Fatalf("首次 AcquireProject = (%v, busy=%v, err=%v)", l, busy, err)
	}
	_, busy2, err2 := flash.AcquireProject(dir)
	if err2 != nil {
		t.Fatalf("二次 AcquireProject err = %v", err2)
	}
	if !busy2 {
		t.Fatal("同 work-dir 二次获取应 busy（单例）")
	}
	if flash.WorkDirFree(dir) {
		t.Fatal("持锁期间 WorkDirFree 应为 false")
	}
	l.Release()
	if !flash.WorkDirFree(dir) {
		t.Fatal("释放后 WorkDirFree 应为 true")
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func dirExists(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

// samePath 与 instance.Manager 同口径（Windows 路径语义：大小写不敏感）。
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
