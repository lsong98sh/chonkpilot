// toolchain_test.go — 工具链探测单测（I-65 ⑥ Chrome 探测不到）：
// 候选命中 / 未安装返回空 / %VAR% 展开 / Chrome 版本目录解析。全部用 t.TempDir() 构造，
// 不依赖本机真实安装。
package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

// mkFile 在指定路径造一个占位文件（父目录自动创建）。
func mkFile(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", p, err)
	}
}

// TestFindToolchainCandidateHit 常见安装目录命中：Bin 非 PATH 名，仅靠 Dirs 必须找到。
func TestFindToolchainCandidateHit(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Application")
	exe := filepath.Join(app, "chrome_probexyz.exe")
	mkFile(t, exe)

	got := findToolchain(toolchainCandidate{Bin: "chrome_probexyz", Dirs: []string{app}})
	if got != exe {
		t.Fatalf("候选命中失败：want %q, got %q", exe, got)
	}
}

// TestFindToolchainNotFound 未安装 → 返回空字符串（不得硬编码"成功"）。
func TestFindToolchainNotFound(t *testing.T) {
	got := findToolchain(toolchainCandidate{
		Bin:  "chonkpilot_definitely_missing_tool",
		Dirs: []string{t.TempDir()},
	})
	if got != "" {
		t.Fatalf("未安装应返回空，实际 %q", got)
	}
}

// TestExpandWinEnv 候选目录里的 %VAR% 必须被展开（os.ExpandEnv 只认 $VAR，这正是
// Chrome 探测不到的直接原因）。
func TestExpandWinEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHONK_TEST_ROOT", root)

	if got, want := expandWinEnv(`%CHONK_TEST_ROOT%\Google\Chrome\Application`), filepath.Join(root, `Google\Chrome\Application`); got != want {
		t.Fatalf("%%VAR%% 展开失败：want %q, got %q", want, got)
	}
	if got, want := expandWinEnv(`$CHONK_TEST_ROOT\x`), filepath.Join(root, `x`); got != want {
		t.Fatalf("$VAR 展开失败：want %q, got %q", want, got)
	}
	// 无百分号配对的字面量原样保留
	if got := expandWinEnv(`C:\plain\dir`); got != `C:\plain\dir` {
		t.Fatalf("纯字面量被改动：%q", got)
	}
}

// TestVersionFromDir Chrome 版本来自 Application\<ver>\ 目录名（--version 不可靠）。
func TestVersionFromDir(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Application")
	exe := filepath.Join(app, "chrome.exe")
	mkFile(t, exe)
	for _, d := range []string{"9.0.0.0", "152.0.7977.84", "PlatformExperienceHelper", "SetupMetrics", "100.0.4896.75"} {
		if err := os.MkdirAll(filepath.Join(app, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := versionFromDir(exe); got != "152.0.7977.84" {
		t.Fatalf("应取最高版本号目录，实际 %q", got)
	}
}

// TestVersionFromDirEmpty 同目录无版本号子目录 → 空（不误取非数字目录）。
func TestVersionFromDirEmpty(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Application")
	exe := filepath.Join(app, "chrome.exe")
	mkFile(t, exe)
	if err := os.MkdirAll(filepath.Join(app, "PlatformExperienceHelper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := versionFromDir(exe); got != "" {
		t.Fatalf("应返回空，实际 %q", got)
	}
}

// TestToolchainVersionDirVer chrome 走目录版本，不执行进程。
func TestToolchainVersionDirVer(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Application")
	exe := filepath.Join(app, "chrome.exe")
	mkFile(t, exe)
	if err := os.MkdirAll(filepath.Join(app, "152.0.7977.84"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := toolchainCandidate{ID: "chrome", Bin: "chrome", DirVer: true}
	if got := toolchainVersion(c, exe); got != "152.0.7977.84" {
		t.Fatalf("want 152.0.7977.84, got %q", got)
	}
}

// TestToolchainVersionNoArgsNoExec 无参数且非目录版本 → 不启动进程（避免误弹窗），返回空。
func TestToolchainVersionNoArgsNoExec(t *testing.T) {
	c := toolchainCandidate{ID: "x", Bin: "x"}
	if got := toolchainVersion(c, filepath.Join(t.TempDir(), "nope.exe")); got != "" {
		t.Fatalf("want 空，got %q", got)
	}
}
