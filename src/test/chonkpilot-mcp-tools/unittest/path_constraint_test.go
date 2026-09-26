// 进程级黑盒：文件操作参数路径强约束（决策 R-11）。
//
// 断言真实调用形态：<exe> <tool> --input=<json> 的退出码与 stdout（错误消息）。
// 相对路径 → 非 0 退出码 + 错误消息含原值；绝对路径 → 正常（退出码 0）。
// 调用上下文（instance/workdir/datadir）经 **子进程环境变量 CHONKPILOT_*** 注入（R-11 二次升级；
// 不再经 args 的 _instance/_workdir）。
// 与 find_test.go 同属进程级黑盒；被测 exe 缺失时整体 Skip（须先构建）。
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

// TestPathConstraintRejectsRelative 各工具的相对路径入参 → 非 0 退出码 + 消息含原值。
func TestPathConstraintRejectsRelative(t *testing.T) {
	exe := coreExe(t)
	cases := []struct {
		name string
		tool string
		args map[string]any
		want string // 错误消息须含的原值
	}{
		{"file_read.files[].path", "file_read",
			map[string]any{"files": []any{map[string]any{"path": "a.txt"}}}, "a.txt"},
		{"file_find.path", "file_find", map[string]any{"path": "src"}, "src"},
		{"file_diff.file1", "file_diff",
			map[string]any{"file1": "a.txt", "file2": "b.txt"}, "a.txt"},
		{"filesys_run.DSL", "filesys_run",
			map[string]any{"script": "RPL #\"src/main.py\" \"a\" \"b\""}, "src/main.py"},
		{"filesys_run.md5key", "filesys_run",
			map[string]any{"script": "INS #\"" + writeTmp(t, "x.txt", "x") + "\" \"x\"",
				"md5": map[string]any{"relative/path.txt": "deadbeef"}}, "relative/path.txt"},
		{"script_run.file", "script_run",
			map[string]any{"runtime": "python", "file": "s.py"}, "s.py"},
		{"script_run.workdir", "script_run",
			map[string]any{"runtime": "cmd", "script": "echo hi", "workdir": "sub"}, "sub"},
		{"script_run.interpreter", "script_run",
			map[string]any{"runtime": "python", "script": "print(1)", "interpreter": "python"}, "python"},
		{"filesys_run.DSL数据源", "filesys_run",
			map[string]any{"script": "LOOP row=#\"rows.csv\".lines\n   SET \"a\" => b\nEND\n"}, "rows.csv"},
		{"web_fetch.save_as", "web_fetch",
			map[string]any{"url": "http://127.0.0.1:1/never", "save_as": "out.bin"}, "out.bin"},
		{"web_fetch.form_files[].path", "web_fetch",
			map[string]any{"url": "http://127.0.0.1:1/never",
				"form":       map[string]any{"k": "v"},
				"form_files": []any{map[string]any{"field": "f", "path": "rel.txt"}}}, "rel.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
	exe := coreExe(t)
	f := writeTmp(t, "ok.txt", "hello\n")
	dir := filepath.Dir(f)

	t.Run("file_read", func(t *testing.T) {
		code, out := runTool(t, exe, "file_read", map[string]any{"files": []any{map[string]any{"path": f}}}, baseEnv())
		if code != 0 {
			t.Fatalf("绝对路径应成功，code=%d out=%q", code, out)
		}
	})
	t.Run("file_find", func(t *testing.T) {
		code, out := runTool(t, exe, "file_find", map[string]any{"path": dir}, baseEnv())
		if code != 0 {
			t.Fatalf("绝对路径应成功，code=%d out=%q", code, out)
		}
	})
	t.Run("file_diff", func(t *testing.T) {
		f2 := writeTmp(t, "ok2.txt", "world\n")
		code, out := runTool(t, exe, "file_diff", map[string]any{"file1": f, "file2": f2}, baseEnv())
		if code != 0 {
			t.Fatalf("绝对路径应成功，code=%d out=%q", code, out)
		}
	})
	t.Run("filesys_run 数据源", func(t *testing.T) {
		csv := filepath.ToSlash(writeTmp(t, "rows.csv", "a\nb\n"))
		script := "LOOP row=#\"" + csv + "\".lines\n   SET \"s\" => last\nEND\n"
		code, out := runTool(t, exe, "filesys_run", map[string]any{"script": script}, baseEnv())
		if code != 0 {
			t.Fatalf("绝对数据源应成功，code=%d out=%q", code, out)
		}
	})
}

// TestPathConstraintAcceptsTempPrefix !/ 前缀映射到 <系统 temp>/chonkpilot/<instance>/（R-11 二次升级）：
// instance 经子进程环境变量 CHONKPILOT_INSTANCE 注入。
func TestPathConstraintAcceptsTempPrefix(t *testing.T) {
	exe := coreExe(t)
	root := filepath.Join(os.TempDir(), "chonkpilot", "l2instance")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "l2.txt"), []byte("temp-prefix-ok\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	env := map[string]string{"CHONKPILOT_INSTANCE": "l2instance"}
	code, out := runTool(t, exe, "file_read", map[string]any{
		"files": []any{map[string]any{"path": "!/l2.txt"}},
	}, env)
	if code != 0 {
		t.Fatalf("!/ 前缀应成功，code=%d out=%q", code, out)
	}
	if !strings.Contains(out, "temp-prefix-ok") {
		t.Fatalf("应读到临时目录文件内容，out=%q", out)
	}
}

// TestPathConstraintTempPrefixRequiresInstance 缺 CHONKPILOT_INSTANCE 时 !/ 解析 → 顶层错误（缺 instance）。
func TestPathConstraintTempPrefixRequiresInstance(t *testing.T) {
	exe := coreExe(t)
	code, out := runTool(t, exe, "file_read", map[string]any{
		"files": []any{map[string]any{"path": "!/l2.txt"}},
	}, map[string]string{"CHONKPILOT_INSTANCE": ""})
	if code == 0 {
		t.Fatalf("缺 instance 时 !/ 应失败，got code=0 out=%q", out)
	}
	if !strings.Contains(out, "缺少 instance") {
		t.Fatalf("错误消息应含「缺少 instance」，got out=%q", out)
	}
}

// TestPathConstraintAcceptsEnvWorkdir filesys_run：子进程环境 CHONKPILOT_WORKDIR 拼绝对路径读数据源。
func TestPathConstraintAcceptsEnvWorkdir(t *testing.T) {
	exe := coreExe(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rows.csv"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "LOOP row=#\"{{env.CHONKPILOT_WORKDIR}}/rows.csv\".lines\n   SET \"s\" => last\nEND\n"
	env := map[string]string{"CHONKPILOT_INSTANCE": "unittest-l2", "CHONKPILOT_WORKDIR": dir}
	code, out := runTool(t, exe, "filesys_run", map[string]any{"script": script}, env)
	if code != 0 {
		t.Fatalf("env 拼绝对路径应成功，code=%d out=%q", code, out)
	}
}

// TestPathConstraintMD5MismatchStaysFails 运行时冲突（md5 不一致）仍整体成功且记 fails。
func TestPathConstraintMD5MismatchStaysFails(t *testing.T) {
	exe := coreExe(t)
	f := writeTmp(t, "m.txt", "original")
	code, out := runTool(t, exe, "filesys_run", map[string]any{
		"script": "RPL #\"" + f + "\" \"original\" \"changed\"",
		"md5":    map[string]any{f: "deadbeef"},
	}, baseEnv())
	if code != 0 {
		t.Fatalf("md5 不一致应整体成功，code=%d out=%q", code, out)
	}
	if !strings.Contains(out, "MD5 不一致") {
		t.Fatalf("应含 MD5 fails，out=%q", out)
	}
	if b, _ := os.ReadFile(f); string(b) != "original" {
		t.Fatalf("md5 不一致不应写盘，got %q", string(b))
	}
}
