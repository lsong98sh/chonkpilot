package project

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProbeEmptyDir 空目录 → Empty=true、HasCode=false。
func TestProbeEmptyDir(t *testing.T) {
	dir := t.TempDir()
	res := Probe(dir)
	if !res.Empty {
		t.Fatalf("empty dir: Empty=false, want true (file_count=%d)", res.FileCount)
	}
	if res.HasCode {
		t.Fatalf("empty dir: HasCode=true, want false")
	}
}

// TestProbeIgnoresGitAndData 只有 .git / .chonkpilot 时仍视为空目录，但 HasGit=true。
func TestProbeIgnoresGitAndData(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".chonkpilot", "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := Probe(dir)
	if res.HasGit != true {
		t.Fatalf("HasGit=false, want true")
	}
	if !res.Empty {
		t.Fatalf("only .git/.chonkpilot should be Empty=true (file_count=%d)", res.FileCount)
	}
}

// TestProbeDetectsCodeAndToolchain 代码 + 清单 → HasCode / 语言 / 包管理 / 构建 / 测试 / 框架。
func TestProbeDetectsCodeAndToolchain(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{"dependencies":{"vue":"^3.0.0"},"devDependencies":{"vite":"^5.0.0"}}`)
	write("pnpm-lock.yaml", "")
	write("vite.config.ts", "")
	write("vitest.config.ts", "")
	write(".eslintrc.json", "")
	write("README.md", "# demo")
	write("src/main.ts", "export const x = 1")

	res := Probe(dir)
	if res.Empty || !res.HasCode {
		t.Fatalf("want non-empty with code; got Empty=%v HasCode=%v", res.Empty, res.HasCode)
	}
	if !res.HasReadme {
		t.Fatalf("HasReadme=false, want true")
	}
	if res.PackageManager != "pnpm" {
		t.Fatalf("PackageManager=%q, want pnpm", res.PackageManager)
	}
	if res.BuildTool != "vite" {
		t.Fatalf("BuildTool=%q, want vite", res.BuildTool)
	}
	if res.TestTool != "vitest" {
		t.Fatalf("TestTool=%q, want vitest", res.TestTool)
	}
	if res.LintTool != "eslint" {
		t.Fatalf("LintTool=%q, want eslint", res.LintTool)
	}
	if len(res.Languages) == 0 || res.Languages[0] != "TypeScript" {
		t.Fatalf("Languages=%v, want TypeScript first", res.Languages)
	}
	if !contains(res.Frameworks, "Vue") || !contains(res.Frameworks, "Vite") {
		t.Fatalf("Frameworks=%v, want Vue+Vite", res.Frameworks)
	}
	if !contains(res.TopDirs, "src") {
		t.Fatalf("TopDirs=%v, want src", res.TopDirs)
	}
}

// TestSpecPath 落点固定为 <workDir>/.chonkpilot/project_spec.md。
func TestSpecPath(t *testing.T) {
	got := SpecPath("/tmp/proj")
	if got != "/tmp/proj/.chonkpilot/project_spec.md" {
		t.Fatalf("SpecPath=%q", got)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
