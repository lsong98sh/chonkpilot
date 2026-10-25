// 进程级黑盒：脚本 / 网络工具的路径强约束（决策 R-11）。
//
// 断言真实调用形态：<exe> <tool> --input=<json> 的退出码与 stdout（错误消息）。
// 相对路径 → 非 0 退出码 + 错误消息含原值；绝对路径 → 正常（退出码 0）。
// 调用上下文（instance/workdir/datadir）经 **子进程环境变量 CHONKPILOT_*** 注入（R-11 二次升级）。
//
// 2026-10-25（PLUGIN-SPLIT-1）：**file 域 11 工具已随 file 插件迁出主仓**
// （`chonkpilot-plugins/file`，执行体 = 插件自身 MCP server，`chonkpilot-tools-executor.exe` 已删除）；
// 其路径约束回归改为进程内用例：`chonkpilot-plugins/file/lib/toolset/path_constraint_test.go`。
// 本文件只保留 script_run / web_fetch（仍在主仓 executor）。
package chonkpilotmcptools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// distExe 定位 dist/other/capability/executors 下的 executor（相对本测试源文件路径，不依赖 cwd）。
//
// 产物缺失默认 Skip；设环境变量 CHONK_REQUIRE_DIST=1 时改为**强制失败**（防"假绿"）。
func distExe(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	p := filepath.Join(root, "dist", "other", "capability", "executors", name)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	requireDistOrSkip(t, "executor 未构建（先跑 build-mcp-server.ps1）", p)
	return ""
}

// requireDistOrSkip：产物缺失时默认 Skip；设 CHONK_REQUIRE_DIST=1 则强制失败（门禁防假绿）。
func requireDistOrSkip(t *testing.T, reason, path string) {
	t.Helper()
	if os.Getenv("CHONK_REQUIRE_DIST") == "1" {
		t.Fatalf("[CHONK_REQUIRE_DIST=1] 产物缺失，强制失败（防假绿）：%s（%s）", reason, path)
	}
	t.Logf("⚠️ 跳过（产物缺失 → 假绿风险）：%s（%s）；设 CHONK_REQUIRE_DIST=1 可强制失败", reason, path)
	t.Skipf("%s", reason)
}

// baseEnv 内部 executor 的调用上下文（宿主 spawn executor 时注入的 CHONKPILOT_*）。
func baseEnv() map[string]string {
	return map[string]string{"CHONKPILOT_INSTANCE": "unittest-l2"}
}

// exeForTool 按工具所属执行体返回 exe（一工具/一域一 exe）。
func exeForTool(t *testing.T, tool string) string {
	t.Helper()
	switch tool {
	case "script_run":
		return distExe(t, "chonkpilot-script-executor.exe")
	case "web_fetch":
		return distExe(t, "chonkpilot-web-executor.exe")
	default:
		t.Fatalf("未知工具：%s", tool)
		return ""
	}
}

// runTool 以 <exe> <tool> --input=<tmp.json> 执行（env = 追加的子进程环境变量），返回退出码与 stdout。
func runTool(t *testing.T, exe, tool string, args map[string]any, env map[string]string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.json")
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	if err := os.WriteFile(in, raw, 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	cmd := exec.Command(exe, tool, "--input="+in)
	// 剥离父进程可能残留的 CHONKPILOT_* 后再追加本次 env，保证注入确定（同名以 env 为准）。
	cmd.Env = stripChonkEnv(os.Environ())
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var so, se strings.Builder
	cmd.Stdout = &so
	cmd.Stderr = &se
	err = cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("exec: %v (stderr=%q)", err, se.String())
	}
	return code, so.String()
}

// stripChonkEnv 复制环境并剥离 CHONKPILOT_* 前缀项。
func stripChonkEnv(base []string) []string {
	out := make([]string, 0, len(base))
	for _, kv := range base {
		if strings.HasPrefix(kv, "CHONKPILOT_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// TestPathConstraintRejectsRelative 脚本 / 网络工具的相对路径入参 → 非 0 退出码 + 消息含原值。
func TestPathConstraintRejectsRelative(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args map[string]any
		want string // 错误消息须含的原值
	}{
		{"script_run.file", "script_run",
			map[string]any{"runtime": "python", "file": "s.py"}, "s.py"},
		{"script_run.workdir", "script_run",
			map[string]any{"runtime": "cmd", "script": "echo hi", "workdir": "sub"}, "sub"},
		{"script_run.interpreter", "script_run",
			map[string]any{"runtime": "python", "script": "print(1)", "interpreter": "python"}, "python"},
		{"web_fetch.save_as", "web_fetch",
			map[string]any{"url": "http://127.0.0.1:1/never", "save_as": "out.bin"}, "out.bin"},
		{"web_fetch.form_files[].path", "web_fetch",
			map[string]any{"url": "http://127.0.0.1:1/never",
				"form":       map[string]any{"k": "v"},
				"form_files": []any{map[string]any{"field": "f", "path": "rel.txt"}}}, "rel.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exe := exeForTool(t, tc.tool)
			code, out := runTool(t, exe, tc.tool, tc.args, baseEnv())
			if code == 0 {
				t.Fatalf("相对路径应非 0 退出，got code=0 out=%q", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("错误消息应含原值 %q，got out=%q", tc.want, out)
			}
			if !strings.Contains(out, "绝对路径") {
				t.Fatalf("错误消息应含路径约束说明，got out=%q", out)
			}
		})
	}
}
