package scriptrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/fileops"
)

// TestScriptRunFileRejectsRelative R-11：script_run 的 file 参数为相对路径 → 顶层错误。
func TestScriptRunFileRejectsRelative(t *testing.T) {
	res := HandleScriptRun("", map[string]interface{}{
		"runtime": "python", "file": "script.py",
	})
	if res.Success {
		t.Fatalf("相对 file 应失败，got %+v", res)
	}
	if !strings.Contains(res.Error, "file") || !strings.Contains(res.Error, "script.py") {
		t.Fatalf("错误消息应指明 file 与原值，got %q", res.Error)
	}
}

// TestScriptRunFileAcceptsAbsolute 绝对路径不再被路径校验拦截（后续因脚本不存在而失败，
// 证明已越过 R-11 校验）。
func TestScriptRunFileAcceptsAbsolute(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "not-exist.py")
	t.Setenv(fileops.EnvInterpreters, `{"python":"python"}`)
	res := HandleScriptRun("", map[string]interface{}{
		"runtime": "python", "file": abs,
	})
	if res.Success {
		t.Fatalf("不存在的绝对脚本应失败，got %+v", res)
	}
	if strings.Contains(res.Error, "路径必须是绝对路径") {
		t.Fatalf("绝对路径不应触发 R-11 拒绝，got %q", res.Error)
	}
	if !strings.Contains(res.Error, "脚本文件不存在") {
		t.Fatalf("应因脚本不存在失败，got %q", res.Error)
	}
}

// TestScriptRunWorkdirRejectsRelative R-11：workdir（运行目录）为相对路径 → 顶层错误。
func TestScriptRunWorkdirRejectsRelative(t *testing.T) {
	res := HandleScriptRun("", map[string]interface{}{
		"runtime": "cmd", "script": "echo hi", "workdir": "sub/dir",
	})
	if res.Success {
		t.Fatalf("相对 workdir 应失败，got %+v", res)
	}
	if !strings.Contains(res.Error, "workdir") || !strings.Contains(res.Error, "sub/dir") {
		t.Fatalf("错误消息应指明 workdir 与原值，got %q", res.Error)
	}
}

// TestScriptRunInterpreterRejectsRelative R-11：interpreter（解释器/可执行文件路径）为相对 → 顶层错误。
func TestScriptRunInterpreterRejectsRelative(t *testing.T) {
	res := HandleScriptRun("", map[string]interface{}{
		"runtime": "python", "script": "print(1)", "interpreter": "python",
	})
	if res.Success {
		t.Fatalf("相对 interpreter 应失败，got %+v", res)
	}
	if !strings.Contains(res.Error, "interpreter") || !strings.Contains(res.Error, "python") {
		t.Fatalf("错误消息应指明 interpreter 与原值，got %q", res.Error)
	}
}

// TestScriptRunWorkdirAcceptsAbsolute 绝对 workdir 越过 R-11 校验（不再被路径约束拒绝）。
func TestScriptRunWorkdirAcceptsAbsolute(t *testing.T) {
	dir := t.TempDir()
	res := HandleScriptRun("", map[string]interface{}{
		"runtime": "cmd", "script": "echo hi", "workdir": dir,
	})
	if strings.Contains(res.Error, "路径必须是绝对路径") {
		t.Fatalf("绝对 workdir 不应触发 R-11 拒绝，got %q", res.Error)
	}
}

// TestJavaTempScriptExtIsJava 回归 I-80：java 临时脚本扩展名必须为 .java。
// 否则 `java <file>.jsh` 会把路径当类名 → ClassNotFoundException（JDK 单文件源码模式要求 .java）。
func TestJavaTempScriptExtIsJava(t *testing.T) {
	script := "public class Hello { public static void main(String[] a){ System.out.println(\"x\"); } }"
	argv, cleanup, err := buildCommand("java", "javafake", script, "")
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if len(argv) != 2 {
		t.Fatalf("argv = %v, want [interp, script]", argv)
	}
	if ext := filepath.Ext(argv[1]); ext != ".java" {
		t.Fatalf("java 临时脚本扩展名 = %q, want .java（I-80：非 .java 会被当作类名）", ext)
	}
	if b, err := os.ReadFile(argv[1]); err != nil || string(b) != script {
		t.Fatalf("临时脚本内容不符：err=%v content=%q", err, string(b))
	}
}

// TestJavaRealInterpreterRunsScript 端到端（本机有 java 才跑）：script_run(runtime=java)
// 走 JDK 单文件源码模式真实执行，输出哨兵。
func TestJavaRealInterpreterRunsScript(t *testing.T) {
	javaPath, err := exec.LookPath("java")
	if err != nil {
		t.Skip("本机无 java，跳过 java runtime 实跑")
	}
	res := HandleScriptRun("", map[string]interface{}{
		"runtime":     "java",
		"interpreter": javaPath,
		"script":      "public class CkProbe { public static void main(String[] a){ System.out.println(\"CK-JAVA-OK\"); } }",
	})
	if !res.Success {
		t.Fatalf("java 脚本应执行成功，got %+v", res)
	}
	if !strings.Contains(res.Output, "CK-JAVA-OK") {
		t.Fatalf("输出应含哨兵，got %q", res.Output)
	}
}
