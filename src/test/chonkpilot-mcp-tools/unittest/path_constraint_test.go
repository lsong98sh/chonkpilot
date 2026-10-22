// 进程级黑盒：文件操作参数路径强约束（决策 R-11）。
//
// 断言真实调用形态：<exe> <tool> --input=<json> 的退出码与 stdout（错误消息）。
// 相对路径 → 非 0 退出码 + 错误消息含原值；绝对路径 → 正常（退出码 0）。
// 调用上下文（instance/workdir/datadir）经 **子进程环境变量 CHONKPILOT_*** 注入（R-11 二次升级；
// 不再经 args 的 _instance/_workdir）。
// 与 find_test.go 同属进程级黑盒；被测 exe 缺失时整体 Skip（须先构建）。
//
// 2026-10-21（GATEWAY-2/L1）：DSL 退役 —— `filesys_run` 拆为 file 域 11 个原子工具
// （由 `chonkpilot-tools-executor.exe` 承载）；`script_run` / `web_fetch` 各有一工具一 exe。
package chonkpilotmcptools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// baseEnv 内部 executor 的调用上下文（宿主 spawn executor 时注入的 CHONKPILOT_*）。
func baseEnv() map[string]string {
	return map[string]string{"CHONKPILOT_INSTANCE": "unittest-l2"}
}

// exeForTool 按工具所属执行体返回 exe（一工具/一域一 exe）。
func exeForTool(t *testing.T, tool string) string {
	t.Helper()
	switch {
	case strings.HasPrefix(tool, "file_"):
		return toolsExe(t)
	case tool == "script_run":
		return distExe(t, "chonkpilot-script-executor.exe")
	case tool == "web_fetch":
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

func writeTmp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// fileArgs 构造 file 域工具的入参 {purpose, targets:[target]}。
func fileArgs(target map[string]any) map[string]any {
	return map[string]any{"purpose": "路径约束测试", "targets": []any{target}}
}

// execResult 是 file 域工具单个 target 的结果（只取断言所需字段）。
type execResult struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Reason string `json:"reason"`
}

// execSummary 是 file 域工具输出的汇总计数。
type execSummary struct {
	Total   int `json:"total"`
	OK      int `json:"ok"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// execEnvelope 是 file 域工具的统一输出信封。
type execEnvelope struct {
	Results []execResult `json:"results"`
	Summary execSummary  `json:"summary"`
}

// parseEnvelope 解析 stdout 的结果 JSON。
func parseEnvelope(t *testing.T, out string) execEnvelope {
	t.Helper()
	var env execEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout 不是结果 JSON：%v；out=%q", err, out)
	}
	return env
}

// TestPathConstraintRejectsRelative 各工具的相对路径入参 → 非 0 退出码 + 消息含原值。
func TestPathConstraintRejectsRelative(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args map[string]any
		want string // 错误消息须含的原值
	}{
		{"file_read.path相对", "file_read",
			fileArgs(map[string]any{"path": "a.txt"}), "a.txt"},
		{"file_find.root相对", "file_find",
			fileArgs(map[string]any{"root": "src"}), "src"},
		{"file_diff.left相对", "file_diff",
			fileArgs(map[string]any{"left": "a.txt", "right": "b.txt"}), "a.txt"},
		{"file_replace.path相对", "file_replace",
			fileArgs(map[string]any{"path": "src/main.py", "find": "a", "replace": "b"}), "src/main.py"},
		{"file_move.to相对", "file_move",
			fileArgs(map[string]any{"from": writeTmp(t, "x.txt", "x"), "to": "relative/path.txt"}), "relative/path.txt"},
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

// TestPathConstraintAcceptsAbsolute 绝对路径入参 → 正常（退出码 0）。
func TestPathConstraintAcceptsAbsolute(t *testing.T) {
	exe := toolsExe(t)
	f := writeTmp(t, "ok.txt", "hello\n")
	dir := filepath.Dir(f)

	t.Run("file_read", func(t *testing.T) {
		code, out := runTool(t, exe, "file_read", fileArgs(map[string]any{"path": f}), baseEnv())
		if code != 0 {
			t.Fatalf("绝对路径应成功，code=%d out=%q", code, out)
		}
	})
	t.Run("file_find", func(t *testing.T) {
		code, out := runTool(t, exe, "file_find", fileArgs(map[string]any{"root": dir}), baseEnv())
		if code != 0 {
			t.Fatalf("绝对路径应成功，code=%d out=%q", code, out)
		}
	})
	t.Run("file_diff", func(t *testing.T) {
		f2 := writeTmp(t, "ok2.txt", "world\n")
		code, out := runTool(t, exe, "file_diff", fileArgs(map[string]any{"left": f, "right": f2}), baseEnv())
		if code != 0 {
			t.Fatalf("绝对路径应成功，code=%d out=%q", code, out)
		}
	})
}

// TestPathConstraintAcceptsTempPrefix !/ 前缀映射到 <系统 temp>/chonkpilot/<instance>/（R-11 二次升级）：
// instance 经子进程环境变量 CHONKPILOT_INSTANCE 注入。
func TestPathConstraintAcceptsTempPrefix(t *testing.T) {
	exe := toolsExe(t)
	root := filepath.Join(os.TempDir(), "chonkpilot", "l2instance")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "l2.txt"), []byte("temp-prefix-ok\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	env := map[string]string{"CHONKPILOT_INSTANCE": "l2instance"}

	// 写：!/ 前缀落盘到 <temp>/chonkpilot/<instance>/
	code, out := runTool(t, exe, "file_write", fileArgs(map[string]any{
		"path": "!/l2-out.txt", "content": "temp-prefix-ok\n",
	}), env)
	if code != 0 {
		t.Fatalf("!/ 前缀写应成功，code=%d out=%q", code, out)
	}
	data, rerr := os.ReadFile(filepath.Join(root, "l2-out.txt"))
	if rerr != nil {
		t.Fatalf("读取落盘结果失败：%v（out=%q）", rerr, out)
	}
	if !strings.Contains(string(data), "temp-prefix-ok") {
		t.Fatalf("临时目录文件内容不符，got %q", string(data))
	}

	// 读：!/ 前缀解析到同一 instance 目录
	code, out = runTool(t, exe, "file_read", fileArgs(map[string]any{"path": "!/l2.txt"}), env)
	if code != 0 {
		t.Fatalf("!/ 前缀读应成功，code=%d out=%q", code, out)
	}
	if !strings.Contains(out, "temp-prefix-ok") {
		t.Fatalf("应读到临时目录文件内容，got out=%q", out)
	}
}

// TestPathConstraintTempPrefixRequiresInstance 缺 CHONKPILOT_INSTANCE 时 !/ 解析 → 失败（缺 instance）。
func TestPathConstraintTempPrefixRequiresInstance(t *testing.T) {
	exe := toolsExe(t)
	code, out := runTool(t, exe, "file_read", fileArgs(map[string]any{"path": "!/l2.txt"}),
		map[string]string{"CHONKPILOT_INSTANCE": ""})
	if code == 0 {
		t.Fatalf("缺 instance 时 !/ 应失败，got code=0 out=%q", out)
	}
	if !strings.Contains(out, "缺少 instance") {
		t.Fatalf("错误消息应含「缺少 instance」，got out=%q", out)
	}
}

// TestPathConstraintMD5MismatchSkips 运行时冲突（expectMd5 不一致）→ 整体成功（退出码 0）且该项 skipped、不写盘。
func TestPathConstraintMD5MismatchSkips(t *testing.T) {
	exe := toolsExe(t)
	f := writeTmp(t, "m.txt", "original")
	code, out := runTool(t, exe, "file_replace", fileArgs(map[string]any{
		"path": f, "find": "original", "replace": "changed", "expectMd5": "deadbeef",
	}), baseEnv())
	if code != 0 {
		t.Fatalf("md5 不一致应整体成功，code=%d out=%q", code, out)
	}
	env := parseEnvelope(t, out)
	if len(env.Results) != 1 || env.Results[0].Status != "skipped" {
		t.Fatalf("md5 不一致应记 skipped，got=%+v out=%q", env.Results, out)
	}
	if !strings.Contains(env.Results[0].Reason, "md5") {
		t.Fatalf("skip 原因应含 md5，got=%q", env.Results[0].Reason)
	}
	if env.Summary.Failed != 0 || env.Summary.Skipped != 1 {
		t.Fatalf("summary 不符：%+v", env.Summary)
	}
	if b, _ := os.ReadFile(f); string(b) != "original" {
		t.Fatalf("md5 不一致不应写盘，got %q", string(b))
	}
}
