package mcpgateway

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/sandbox"
)

// TestEnsureLowILWritableRootsLabelsSandboxDirs（G-75）：ensureLowILWritableRoots 除
// workdir + .chonkpilot/tmp 外，还须给 SandboxDirs 里 writable=true 的目录打 Low IL 标签
// （writable=false 的只读条目不打标）。
//
// 打标需要 WRITE_OWNER / SeRelabelPrivilege；无权限（如部分盘符目录）时跳过。
func TestEnsureLowILWritableRootsLabelsSandboxDirs(t *testing.T) {
	workdir := t.TempDir()
	extraRW := t.TempDir()
	extraRO := t.TempDir()

	var warns []string
	logf := func(format string, args ...any) { warns = append(warns, format) }
	ensureLowILWritableRoots("t-g75", workdir, []sandbox.Rule{
		{Dir: extraRW, Writable: true},
		{Dir: extraRO, Writable: false},
	}, logf)
	_ = extraRO
	for _, w := range warns {
		if strings.Contains(w, "low-IL tag") {
			t.Skipf("skip：当前环境无法打 Low IL 标签（权限不足）：%s", w)
		}
	}

	// 内置默认：.chonkpilot/tmp 已创建。
	if _, err := os.Stat(filepath.Join(workdir, ".chonkpilot", "tmp")); err != nil {
		t.Fatalf("tmp 目录未创建: %v", err)
	}
	// SandboxDirs writable 目录已打 Low IL 标签。
	assertLowIL := func(dir string, want bool) {
		t.Helper()
		out, err := exec.Command("icacls", dir).CombinedOutput()
		if err != nil {
			t.Skipf("skip：icacls 查询失败: %v", err)
		}
		got := strings.Contains(string(out), "Low Mandatory Level")
		if got != want {
			t.Fatalf("%q Low IL 标签 = %v, want %v（icacls 输出：%s）", dir, got, want, strings.TrimSpace(string(out)))
		}
	}
	assertLowIL(workdir, true)
	assertLowIL(extraRW, true)
	assertLowIL(extraRO, false)
}
