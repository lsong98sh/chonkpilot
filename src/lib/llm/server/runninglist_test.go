package server

import (
	"context"
	"testing"

	"github.com/lsong98sh/chonkpilot-agent/session/runner"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// TestSplitInstKey：运行态键的拆分（instKey 的逆；无 NUL → instance 为空，兼容旧语义）。
func TestSplitInstKey(t *testing.T) {
	cases := []struct{ key, inst, id string }{
		{instKey("ins-a", "sess-1"), "ins-a", "sess-1"},
		{"legacy-sess", "", "legacy-sess"},
		{instKey("", "sess-2"), "", "sess-2"},
	}
	for _, c := range cases {
		inst, id := splitInstKey(c.key)
		if inst != c.inst || id != c.id {
			t.Fatalf("splitInstKey(%q) = (%q,%q)，期望 (%q,%q)", c.key, inst, id, c.inst, c.id)
		}
	}
}

// TestOnSessionRunningList：只读内存快照 —— 直接读 busy 注册表，不落库。
func TestOnSessionRunningList(t *testing.T) {
	s := &Server{busy: runner.NewRegistry[string]()}
	s.busy[instKey("ins-a", "sess-1")] = "turn-1"
	s.busy[instKey("ins-b", "sess-2")] = "turn-2"
	s.busy["legacy-sess"] = "turn-3" // instance 为空（旧调用方）

	v := &mq.Value{}
	if err := s.onSessionRunningList(context.Background(), "session-running-list", v); err != nil {
		t.Fatalf("handler: %v", err)
	}
	res, ok := v.Result.(map[string]any)
	if !ok {
		t.Fatalf("Result 应是 map，实际 %T", v.Result)
	}
	list, ok := res["list"].([]map[string]any)
	if !ok {
		t.Fatalf("list 应是 []map[string]any，实际 %T", res["list"])
	}
	if len(list) != 3 {
		t.Fatalf("应返回 3 条在跑会话，实际 %d: %+v", len(list), list)
	}
	got := map[string]string{} // session_id → turn
	for _, it := range list {
		got[str(it["session_id"])] = str(it["turn"])
	}
	for _, want := range []struct{ session, turn string }{
		{"sess-1", "turn-1"}, {"sess-2", "turn-2"}, {"legacy-sess", "turn-3"},
	} {
		if got[want.session] != want.turn {
			t.Fatalf("session %s 的 turn = %q，期望 %q（全量 %+v）", want.session, got[want.session], want.turn, list)
		}
	}

	// 空注册表 → 空数组（不是 nil），前端可直接遍历。
	empty := &Server{busy: runner.NewRegistry[string]()}
	ev := &mq.Value{}
	if err := empty.onSessionRunningList(context.Background(), "session-running-list", ev); err != nil {
		t.Fatalf("handler(empty): %v", err)
	}
	if n := len(ev.Result.(map[string]any)["list"].([]map[string]any)); n != 0 {
		t.Fatalf("空注册表应返回 0 条，实际 %d", n)
	}
}
