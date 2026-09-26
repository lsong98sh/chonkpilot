// 执行池按 instance 分桶白盒测试（2026-09-19 实例隔离第二批，缺口 2）：
//   - 两 instance 的任务**互不可见**（同名 tool_call_id 各自解析到本 instance 桶内任务）；
//   - **互不可取消**（用 B 的 instance 引用取消 A 的任务 → 明确 not found；A 的取消不影响 B）；
//   - `MaxTasks` 为**每 instance 上限**（A 打满不阻塞 B）。
//
// 复用既有 fake Provider（Call 阻塞在 release 上）+ 测试总线驱动（tools/call → pending），
// 与 async_test.go 同源；instance 空 = 旧单池语义由既有全部用例覆盖（行为不变）。
package mcpgateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// newIsolateGW 建带自定义 MaxTasks 的 gateway（同 newTestGW，仅放宽上限注入）。
func newIsolateGW(t *testing.T, max int) (mq.Bus, *Gateway) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	g, err := New(Params{Bus: bus, CallTimeout: 5 * time.Second, MaxTasks: max, Logf: t.Logf})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = g.Stop(context.Background())
		bus.Close()
	})
	return bus, g
}

// TestIsolateExecPoolInvisibleAndUncancelableAcrossInstances：同名 tool_call_id、不同 instance →
// 各自桶内独立任务；跨 instance 的引用**不可见、不可取消**（缺口 2）。
func TestIsolateExecPoolInvisibleAndUncancelableAcrossInstances(t *testing.T) {
	bus, g := newIsolateGW(t, 8)
	fk := newFakeProvider("iso", OriginBuiltin)
	addFakeProvider(t, g, fk, "run")

	// 两 instance 用**完全相同**的 tool_call_id（最严格的隔离判据）
	outA := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "iso_run", "instance_id": "ins-a", "tool_call_id": "tc-dup",
		"arguments": map[string]any{"async": "always"},
	})
	evA := waitPending(t, outA)
	outB := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "iso_run", "instance_id": "ins-b", "tool_call_id": "tc-dup",
		"arguments": map[string]any{"async": "always"},
	})
	evB := waitPending(t, outB)
	if evA == evB {
		t.Fatalf("两 instance 的任务应各自建条目（gw task id 全局唯一）: %s", evA)
	}

	// ① 互不可见：同名 tool_call_id 在各自桶内解析到本 instance 的任务；跨桶查不到
	g.tm.mu.Lock()
	locA := g.tm.locate("ins-a", "tc-dup")
	locB := g.tm.locate("ins-b", "tc-dup")
	g.tm.mu.Unlock()
	if locA == nil || locA.ID != evA {
		t.Fatalf("ins-a 的 tc-dup 应命中 A(%s)，got %+v", evA, locA)
	}
	if locB == nil || locB.ID != evB {
		t.Fatalf("ins-b 的 tc-dup 应命中 B(%s)，got %+v", evB, locB)
	}
	if _, ok := g.tm.statusMap("ins-a", evB); ok {
		t.Fatalf("ins-a 不应看到 ins-b 的任务 %s", evB)
	}
	if _, ok := g.tm.statusMap("ins-b", evA); ok {
		t.Fatalf("ins-b 不应看到 ins-a 的任务 %s", evA)
	}

	// ② 互不可取消：用 ins-b 的归属取消 ins-a 的任务 → not found（不动任何任务）
	if ok, err := g.CancelExec("ins-b", evA); err == nil || ok {
		t.Fatalf("跨 instance 取消应被拒: ok=%v err=%v", ok, err)
	}
	if st := poolStatus(t, g, evA); !nonTerminal(st) {
		t.Fatalf("A 不应被 B 的取消影响: %v", st)
	}
	// 本 instance 取消生效，且不影响 B
	if ok, err := g.CancelExec("ins-a", "tc-dup"); err != nil || !ok {
		t.Fatalf("本 instance 取消应成功: ok=%v err=%v", ok, err)
	}
	if st := awaitTerminal(t, g, evA); st["state"] != "cancelled" {
		t.Fatalf("A state = %v, want cancelled", st["state"])
	}
	if st := poolStatus(t, g, evB); !nonTerminal(st) {
		t.Fatalf("B 不应被 A 的取消影响: %v", st)
	}

	// 清理：放行并取消 B，避免 goroutine 泄漏
	if ok, err := g.CancelExec("ins-b", evB); err != nil || !ok {
		t.Fatalf("B 取消失败: ok=%v err=%v", ok, err)
	}
	close(fk.release)
}

// TestMaxTasksPerInstanceIndependent：`MaxTasks` = 每 instance 上限 —— A 打满后 A 的后续调用被拒，
// 但 B 仍可正常起任务（计数独立，缺口 2）。
func TestMaxTasksPerInstanceIndependent(t *testing.T) {
	bus, g := newIsolateGW(t, 1)
	fk := newFakeProvider("cap", OriginBuiltin)
	addFakeProvider(t, g, fk, "run")

	outA := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "cap_run", "instance_id": "ins-a", "tool_call_id": "tc-a1",
		"arguments": map[string]any{"async": "always"},
	})
	taskA := waitPending(t, outA)

	// ins-a 第二个任务 → 超本 instance 上限
	err := gwCallErr(bus, "tools/call", map[string]any{
		"name": "cap_run", "instance_id": "ins-a", "tool_call_id": "tc-a2",
		"arguments": map[string]any{"async": "always"},
	})
	if err == nil || !strings.Contains(err.Error(), "并发任务数已达上限") {
		t.Fatalf("ins-a 超上限应被拒，got err=%v", err)
	}

	// ins-b 不受 ins-a 计数影响 → 正常起任务
	outB := gwCallAsync(bus, "tools/call", map[string]any{
		"name": "cap_run", "instance_id": "ins-b", "tool_call_id": "tc-b1",
		"arguments": map[string]any{"async": "always"},
	})
	taskB := waitPending(t, outB)
	if taskB == taskA {
		t.Fatalf("两 instance 应各建任务: %s", taskB)
	}

	close(fk.release)
	_ = awaitTerminal(t, g, taskA)
	_ = awaitTerminal(t, g, taskB)
}

// waitPending 等待 tools/call 返回 pending 并取 task_id（缺口 2 用例辅助）。
func waitPending(t *testing.T, out <-chan gwOutcome) string {
	t.Helper()
	select {
	case o := <-out:
		if o.err != nil {
			t.Fatalf("tools/call: %v", o.err)
		}
		return gwPendingTaskID(t, o.res)
	case <-time.After(5 * time.Second):
		t.Fatal("tools/call 未在 5s 内返回 pending")
	}
	return ""
}

// nonTerminal 判定执行池快照是否为**非终态**（pending/running；spawn 与执行 goroutine 之间
// 的瞬态 pending 亦视为在飞）。
func nonTerminal(st map[string]any) bool {
	s, _ := st["state"].(string)
	return s == "pending" || s == "running"
}
