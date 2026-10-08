// dslfs_test.go — 共享文件句柄的沙箱与行为档位单测：
//   - 未启用隔离（agentbox.Set(nil)）→ 一律放行；
//   - 启用隔离（允许目录可写）→ 允许目录内放行、目录外读/写被拒且不落盘；
//   - Default / Browser 两档的既有差异（Blocks、空文件 range）逐字保真。
package dslfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
)

// withSandbox 在用例内设置进程级策略并在结束时还原。
func withSandbox(t *testing.T, rules []agentbox.Rule, enabled bool) {
	t.Helper()
	prev := agentbox.Current()
	t.Cleanup(func() { agentbox.Set(prev) })
	if enabled {
		agentbox.Set(agentbox.New(rules))
	} else {
		agentbox.Set(nil)
	}
}

// TestSandboxDisabledAllows：未启用隔离 = 一律放行（读写均成功）。
func TestSandboxDisabledAllows(t *testing.T) {
	withSandbox(t, nil, false)
	f := New(filepath.Join(t.TempDir(), "a.txt"), Default)
	if err := f.WriteAll("hi"); err != nil {
		t.Fatalf("未启用隔离写应放行: %v", err)
	}
	if s, err := f.ReadText(); err != nil || s != "hi" {
		t.Fatalf("未启用隔离读应放行: %q %v", s, err)
	}
}

// TestSandboxEnabledRejectsOutside：启用隔离 → 允许目录内放行、目录外读/写被拒且不落盘。
func TestSandboxEnabledRejectsOutside(t *testing.T) {
	base := t.TempDir()
	allow := filepath.Join(base, "allow")
	deny := filepath.Join(base, "deny")
	for _, d := range []string{allow, deny} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	withSandbox(t, []agentbox.Rule{{Dir: allow, Writable: true}}, true)

	// 允许目录内写/读 → 放行
	in := New(filepath.Join(allow, "ok.txt"), Default)
	if err := in.WriteAll("hello"); err != nil {
		t.Fatalf("允许目录内写应放行: %v", err)
	}
	if s, err := in.ReadText(); err != nil || s != "hello" {
		t.Fatalf("允许目录内读应放行: %q %v", s, err)
	}

	// 目录外写 → 拒绝 + 不落盘
	bad := filepath.Join(deny, "bad.txt")
	if err := New(bad, Default).WriteAll("nope"); err == nil {
		t.Fatal("允许目录外写应被拒")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatalf("越界写不得落盘: %v", err)
	}

	// 目录外读 → 拒绝
	seed := filepath.Join(deny, "seed.txt")
	if err := os.WriteFile(seed, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(seed, Default).ReadText(); err == nil {
		t.Fatal("允许目录外读应被拒")
	}
}

// TestProfileDivergence：Default 与 Browser 两档的既有差异逐字保真。
func TestProfileDivergence(t *testing.T) {
	withSandbox(t, nil, false)
	dir := t.TempDir()

	// 空文件 range：default 档非全量请求报错；browser 档恒返回空。
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(empty, Default).ReadRange(1, 2); err == nil {
		t.Fatal("default 档空文件非全量 range 应报错")
	}
	if got, err := New(empty, Browser).ReadRange(1, 2); err != nil || len(got) != 0 {
		t.Fatalf("browser 档空文件 range 应返回空: %v %v", got, err)
	}

	// Stat.Blocks：default 计算（含 "\n\n" 分隔 → 2），browser 恒 0。
	p := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(p, []byte("a\n\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sd, err := New(p, Default).Stat()
	if err != nil {
		t.Fatal(err)
	}
	sb, err := New(p, Browser).Stat()
	if err != nil {
		t.Fatal(err)
	}
	if sd.Blocks != 2 {
		t.Fatalf("default Blocks=%d, want 2", sd.Blocks)
	}
	if sb.Blocks != 0 {
		t.Fatalf("browser Blocks=%d, want 0", sb.Blocks)
	}
}
