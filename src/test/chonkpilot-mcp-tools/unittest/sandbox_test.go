// sandbox_test.go — executor 侧 agentbox 沙箱的**进程级黑盒**回归（L2，工程目录
// chonkpilot-test/chonkpilot-mcp-tools/unittest）。
//
// 契约：宿主（mcp-server）spawn executor 时经环境变量 CHONKPILOT_SANDBOX 注入策略 JSON
// （[{dir, writable}]，允许目录递归）。本测试直接 exec 真实 executor exe 并注入该环境变量，
// 断言：
//   - 开启隔离：允许目录内写成功、允许目录外写/读被拒绝（exit≠0 + 可诊断消息 + 文件未落盘）；
//   - 关闭隔离（不注入）：同样两次操作均成功（默认兼容，行为与引入前等价）。
package chonkpilotmcptools

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
)

// runExecutorEnv 以 <exe> <tool> --input=<tmp.json> 执行，注入指定环境变量（追加在宿主环境之上）。
// 恒注入 CHONKPILOT_INSTANCE（宿主 spawn executor 的正常形态；filesys_run 的 BuildToolEnv 依赖它）。
// 返回退出码、stdout、stderr。
func runExecutorEnv(t *testing.T, exe, tool string, args map[string]any, env map[string]string) (int, string, string) {
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
	cmd.Env = append(os.Environ(), "CHONKPILOT_INSTANCE=sandbox-l2-test")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err = cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("exec: %v (stderr=%q)", err, se.String())
	}
	return code, so.String(), se.String()
}

// sandboxEnvJSON 构造策略环境变量值（[{dir, writable}]）。
func sandboxEnvJSON(t *testing.T, rules []agentbox.Rule) string {
	t.Helper()
	b, err := json.Marshal(rules)
	if err != nil {
		t.Fatalf("marshal policy: %v", err)
	}
	return string(b)
}

// insScript 构造 filesys_run 的 INS（创建文件）脚本。
func insScript(path, content string) map[string]any {
	esc := strings.ReplaceAll(path, `\`, `\\`)
	return map[string]any{"script": `INS #"` + esc + `" "` + content + `"`}
}

// TestSandboxEnforcedWhenEnabled：注入策略（仅允许 allowDir，可写）→
// 允许目录内 INS 成功落盘；目录外 INS 被拒绝且不落盘；目录外 file_read 被拒绝。
func TestSandboxEnforcedWhenEnabled(t *testing.T) {
	exe := coreExe(t)
	base := t.TempDir()
	allowDir := filepath.Join(base, "allow")
	denyDir := filepath.Join(base, "deny")
	for _, d := range []string{allowDir, denyDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	// 预置一个「目录外」文件，用于断言读拒绝
	denyFile := filepath.Join(denyDir, "secret.txt")
	if err := os.WriteFile(denyFile, []byte("top-secret"), 0o644); err != nil {
		t.Fatalf("seed deny file: %v", err)
	}
	env := map[string]string{
		agentbox.EnvSandbox: sandboxEnvJSON(t, []agentbox.Rule{{Dir: allowDir, Writable: true}}),
	}

	// ① 允许目录内写 → 成功 + 落盘
	inside := filepath.Join(allowDir, "ok.txt")
	code, out, se := runExecutorEnv(t, exe, "filesys_run", insScript(inside, "hello"), env)
	if code != 0 {
		t.Fatalf("允许目录内写应成功：exit=%d out=%q stderr=%q", code, out, se)
	}
	if b, err := os.ReadFile(inside); err != nil || string(b) != "hello" {
		t.Fatalf("允许目录内文件应落盘且内容正确：err=%v content=%q", err, string(b))
	}

	// ② 允许目录外写 → 拒绝（exit≠0）+ 明确 agentbox 拒绝消息 + 不落盘
	outside := filepath.Join(denyDir, "bad.txt")
	code, out, se = runExecutorEnv(t, exe, "filesys_run", insScript(outside, "nope"), env)
	if code == 0 {
		t.Fatalf("允许目录外写必须失败：exit=0 out=%q", out)
	}
	if !strings.Contains(out, "agentbox") {
		t.Fatalf("拒绝消息应带 agentbox 标识：out=%q", out)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("越界写不得落盘（stat err=%v）", err)
	}
	if !strings.Contains(se, "[agentbox]") {
		t.Fatalf("应留下 stderr 审计行：[agentbox] ...；stderr=%q", se)
	}

	// ③ 允许目录外读 → 拒绝
	readArgs := map[string]any{"files": []any{map[string]any{"path": denyFile}}}
	code, out, _ = runExecutorEnv(t, exe, "file_read", readArgs, env)
	if code == 0 {
		t.Fatalf("允许目录外读必须失败：exit=0 out=%q", out)
	}
	if !strings.Contains(out, "agentbox") {
		t.Fatalf("读拒绝消息应带 agentbox 标识：out=%q", out)
	}

	// ④ 允许目录内读 → 成功
	readArgs = map[string]any{"files": []any{map[string]any{"path": inside}}}
	if code, out, se = runExecutorEnv(t, exe, "file_read", readArgs, env); code != 0 {
		t.Fatalf("允许目录内读应成功：exit=%d out=%q stderr=%q", code, out, se)
	}
}

// TestSandboxDisabledByDefault：不注入策略环境变量 → 目录内外读写**均成功**（默认兼容）。
func TestSandboxDisabledByDefault(t *testing.T) {
	exe := coreExe(t)
	base := t.TempDir()
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	f1 := filepath.Join(a, "one.txt")
	f2 := filepath.Join(b, "two.txt")
	for _, tc := range []string{f1, f2} {
		code, out, se := runExecutorEnv(t, exe, "filesys_run", insScript(tc, "ok"), nil)
		if code != 0 {
			t.Fatalf("未开启隔离时写应成功 %s：exit=%d out=%q stderr=%q", tc, code, out, se)
		}
		if _, err := os.Stat(tc); err != nil {
			t.Fatalf("未开启隔离时应落盘 %s：%v", tc, err)
		}
		readArgs := map[string]any{"files": []any{map[string]any{"path": tc}}}
		if code, out, se = runExecutorEnv(t, exe, "file_read", readArgs, nil); code != 0 {
			t.Fatalf("未开启隔离时读应成功 %s：exit=%d out=%q stderr=%q", tc, code, out, se)
		}
	}
}
