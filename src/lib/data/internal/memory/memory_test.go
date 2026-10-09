// memory 域写路径安全单测（A-29）：项目级记忆目录（<workdir>/.chonkpilot/memory）在工作区内，
// 恶意仓库可预置 symlink 使其指向根外 —— 写前须解析真实落点复验仍在根内，越界即拒绝。
// 创建符号链接需权限（Windows 需开发者模式/管理员）→ 创建失败即跳过（不误报）。
package memory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// newMemoryEnv 构造测试服务（无总线）与实例 Scope。
func newMemoryEnv(t *testing.T) (*Service, facade.Scope) {
	t.Helper()
	usrDir := t.TempDir()
	wd := t.TempDir()
	s := New(kernel.NewBase(nil, kernel.Options{UsrPath: filepath.Join(usrDir, "usr.db"), AppDir: usrDir}))
	return s, facade.Scope{WorkDir: wd}
}

// TestMemorySaveSymlinkDirEscapeRejected：项目级记忆目录被预置为指向根外的 symlink 时，
// MemorySave 必须拒绝（不跟随 symlink 越界写）。
func TestMemorySaveSymlinkDirEscapeRejected(t *testing.T) {
	s, scope := newMemoryEnv(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(scope.WorkDir, ".chonkpilot"), 0o755); err != nil {
		t.Fatalf("mkdir .chonkpilot: %v", err)
	}
	link := filepath.Join(scope.WorkDir, ".chonkpilot", "memory")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("创建符号链接失败（需权限）：%v", err)
	}

	_, err := s.MemorySave(facade.MemorySaveRequest{Scope: scope, Category: "项目概要", Content: "leak"})
	if err == nil {
		t.Fatal("指向根外的 symlink 目录应拒绝写入")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "项目概要.md")); statErr == nil {
		t.Fatal("不得跟随 symlink 越界写文件")
	}
}

// TestMemorySaveSymlinkFileEscapeRejected：目标文件本身为指向根外的 symlink 时同样拒绝。
func TestMemorySaveSymlinkFileEscapeRejected(t *testing.T) {
	s, scope := newMemoryEnv(t)
	memDir := filepath.Join(scope.WorkDir, ".chonkpilot", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}
	outsideFile := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(outsideFile, []byte("orig"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(memDir, "项目概要.md")); err != nil {
		t.Skipf("创建符号链接失败（需权限）：%v", err)
	}

	if _, err := s.MemorySave(facade.MemorySaveRequest{Scope: scope, Category: "项目概要", Content: "leak"}); err == nil {
		t.Fatal("指向根外的 symlink 文件应拒绝写入")
	}
	if raw, _ := os.ReadFile(outsideFile); string(raw) != "orig" {
		t.Fatalf("不得跟随 symlink 覆盖根外文件：%q", raw)
	}
}

// TestMemorySaveNormalPathWrites：无 symlink 的正常路径照常写入（复验不误拒）。
func TestMemorySaveNormalPathWrites(t *testing.T) {
	s, scope := newMemoryEnv(t)
	if _, err := s.MemorySave(facade.MemorySaveRequest{Scope: scope, Category: "项目概要", Content: "ok"}); err != nil {
		t.Fatalf("正常写入不应被拒：%v", err)
	}
	got, err := os.ReadFile(filepath.Join(scope.WorkDir, ".chonkpilot", "memory", "项目概要.md"))
	if err != nil || string(got) != "ok" {
		t.Fatalf("正常路径应落盘：got=%q err=%v", got, err)
	}
}

// TestMemoryPathWithinRealBranches：复验原语的分支口径（不依赖符号链接）——目标（及其父目录）
// 尚不存在（新建）时解析最近存在的祖先回拼，不得误拒；词法上位于 root 之外的目标仍拒绝。
func TestMemoryPathWithinRealBranches(t *testing.T) {
	root := t.TempDir()
	if !memoryPathWithinReal(root, filepath.Join(root, "sub", "项目概要.md")) {
		t.Fatal("不存在的目标（父目录亦不存在）不应被误拒")
	}
	outside := t.TempDir()
	if memoryPathWithinReal(root, filepath.Join(outside, "x.md")) {
		t.Fatal("词法越界目标应拒绝")
	}
}
