// history 插件 L2 黑盒行为测试（外部包历史_test）：全部经「发送 mq 消息」驱动、「监听 mq 消息」
// 与 git 仓库可观察副作用断言（见 docs/spec/50-testing/50-测试体系.md §1）。
//
// 覆盖（2026-09-27 批次③改造后语义）：
//   - 启用且 workdir 有 .git → 注册 4 个工具（载荷带 pre_hook_subject）；前置钩子在脏时打点建链；
//   - 门控：history.enabled 非 "true" → 不打点、不注册工具；
//   - 非 git 仓库 → 不打点、不注册（工具摘除）；
//   - session-complete（主轮次）轮末补点；
//   - **绝不在用户分支产生提交**（HEAD 不变）；
//   - git 不可用 → Start 返回 nil（功能禁用，非启动失败）。
package history_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin"
	"github.com/chonkpilot/chonkpilot-plugin-history"
)

func pubJSON(bus mq.Bus, subject string, v any) {
	b, _ := json.Marshal(v)
	bus.Emit(context.Background(), subject, b)
}

// regCapture 记录 gateway tools/register|unregister 消息（并应答，使插件注册成功）。
type regCapture struct {
	mu   sync.Mutex
	regs []map[string]any
	uns  int
}

func (r *regCapture) regsList() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, len(r.regs))
	copy(out, r.regs)
	return out
}

func (r *regCapture) unregCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.uns
}

// stubPrjConfig 桩应答 `data-prj-config-load`（按 key 返回值）与 `-save`（吞下应答），
// 并冒充 gateway 方法面 mcp-tools-register/unregister。
func stubPrjConfig(t *testing.T, bus mq.Bus, vals map[string]string) *regCapture {
	t.Helper()
	cap := &regCapture{}
	if _, err := bus.On("data-prj-config-load", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil || m["ok"] != nil {
			return nil
		}
		id := ""
		if d, ok := m["data"].(map[string]any); ok {
			id, _ = d["id"].(string)
		}
		reqID, _ := m["req_id"].(string)
		b, _ := json.Marshal(map[string]any{
			"req_id": reqID, "ok": true, "result": map[string]any{"data": vals[id]},
		})
		go bus.Emit(context.Background(), "data-prj-config-load", b)
		return nil
	}); err != nil {
		t.Fatalf("桩应答订阅失败: %v", err)
	}
	if _, err := bus.On("data-prj-config-save", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil || m["ok"] != nil {
			return nil
		}
		reqID, _ := m["req_id"].(string)
		b, _ := json.Marshal(map[string]any{"req_id": reqID, "ok": true, "result": map[string]any{}})
		go bus.Emit(context.Background(), "data-prj-config-save", b)
		return nil
	}); err != nil {
		t.Fatalf("桩应答订阅失败: %v", err)
	}
	if _, err := bus.On("mcp-tools-register", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var p map[string]any
		_ = json.Unmarshal(v.Payload, &p)
		cap.mu.Lock()
		cap.regs = append(cap.regs, p)
		cap.mu.Unlock()
		v.Result = map[string]any{"registered": true, "name": p["name"]}
		return nil
	}); err != nil {
		t.Fatalf("桩注册订阅失败: %v", err)
	}
	if _, err := bus.On("mcp-tools-unregister", 0, func(_ context.Context, _ string, v *mq.Value) error {
		cap.mu.Lock()
		cap.uns++
		cap.mu.Unlock()
		v.Result = map[string]any{"unregistered": true}
		return nil
	}); err != nil {
		t.Fatalf("桩注销订阅失败: %v", err)
	}
	return cap
}

func gitCmd(wd string, args ...string) (string, error) {
	all := append([]string{"-C", wd}, args...)
	out, err := exec.Command("git", all...).CombinedOutput()
	return string(out), err
}

func initRepo(t *testing.T) string {
	t.Helper()
	wd := t.TempDir()
	if out, err := gitCmd(wd, "init", "-q"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	_, _ = gitCmd(wd, "config", "user.email", "test@chonkpilot.local")
	_, _ = gitCmd(wd, "config", "user.name", "chonkpilot-test")
	return wd
}

// refExists 该 workdir 是否存在检查点 ref（slug = 根会话标识）。
func refExists(wd, slug string) bool {
	_, err := gitCmd(wd, "rev-parse", "--verify", "--quiet", "refs/chonkpilot/"+slug)
	return err == nil
}

func chainLen(wd, slug string) int {
	out, err := gitCmd(wd, "rev-list", "--count", "refs/chonkpilot/"+slug)
	if err != nil {
		return 0
	}
	var n int
	_, _ = fmt.Sscan(strings.TrimSpace(out), &n)
	return n
}

func headCommit(wd string) string {
	out, _ := gitCmd(wd, "rev-parse", "HEAD")
	return strings.TrimSpace(out)
}

// waitFor 轮询直到 cond 为 true 或超时。
func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// startHistory 建总线 + 插件 + 桩；返回（bus, 注册捕获）。
func startHistory(t *testing.T, wd, enabled string) (mq.Bus, *regCapture) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	cap := stubPrjConfig(t, bus, map[string]string{"history.enabled": enabled})
	if err := history.New().Start(plugin.Deps{Bus: bus, Logf: t.Logf}); err != nil {
		t.Fatalf("history Start: %v", err)
	}
	pubJSON(bus, "instance-register", map[string]any{
		"instance_id": "ins-l2", "client_type": "unittest", "work_dir": wd,
	})
	return bus, cap
}

// preHook 发一次前置打点钩子（执行工具前）。
func preHook(bus mq.Bus, session, top, tool string) {
	ctx := map[string]any{"instance_id": "ins-l2", "session": session, "turn": "turn-l2"}
	if top != "" {
		ctx["top_session"] = top
	}
	pubJSON(bus, "history-pre-tool-hook", map[string]any{"tool": tool, "context": ctx})
}

// dirty 发一次文件变更广播（置脏）。
func dirty(bus mq.Bus, wd, name string) {
	pubJSON(bus, "filesys.changed", map[string]any{"work_dir": wd, "path": name, "operation": "write"})
}

// TestHistoryDisabledNoCheckpointNoTools：history.enabled 缺失/非 "true" → 不打点、不注册工具。
func TestHistoryDisabledNoCheckpointNoTools(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不可用")
	}
	wd := initRepo(t)
	bus, cap := startHistory(t, wd, "") // 键缺失 → 关闭
	if err := os.WriteFile(filepath.Join(wd, "a.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty(bus, wd, "a.txt")
	preHook(bus, "s1", "", "file_write")
	// 有界观察窗口内：不得建链、不得注册工具
	if waitFor(func() bool { return refExists(wd, "s1") }, 300*time.Millisecond) {
		t.Fatal("未启用不应打点")
	}
	if len(cap.regsList()) != 0 {
		t.Fatalf("未启用不应注册工具，got %d", len(cap.regsList()))
	}
}

// TestHistoryPreHookCheckpointsAndNoUserCommit：启用 + git 仓库 → 前置钩子打点建链；
// 工具注册载荷带 pre_hook_subject；**不在用户分支产生提交**。
func TestHistoryPreHookCheckpointsAndNoUserCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不可用")
	}
	wd := initRepo(t)
	if err := os.WriteFile(filepath.Join(wd, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = gitCmd(wd, "add", ".")
	_, _ = gitCmd(wd, "commit", "-q", "-m", "init")
	head0 := headCommit(wd)

	bus, cap := startHistory(t, wd, "true")
	// 等异步回读落定 → 工具注册（4 个，声明 pre_hook_subject）
	if !waitFor(func() bool { return len(cap.regsList()) == 4 }, 3*time.Second) {
		t.Fatalf("应注册 4 个工具，got %d", len(cap.regsList()))
	}
	for _, p := range cap.regsList() {
		if p["pre_hook_subject"] != "history-pre-tool-hook" {
			t.Fatalf("注册载荷缺 pre_hook_subject: %+v", p)
		}
		if p["handler_subject"] != "history-tool-call" {
			t.Fatalf("注册载荷 handler_subject 不符: %+v", p)
		}
	}
	// 脏 → 前置钩子打点
	if err := os.WriteFile(filepath.Join(wd, "a.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty(bus, wd, "a.txt")
	preHook(bus, "s1", "", "file_write")
	if !waitFor(func() bool { return chainLen(wd, "s1") == 1 }, 3*time.Second) {
		t.Fatalf("前置钩子应打点 1 次，got %d", chainLen(wd, "s1"))
	}
	// 检查点 message 含 tool/ts
	out, _ := gitCmd(wd, "log", "--format=%s", "-n", "1", "refs/chonkpilot/s1")
	if !strings.Contains(out, "chonk-ckpt:") || !strings.Contains(out, "tool=file_write") {
		t.Fatalf("检查点 message 不符: %s", out)
	}
	// 无变更（不脏）→ 前置钩子不打点
	preHook(bus, "s1", "", "file_write")
	time.Sleep(200 * time.Millisecond)
	if chainLen(wd, "s1") != 1 {
		t.Fatalf("不脏不应再打点，got %d", chainLen(wd, "s1"))
	}
	// 用户分支 HEAD 未被改动（打点不产生分支提交）
	if got := headCommit(wd); got != head0 {
		t.Fatalf("用户分支 HEAD 被改动: %s → %s", head0, got)
	}
}

// TestHistorySessionCompleteBackfill：主轮次 session-complete → 轮末补点（脏时）。
func TestHistorySessionCompleteBackfill(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不可用")
	}
	wd := initRepo(t)
	bus, cap := startHistory(t, wd, "true")
	if !waitFor(func() bool { return len(cap.regsList()) == 4 }, 3*time.Second) {
		t.Fatal("工具应注册")
	}
	// 前置钩子建首个点（登记会话归属）
	if err := os.WriteFile(filepath.Join(wd, "a.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty(bus, wd, "a.txt")
	preHook(bus, "s-main", "", "file_write")
	if !waitFor(func() bool { return chainLen(wd, "s-main") == 1 }, 3*time.Second) {
		t.Fatal("前置钩子应打点")
	}
	// 再次变更 + 主轮次终态 → 轮末补点
	if err := os.WriteFile(filepath.Join(wd, "a.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty(bus, wd, "a.txt")
	pubJSON(bus, "session-complete", map[string]any{"session": "s-main", "turn": "turn-l2", "status": "complete"})
	if !waitFor(func() bool { return chainLen(wd, "s-main") == 2 }, 3*time.Second) {
		t.Fatalf("轮末补点后链长应为 2，got %d", chainLen(wd, "s-main"))
	}
}

// TestHistorySkipsNonGitRepo：非 git 仓库 → 不打点、不注册工具（工具摘除）。
func TestHistorySkipsNonGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不可用")
	}
	wd := t.TempDir() // 非 git 仓库
	bus, cap := startHistory(t, wd, "true")
	if err := os.WriteFile(filepath.Join(wd, "a.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty(bus, wd, "a.txt")
	preHook(bus, "s1", "", "file_write")
	if waitFor(func() bool { return len(cap.regsList()) > 0 }, 300*time.Millisecond) {
		t.Fatal("非 git 仓库不应注册工具")
	}
	if refExists(wd, "s1") {
		t.Fatal("非 git 仓库不应打点")
	}
}

// TestHistoryDisabledWithoutGit：git 不可用 → Start 返回 nil（功能禁用，非启动失败）。
func TestHistoryDisabledWithoutGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // 清空 PATH → LookPath("git") 必然失败
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer bus.Close()
	if err := history.New().Start(plugin.Deps{Bus: bus, Logf: t.Logf}); err != nil {
		t.Fatalf("git 缺失时应禁用而非失败: %v", err)
	}
}
