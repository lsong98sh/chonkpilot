package workspace

import (
	"testing"

	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
)

// TestRouterScopeSubscriptions 验证一个窗口多 workdir 的订阅路由：
//   - 只投命中 scope 的 sink；
//   - 无 scope 的全局消息投所有 sink（各一次）；
//   - 定向消息仍投目标实例（即使未订阅该 scope）。
func TestRouterScopeSubscriptions(t *testing.T) {
	r := NewRouter()
	r.Subscribe("sink-a", Scope{WorkDir: `E:\A`})
	r.Subscribe("sink-b", Scope{WorkDir: `E:\B`})
	sinks := []Sink{{ID: "sink-a", WorkDir: `E:\A`}, {ID: "sink-b", WorkDir: `E:\B`}}

	ids := func(ts []Target) []string {
		out := make([]string, 0, len(ts))
		for _, t := range ts {
			out = append(out, t.ID)
		}
		return out
	}

	// B 项目事件 → 只投 sink-b。
	got := ids(r.Route("llm-receive", []byte(`{"work_dir":"E:\\B","session_id":"s-b"}`), sinks))
	if len(got) != 1 || got[0] != "sink-b" {
		t.Fatalf("B 项目事件应只投 sink-b，实际 %v", got)
	}

	// 全局消息（无 scope） → 两个 sink 各一次。
	got = ids(r.Route("llm-complete", []byte(`{"status":"complete"}`), sinks))
	if len(got) != 2 {
		t.Fatalf("全局消息应投 2 个 sink，实际 %v", got)
	}

	// 定向消息（session-ask）→ 目标实例必投；同时命中 scope 的订阅者也投（B 订阅者）。
	got = ids(r.Route(msgkeys.TopicSessionAsk, []byte(`{"instance_id":"sink-a","work_dir":"E:\\B"}`), sinks))
	if len(got) != 2 || got[0] != "sink-a" || got[1] != "sink-b" {
		t.Fatalf("定向消息应投目标实例 + 命中 scope 的订阅者，实际 %v", got)
	}

	// workdir-opened/closed = 项目注册表生命周期 → 广播所有窗口（不被 work_dir 收口）。
	got = ids(r.Route(msgkeys.TopicWorkdirOpened, []byte(`{"work_dir":"E:\\B"}`), sinks))
	if len(got) != 2 {
		t.Fatalf("workdir-opened 应广播所有 sink，实际 %v", got)
	}
}

// TestScopeMatchPrecision 验证 session 订阅是精确匹配（不含子会话）：
// 消息 scope 的 session_id 必须与订阅一致；不按 dialog/树推导。
func TestScopeMatchPrecision(t *testing.T) {
	sub := Scope{WorkDir: `E:\B`, SessionID: "s-1"}
	if !(Scope{WorkDir: `E:\B`, SessionID: "s-1"}).Match(sub) {
		t.Fatal("同 workdir + 同 session 应命中")
	}
	if (Scope{WorkDir: `E:\B`, SessionID: "s-2"}).Match(sub) {
		t.Fatal("不同 session 不应命中")
	}
	if (Scope{WorkDir: `E:\A`, SessionID: "s-1"}).Match(sub) {
		t.Fatal("不同 workdir 不应命中")
	}
	if !(Scope{}).Match(sub) {
		t.Fatal("全局消息应投所有订阅")
	}
	if !(Scope{WorkDir: `E:\A`, SessionID: "s-9"}).Match(Scope{}) {
		t.Fatal("wildcard 订阅收所有消息")
	}
}

// TestRouterUnsubscribeStopsDelivery 验证退订后不再投递（页签关闭即退订）。
func TestRouterUnsubscribeStopsDelivery(t *testing.T) {
	r := NewRouter()
	r.Subscribe("sink-a", Scope{WorkDir: `E:\A`})
	r.Subscribe("sink-a", Scope{WorkDir: `E:\B`})
	sinks := []Sink{{ID: "sink-a", WorkDir: `E:\A`}}
	payload := []byte(`{"work_dir":"E:\\B","session_id":"s-b"}`)
	if got := r.Route("llm-receive", payload, sinks); len(got) != 1 {
		t.Fatalf("订阅后应收到 1 条，实际 %d", len(got))
	}
	r.Unsubscribe("sink-a", Scope{WorkDir: `E:\B`})
	if got := r.Route("llm-receive", payload, sinks); len(got) != 0 {
		t.Fatalf("退订后应收到 0 条，实际 %d", len(got))
	}
}
