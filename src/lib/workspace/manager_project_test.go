package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRegisterProjectValidatesPath：工作区树“添加 workdir”的信任边界 ——
// 只接受已存在的绝对目录；相对路径 / 不存在目录 / 文件路径必须明确拒绝。
func TestRegisterProjectValidatesPath(t *testing.T) {
	m, _, sink := newTestManager(t)

	if _, err := m.RegisterProject("relative/workdir", ""); err == nil {
		t.Fatal("相对路径应拒绝")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := m.RegisterProject(missing, ""); err == nil {
		t.Fatal("不存在的目录应拒绝")
	}
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RegisterProject(file, ""); err == nil {
		t.Fatal("文件路径应拒绝")
	}
	if _, err := m.RegisterProject(t.TempDir(), "relative/data"); err == nil {
		t.Fatal("相对 data_dir 应拒绝")
	}
	// 合法目录 → 注册并广播 workdir-opened。
	wd := t.TempDir()
	sp, err := m.RegisterProject(wd, "")
	if err != nil {
		t.Fatalf("合法目录应注册: %v", err)
	}
	if sp.WorkDir == "" {
		t.Fatalf("注册结果缺 work_dir: %+v", sp)
	}
	opened, _ := sink.counts()
	if opened != 1 {
		t.Fatalf("应广播 1 次 workdir-opened，实际 %d", opened)
	}
}
