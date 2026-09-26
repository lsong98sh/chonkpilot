// 桥按 instance 过滤事件白盒测试（2026-09-19 实例隔离第二批，缺口 8）：
// 同一条总线上两 instance 的事件流 → 桥**只把本 instance 的事件转发给前端**（A 的 tasks.* /
// mcp-* 不投给 B 的前端）；载荷无 `instance_id` 的事件（data-*/filesys.* 等按 workdir 管理）
// 与本实例未标识（instanceID 空）→ 放行（单 instance 行为与引入过滤前等价）。
package bridge

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// newFilterTestBridge 建桥并订阅总线（捕获 eval 到前端的脚本）。
func newFilterTestBridge(t *testing.T, instanceID string) (mq.Bus, *Bridge, func() []string) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	var mu sync.Mutex
	var scripts []string
	b := New(instanceID, "wd", "dd", func(script string) {
		mu.Lock()
		scripts = append(scripts, script)
		mu.Unlock()
	}, bus)
	if err := b.Start(); err != nil {
		t.Fatalf("bridge start: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	return bus, b, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, scripts...)
	}
}

// TestForwardEventFiltersOtherInstance：本实例 = ins-a —— ins-b 的 tasks.* / mcp-* 事件被丢弃，
// ins-a 的与无归属事件被转发。
func TestForwardEventFiltersOtherInstance(t *testing.T) {
	bus, _, scripts := newFilterTestBridge(t, "ins-a")

	emit := func(subject string, payload map[string]any) {
		bus.Emit(context.Background(), subject, payload).Wait()
	}
	emit("task-started", map[string]any{"instance_id": "ins-a", "task_id": "task-aaa"})
	emit("task-started", map[string]any{"instance_id": "ins-b", "task_id": "task-bbb"})
	emit("mcp-tools-timeout", map[string]any{"instance_id": "ins-b", "tool_call_id": "call-bbb"})
	emit("mcp-tools-timeout", map[string]any{"instance_id": "ins-a", "tool_call_id": "call-aaa"})
	emit("filesys.changed", map[string]any{"path": "/x/y"}) // 无 instance 归属 → 放行

	joined := strings.Join(scripts(), "\n")
	if !strings.Contains(joined, "task-aaa") || !strings.Contains(joined, "call-aaa") {
		t.Fatalf("本实例事件应转发：%s", joined)
	}
	if strings.Contains(joined, "task-bbb") || strings.Contains(joined, "call-bbb") {
		t.Fatalf("他实例事件不得投给本实例前端（缺口 8）：%s", joined)
	}
	if !strings.Contains(joined, "filesys.changed") {
		t.Fatalf("无 instance 归属的事件应放行（旧行为）：%s", joined)
	}
}

// TestForwardEventNoInstanceIDPassesAll：桥无 instanceID（未标识）→ 过滤条件恒真（单 instance
// 形态与引入过滤前逐字节等价）。
func TestForwardEventNoInstanceIDPassesAll(t *testing.T) {
	bus, _, scripts := newFilterTestBridge(t, "")

	bus.Emit(context.Background(), "task-started",
		map[string]any{"instance_id": "ins-a", "task_id": "task-aaa"}).Wait()
	bus.Emit(context.Background(), "task-started",
		map[string]any{"instance_id": "ins-b", "task_id": "task-bbb"}).Wait()

	joined := strings.Join(scripts(), "\n")
	if !strings.Contains(joined, "task-aaa") || !strings.Contains(joined, "task-bbb") {
		t.Fatalf("无 instanceID 时不应过滤：%s", joined)
	}
}
