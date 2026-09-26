// A3 运行时扩展面测试：data-session-* 写/读原语（ensure-session/ensure-turn/append-message/
// set-summary/complete-turn/cleanup-stale/load-messages）+ data-snapshot-*（get/set）+
// data-tasktree-upsert。总线直驱 persist，落库断言经直连 prj 库（regInstance 已 data.Register）。
// 外部模块黑盒（package persist_test）：经 persist.New/Start/Stop + 总线 data-* 主题驱动；
// Record 值转字符串用导出等价 persist.Sval（原白盒 svalOf/Sval）。
package persist_test

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/persist"
)

// TestSessionRuntimePrimitives：会话写原语幂等 + 消息往返（load-messages 还原 tool_calls/kind）。
func TestSessionRuntimePrimitives(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	sess := func(action string, data map[string]any) map[string]any {
		return dataCall(t, bus, "data-session-"+action, map[string]any{"instance_id": "ins-test", "data": data})
	}

	// ensure-session 幂等
	okf := dataResult(t, sess("ensure-session", map[string]any{"session_id": "s1"}))
	if !boolv(okf["ok"]) {
		t.Fatal("ensure-session ok=false")
	}
	dataResult(t, sess("ensure-session", map[string]any{"session_id": "s1"})) // 二次幂等
	// ensure-turn + append-message（msg 嵌套 + 扁平）→ load-messages
	dataResult(t, sess("ensure-turn", map[string]any{"turn_id": "t1", "session_id": "s1"}))
	dataResult(t, sess("append-message", map[string]any{
		"turn_id": "t1", "role": "user", "kind": "text", "content": "问题",
	}))
	dataResult(t, sess("append-message", map[string]any{
		"turn_id": "t1",
		"msg": map[string]any{
			"role":    "assistant",
			"content": "回答",
			"tool_calls": []any{map[string]any{
				"id": "c1", "type": "function",
				"function": map[string]any{"name": "tool_x", "arguments": "{}"},
			}},
		},
	}))
	lm := dataResult(t, sess("load-messages", map[string]any{"turn_id": "t1"}))
	msgs, _ := lm["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("load-messages len=%d, want 2: %+v", len(msgs), msgs)
	}
	m0, _ := msgs[0].(map[string]any)
	if persist.Sval(m0["role"]) != "user" || persist.Sval(m0["kind"]) != "text" {
		t.Fatalf("m0 role/kind mismatch: %+v", m0)
	}
	m1, _ := msgs[1].(map[string]any)
	if persist.Sval(m1["role"]) != "assistant" {
		t.Fatalf("m1 role mismatch: %+v", m1)
	}
	tcs, _ := m1["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("m1 tool_calls not restored: %+v", m1)
	}
	tc0, _ := tcs[0].(map[string]any)
	if persist.Sval(tc0["id"]) != "c1" {
		t.Fatalf("tool_call id mismatch: %+v", tc0)
	}

	// set-summary + complete-turn → turns 表直连断言
	dataResult(t, sess("set-summary", map[string]any{"turn_id": "t1", "summary": "摘要"}))
	dataResult(t, sess("complete-turn", map[string]any{"turn_id": "t1", "status": "done", "finish_reason": "stop"}))
	prj, _ := data.PrjUsr("ins-test")
	var rec data.Record
	if ok, _ := prj.Table("turns").Get("t1", &rec); !ok {
		t.Fatal("turn t1 missing")
	}
	if persist.Sval(rec["status"]) != "done" || persist.Sval(rec["finish_reason"]) != "stop" {
		t.Fatalf("turn status mismatch: %+v", rec)
	}
	if persist.Sval(rec["summary"]) != "摘要" {
		t.Fatalf("turn summary mismatch: %+v", rec)
	}
	if persist.Sval(rec["session_id"]) != "s1" {
		t.Fatalf("turn session mismatch: %+v", rec)
	}
}

// TestSessionCompleteTurnTokens（P3）：`data-session-complete-turn` 的 payload **只增**字段
// `full_tokens`/`brief_tokens` —— 写入往返（turns 行直连 + `data-session-history` 回读）；
// 未携带（如 ReopenTurn 复用本面置回 running）→ **不写该键**（不把「未提供」当 0）。
func TestSessionCompleteTurnTokens(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	sess := func(action string, data map[string]any) map[string]any {
		return dataResult(t, dataCall(t, bus, "data-session-"+action,
			map[string]any{"instance_id": "ins-test", "data": data}))
	}
	sess("ensure-session", map[string]any{"session_id": "s-tok"})
	sess("ensure-turn", map[string]any{"turn_id": "t-tok", "session_id": "s-tok"})
	sess("complete-turn", map[string]any{
		"turn_id": "t-tok", "status": "done", "finish_reason": "stop",
		"full_tokens": 1234, "brief_tokens": 56,
	})
	prj, _ := data.PrjUsr("ins-test")
	var rec data.Record
	if ok, _ := prj.Table("turns").Get("t-tok", &rec); !ok {
		t.Fatal("turn t-tok missing")
	}
	if persist.Sval(rec["full_tokens"]) != "1234" || persist.Sval(rec["brief_tokens"]) != "56" {
		t.Fatalf("预存 token 落库不符：%+v", rec)
	}
	// data-session-history 回读（门面 Turn 透出预存值）
	h := dataResult(t, dataCall(t, bus, "data-session-history",
		map[string]any{"instance_id": "ins-test", "data": map[string]any{"session_id": "s-tok"}}))
	messages, _ := h["messages"].(map[string]any)
	turns, _ := messages["turns"].([]any)
	if len(turns) != 1 {
		t.Fatalf("history turns=%+v", messages)
	}
	t0, _ := turns[0].(map[string]any)
	if f, _ := t0["full_tokens"].(float64); int(f) != 1234 {
		t.Fatalf("history full_tokens=%v, want 1234", t0["full_tokens"])
	}
	if b, _ := t0["brief_tokens"].(float64); int(b) != 56 {
		t.Fatalf("history brief_tokens=%v, want 56", t0["brief_tokens"])
	}
	// 未携带 token 的置回（ReopenTurn 语义）→ 不写该键。
	sess("ensure-turn", map[string]any{"turn_id": "t-reopen", "session_id": "s-tok"})
	sess("complete-turn", map[string]any{"turn_id": "t-reopen", "status": "running", "finish_reason": ""})
	var rec2 data.Record
	if ok, _ := prj.Table("turns").Get("t-reopen", &rec2); !ok {
		t.Fatal("turn t-reopen missing")
	}
	if _, exists := rec2["full_tokens"]; exists {
		t.Fatalf("未提供 token 不应写键：%+v", rec2)
	}
}

// TestSessionContextTurnTokens（P3）：`data-session-context` 结果**只增**伴随数组 `turn_tokens`
// （每项 {turn_id, full?, brief?}）——往返（预存值透出 / 缺值省略键）+ exclude_turn 排除 +
// **旧载荷兼容**（无轮次时不写该键）。
func TestSessionContextTurnTokens(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	sess := func(action string, data map[string]any) map[string]any {
		return dataResult(t, dataCall(t, bus, "data-session-"+action,
			map[string]any{"instance_id": "ins-test", "data": data}))
	}
	sess("ensure-session", map[string]any{"session_id": "s-ctx"})
	sess("ensure-turn", map[string]any{"turn_id": "t1", "session_id": "s-ctx"})
	sess("append-message", map[string]any{"turn_id": "t1", "role": "user", "kind": "text", "content": "q1"})
	// t1 预存 token；t2 只有 runnning（无预存值 → 字段省略键）。
	sess("complete-turn", map[string]any{
		"turn_id": "t1", "status": "done", "finish_reason": "stop",
		"full_tokens": 1234, "brief_tokens": 56,
	})
	sess("ensure-turn", map[string]any{"turn_id": "t2", "session_id": "s-ctx"})
	sess("append-message", map[string]any{"turn_id": "t2", "role": "user", "kind": "text", "content": "q2"})

	ctx := sess("context", map[string]any{"session_id": "s-ctx"})
	if ctx["messages"] == nil {
		t.Fatalf("既有 messages 字段应保持：%+v", ctx)
	}
	toks, _ := ctx["turn_tokens"].([]any)
	if len(toks) != 2 {
		t.Fatalf("turn_tokens 应为 2 项（升序）：%+v", ctx["turn_tokens"])
	}
	t0, _ := toks[0].(map[string]any)
	if t0["turn_id"] != "t1" || intOf(t0["full"]) != 1234 || intOf(t0["brief"]) != 56 {
		t.Fatalf("t1 预存 token 应透出：%+v", t0)
	}
	t1, _ := toks[1].(map[string]any)
	if t1["turn_id"] != "t2" {
		t.Fatalf("t2 应位列升序第 2：%+v", t1)
	}
	// 缺值 → **省略键**（消费方回退实时估算，绝不把缺值当 0）。
	if _, ok := t1["full"]; ok {
		t.Fatalf("t2 无预存值不应写 full 键：%+v", t1)
	}
	if _, ok := t1["brief"]; ok {
		t.Fatalf("t2 无预存值不应写 brief 键：%+v", t1)
	}
	// exclude_turn → 该轮从伴随数组一并排除。
	ex := sess("context", map[string]any{"session_id": "s-ctx", "exclude_turn": "t2"})
	exToks, _ := ex["turn_tokens"].([]any)
	if len(exToks) != 1 {
		t.Fatalf("exclude_turn 应排除该轮：%+v", ex["turn_tokens"])
	}
	if e0, _ := exToks[0].(map[string]any); e0["turn_id"] != "t1" {
		t.Fatalf("排除后应仅剩 t1：%+v", e0)
	}
	// 无轮次会话 → 不写 turn_tokens 键（旧载荷逐字节等价）。
	sess("ensure-session", map[string]any{"session_id": "s-empty"})
	empty := sess("context", map[string]any{"session_id": "s-empty"})
	if _, ok := empty["turn_tokens"]; ok {
		t.Fatalf("无轮次不应写 turn_tokens 键：%+v", empty)
	}
}

// TestSessionCleanupStale：遗留 running turn → cleanup-stale 标 interrupted。
func TestSessionCleanupStale(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	// 直连种子写 running turn
	prj, _ := data.PrjUsr("ins-test")
	tb := prj.Table("turns")
	if err := tb.Upsert("t-stale", data.Record{
		"turn_id": "t-stale", "session_id": "s-stale", "status": "running",
	}); err != nil {
		t.Fatal(err)
	}
	r := dataCall(t, bus, "data-session-cleanup-stale", map[string]any{"instance_id": "ins-test"})
	res := dataResult(t, r)
	if n, _ := res["count"].(float64); n != 1 {
		t.Fatalf("cleanup count=%v, want 1", res["count"])
	}
	var rec data.Record
	if ok, _ := tb.Get("t-stale", &rec); !ok {
		t.Fatal("t-stale missing")
	}
	if persist.Sval(rec["status"]) != "interrupted" {
		t.Fatalf("t-stale status=%s, want interrupted", persist.Sval(rec["status"]))
	}
}

// TestSnapshotGetSet：快照往返 + 无快照 → null。
func TestSnapshotGetSet(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	snap := map[string]any{
		"history": []any{
			map[string]any{"role": "system", "content": "[已压缩早前对话] sum"},
			map[string]any{"role": "user", "kind": "text", "content": "hi"},
		},
		"snapshot_turn": "t9",
	}
	dataCall(t, bus, "data-snapshot-set", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"session_id": "s-snap", "snapshot": snap},
	})
	g := dataResult(t, dataCall(t, bus, "data-snapshot-get", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"session_id": "s-snap"},
	}))
	got, _ := g["snapshot"].(map[string]any)
	if got == nil || persist.Sval(got["snapshot_turn"]) != "t9" {
		t.Fatalf("snapshot get mismatch: %+v", g)
	}
	hist, _ := got["history"].([]any)
	if len(hist) != 2 {
		t.Fatalf("snapshot history len=%d", len(hist))
	}
	// 无快照会话 → snapshot=null
	g2 := dataResult(t, dataCall(t, bus, "data-snapshot-get", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"session_id": "no-snap"},
	}))
	if v, ok := g2["snapshot"]; !ok || v != nil {
		t.Fatalf("no-snapshot want null, got %+v", g2)
	}
}

// TestTasktreeUpsert：节点落库（kind llm→session / tool→tool）+ list 读回。
func TestTasktreeUpsert(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	upsert := func(data map[string]any) {
		t.Helper()
		dataCall(t, bus, "data-tasktree-upsert", map[string]any{
			"instance_id": "ins-test", "data": data,
		})
	}
	upsert(map[string]any{
		"task_id": "n-llm", "top_session": "top1", "session_id": "s-top1",
		"kind": "llm", "title": "会话节点", "status": "running", "created_at": "2026-01-01T00:00:00Z",
	})
	upsert(map[string]any{
		"task_id": "n-tool", "top_session": "top1", "session_id": "s-top1",
		"parent_node_id": "n-llm", "kind": "core_file_read", "title": "读文件",
		"status": "done", "created_at": "2026-01-01T00:00:01Z",
		"finished_at": "2026-01-01T00:00:02Z", "tool_call_id": "tc-1",
	})
	prj, _ := data.PrjUsr("ins-test")
	var rec data.Record
	if ok, _ := prj.Table("tasktree").Get("n-llm", &rec); !ok {
		t.Fatal("n-llm missing")
	}
	if persist.Sval(rec["node_type"]) != "session" {
		t.Fatalf("n-llm node_type=%s, want session", persist.Sval(rec["node_type"]))
	}
	if persist.Sval(rec["status"]) != "running" {
		t.Fatalf("n-llm status mismatch: %+v", rec)
	}
	if ok, _ := prj.Table("tasktree").Get("n-tool", &rec); !ok {
		t.Fatal("n-tool missing")
	}
	if persist.Sval(rec["node_type"]) != "tool" || persist.Sval(rec["tool_call_id"]) != "tc-1" {
		t.Fatalf("n-tool mismatch: %+v", rec)
	}
	r := dataResult(t, dataCall(t, bus, "data-tasktree-list", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"top_session": "top1", "mode": "init"},
	}))
	nodes, _ := r["nodes"].([]any)
	if len(nodes) != 1 { // mode=init 仅 llm/running 存活
		t.Fatalf("init list len=%d, want 1", len(nodes))
	}
}

func boolv(v any) bool {
	b, _ := v.(bool)
	return b
}

// intOf 取 JSON 数字（float64）整数值（缺值/非数值 → 0；测试内部 helper）。
func intOf(v any) int {
	f, _ := v.(float64)
	return int(f)
}
