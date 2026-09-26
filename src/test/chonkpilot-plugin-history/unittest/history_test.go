// history 插件行为测试（黑盒外部版）：git 仓库下主会话 turn-start/llm-complete 提交快照；
// 非 git 仓库 → 记录 notRepo 不提交；子轮次（parents 非空）不提交；无变更不提交。
//
// 门控口径（2026-09-19，42 §2 (125)）：`history.enabled` **默认不开启** —— 只有显式 "true"
// 才提交（缺失/非法 = 关闭）。本文件以桩应答 `data-prj-config-load` 构造两种启动态：
//
//	· 缺省（读回 ""）→ 断言**不产生任何 git 提交**；
//	· 显式 "true" → 断言轮次边界正常提交；
//	· 显式 "false" → 断言不提交。
//
// 外迁说明：mq 为进程内同步派发（Emit 派发即全部订阅 handler 已执行），原测试经
// h.im / h.notRepo 私有字段的异步轮询同步不再需要——统一改为断言总线事件后的可观察
// 副作用（git 提交数 / .gitignore），覆盖语义与原用例一致，故无需导出任何源码标识符。
package history_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// stubPrjConfig 在总线上桩应答 `data-prj-config-load`（返回 value），模拟 persist 读 prj 键。
// 只在请求（无 ok 字段）时回应；应答经总线回发（`dataEmit` 按 req_id 关联收敛）。
func stubPrjConfig(t *testing.T, bus mq.Bus, value string) {
	t.Helper()
	_, err := bus.On("data-prj-config-load", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		if _, isReply := m["ok"]; isReply {
			return nil // 应答不回应答
		}
		reqID, _ := m["req_id"].(string)
		b, _ := json.Marshal(map[string]any{
			"req_id": reqID, "ok": true,
			"result": map[string]any{"data": value},
		})
		bus.Emit(context.Background(), "data-prj-config-load", b)
		return nil
	})
	if err != nil {
		t.Fatalf("桩应答订阅失败: %v", err)
	}
}

func gitCmd(wd string, args ...string) (string, error) {
	all := append([]string{"-C", wd}, args...)
	out, err := exec.Command("git", all...).CombinedOutput()
	return string(out), err
}

// commitCount 当前仓库提交数（无提交/非 git 仓库 → 0）。
func commitCount(wd string) int {
	out, err := gitCmd(wd, "rev-list", "--count", "HEAD")
	if err != nil {
		return 0
	}
	out = strings.TrimSpace(out)
	var n int
	_, _ = fmt.Sscan(out, &n)
	return n
}

// poll 轮询直到 cond 为 true 或超时。
func poll(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// initRepo 建临时 git 仓库并配 user（提交前置）。
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

// startHistory 建总线（带命名空间前缀，与生产一致）+ history 插件（wd 为 work_dir），
// 并确定门控为 enabled 语义：① 桩读 `data-prj-config-load` 返回 enabled；② 注册实例后再广播
// 一次 `data-prj-config-refresh`（同一值）→ **门控同步落定**（与异步回读结果一致，消除时序竞态）。
func startHistory(t *testing.T, wd string, enabled string) (mq.Bus, *history.History) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	stubPrjConfig(t, bus, enabled)
	h := history.New()
	if err := h.Start(plugin.Deps{Bus: bus, Logf: t.Logf}); err != nil {
		t.Fatalf("history Start: %v", err)
	}
	pubJSON(bus, "instance-register", map[string]any{
		"instance_id": "ins-h", "client_type": "unittest", "work_dir": wd,
	})
	pubJSON(bus, "data-prj-config-refresh", map[string]any{
		"id": "history.enabled", "op": "save",
		"list": map[string]any{"history.enabled": enabled},
	})
	return bus, h
}

// turnStart 主会话 turn-start（有变更即提交的触发点之一）。
func turnStart(bus mq.Bus, session, turn string) {
	pubJSON(bus, "session-turn-start", map[string]any{
		"instance_id": "ins-h", "session": session, "turn": turn, "parents": []string{},
	})
}

// waitCommits 反复触发主会话 turn-start，直至提交数 >= want（返回 true）或超时（false）。
// **缺陷检测**：门控回读由 goroutine 落定，落定前的首次触发可能被跳过——按有界轮询反复触发
// （与 [42 §2 (120)] 的「只改何时读」同口径，判定不放宽）。
func waitCommits(bus mq.Bus, wd string, want int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		turnStart(bus, "s1", "t1")
		if commitCount(wd) >= want {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// assertNoCommit 在 timeout 内反复触发主会话 turn-start，断言提交数**恒为 0**
// （一旦出现提交即失败 = 门控未生效）。
func assertNoCommit(t *testing.T, bus mq.Bus, wd string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		turnStart(bus, "s1", "t1")
		if n := commitCount(wd); n != 0 {
			t.Fatalf("不应提交却提交了: count=%d", n)
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestHistoryDefaultOffNoCommit：**缺省（history.enabled 缺失，读回 ""）→ 不产生任何 git 提交**
// —— turn-start 与 llm-complete 在 git 仓库且有未提交变更时均零提交（默认不开启，42 §2 (125)）。
func TestHistoryDefaultOffNoCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不可用")
	}
	wd := initRepo(t)
	bus, _ := startHistory(t, wd, "") // 键缺失 → 读回 ""
	if err := os.WriteFile(filepath.Join(wd, "a.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	pubJSON(bus, "session-complete", map[string]any{
		"instance_id": "ins-h", "session": "s1", "turn": "t2", "status": "complete",
	})
	// 反向断言：等待窗口内反复触发，提交数恒为 0（默认关闭）
	assertNoCommit(t, bus, wd, 800*time.Millisecond)
}

// TestHistoryExplicitFalseNoCommit：显式 "false" → 零提交。
func TestHistoryExplicitFalseNoCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不可用")
	}
	wd := initRepo(t)
	bus, _ := startHistory(t, wd, "false")
	if err := os.WriteFile(filepath.Join(wd, "a.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertNoCommit(t, bus, wd, 800*time.Millisecond)
}

// TestHistoryCommitsOnTurnBoundary：显式开启（"true"）+ git 仓库 → 主会话 turn-start/llm-complete
// 提交；子轮次与无变更不提交；.gitignore 保证 .chonkpilot/ 不入库。
func TestHistoryCommitsOnTurnBoundary(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不可用")
	}
	wd := initRepo(t)

	bus, _ := startHistory(t, wd, "true")
	f := filepath.Join(wd, "a.txt")
	if err := os.WriteFile(f, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 主会话 turn-start → 有变更 → 提交
	if !waitCommits(bus, wd, 1, 3*time.Second) {
		t.Fatalf("turn-start 未提交: count=%d", commitCount(wd))
	}
	if commitCount(wd) != 1 {
		t.Fatalf("期望 1 次提交: count=%d", commitCount(wd))
	}
	if out, _ := gitCmd(wd, "log", "--oneline", "-1"); !strings.Contains(out, "chonk: snapshot") {
		t.Fatalf("提交信息不符: %s", out)
	}
	// .gitignore 已创建且忽略 .chonkpilot/
	gi := filepath.Join(wd, ".gitignore")
	if raw, err := os.ReadFile(gi); err != nil || !strings.Contains(string(raw), ".chonkpilot") {
		t.Fatalf(".gitignore 未忽略 .chonkpilot: %v %q", err, raw)
	}
	// 子轮次 turn-start（parents 非空）→ 不提交
	if err := os.WriteFile(f, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	pubJSON(bus, "session-turn-start", map[string]any{
		"instance_id": "ins-h", "session": "sub-s", "turn": "t2", "parents": []string{"s1"},
	})
	time.Sleep(300 * time.Millisecond)
	if commitCount(wd) != 1 {
		t.Fatalf("子轮次不应提交: count=%d", commitCount(wd))
	}
	// 主会话 llm-complete（有变更）→ 提交
	pubJSON(bus, "session-complete", map[string]any{
		"instance_id": "ins-h", "session": "s1", "turn": "t3", "status": "complete",
	})
	if !poll(func() bool { return commitCount(wd) == 2 }, 3*time.Second) {
		t.Fatalf("llm-complete 未提交: count=%d", commitCount(wd))
	}
	// 无变更 → 不提交
	pubJSON(bus, "session-complete", map[string]any{
		"instance_id": "ins-h", "session": "s1", "turn": "t4", "status": "complete",
	})
	time.Sleep(300 * time.Millisecond)
	if commitCount(wd) != 2 {
		t.Fatalf("无变更不应提交: count=%d", commitCount(wd))
	}
}

// TestHistorySkipsNonGitRepo：非 git 仓库 → 记录 notRepo：首事件不提交（可观察无提交），
// 且同一 wd 事后 git init 成仓库后事件仍被跳过（证明 notRepo 已缓存，不会改判）。
func TestHistorySkipsNonGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不可用")
	}
	wd := t.TempDir()
	bus, _ := startHistory(t, wd, "true") // 显式开启（nil 保护：非 git 仓库仍不提交）
	if err := os.WriteFile(filepath.Join(wd, "a.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 事件 1：非 git 仓库 + 有变更 → 不提交（同步派发，Publish 返回即处理完）
	pubJSON(bus, "session-turn-start", map[string]any{
		"instance_id": "ins-h", "session": "s1", "turn": "t1",
	})
	if commitCount(wd) != 0 {
		t.Fatalf("非 git 仓库不应提交: count=%d", commitCount(wd))
	}
	// 事件 2：同一 wd 已 git init（有变更）→ notRepo 已记录 → 仍跳过不提交
	if out, err := gitCmd(wd, "init", "-q"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	_, _ = gitCmd(wd, "config", "user.email", "test@chonkpilot.local")
	_, _ = gitCmd(wd, "config", "user.name", "chonkpilot-test")
	if err := os.WriteFile(filepath.Join(wd, "a.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	pubJSON(bus, "session-complete", map[string]any{
		"instance_id": "ins-h", "session": "s1", "turn": "t2", "status": "complete",
	})
	if !poll(func() bool { return commitCount(wd) == 0 }, time.Second) {
		t.Fatalf("notRepo 已记录，git init 后仍不应提交: count=%d", commitCount(wd))
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
