// message_backfill_test.go — I-176：data-session-append-message 的「同一行回填」语义。
//   - key 非空 → 按该主键就地更新（running → 终态），保留原 created_at；
//   - role=tool 带 tool_call_id 且无 key → 复用同轮同 call 的既有行（重启后重试路径不产生重复）；
//   - assistant 分段落库（带 key）写同一行。
// 经总线驱动 persist（黑盒），落库断言经直连 prjusr 库（regInstance 已 data.Register）。
package persist_test

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/persist"
)

// msgRowsOfTurn 直连 prjusr 库读某轮全部消息行（created_at 升序）。
func msgRowsOfTurn(t *testing.T, turn string) []data.Record {
	t.Helper()
	db, err := data.PrjUsr("ins-test")
	if err != nil {
		t.Fatalf("PrjUsr: %v", err)
	}
	recs, _, err := db.Table("messages").Query(data.Query{
		Where: data.Record{"turn_id": turn}, OrderBy: "created_at",
	})
	if err != nil {
		t.Fatalf("query messages: %v", err)
	}
	return recs
}

// TestAppendMessageToolKeyedBackfill：带 key 的 running → 终态「就地回填」同一行（恒一行、
// created_at 保持）——role=tool 一行只落一次，不破坏 LLM 重放/前端卡片。
func TestAppendMessageToolKeyedBackfill(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	sess := func(action string, data map[string]any) map[string]any {
		return dataCall(t, bus, "data-session-"+action, map[string]any{"instance_id": "ins-test", "data": data})
	}
	dataResult(t, sess("ensure-session", map[string]any{"session_id": "s1"}))
	dataResult(t, sess("ensure-turn", map[string]any{"turn_id": "t1", "session_id": "s1"}))

	// ① 发起：running（content 仅 call 段）→ 结果回 id（= 主键）
	r1 := dataResult(t, sess("append-message", map[string]any{
		"turn_id": "t1",
		"msg": map[string]any{
			"role": "tool", "tool_call_id": "c1", "tool_call_status": "running",
			"content": `{"call":{"tool_call_id":"c1","name":"t_x"}}`,
		},
	}))
	key := persist.Sval(r1["id"])
	if key == "" {
		t.Fatalf("append-message 应回 id：%+v", r1)
	}
	rows := msgRowsOfTurn(t, "t1")
	if len(rows) != 1 || persist.Sval(rows[0]["tool_call_status"]) != "running" {
		t.Fatalf("发起后应有 1 行 running: %+v", rows)
	}
	created := persist.Sval(rows[0]["created_at"])

	// ② 终态回填（带 key）→ 同一行、状态终态、created_at 不变、无第二行
	r2 := dataResult(t, sess("append-message", map[string]any{
		"turn_id": "t1", "key": key,
		"msg": map[string]any{
			"role": "tool", "tool_call_id": "c1", "tool_call_status": "completed",
			"content": `{"call":{"tool_call_id":"c1","name":"t_x"},"result":{"content":"ok","status":"completed"}}`,
		},
	}))
	if got := persist.Sval(r2["id"]); got != key {
		t.Fatalf("回填应回同一主键 %q，得 %q", key, got)
	}
	rows = msgRowsOfTurn(t, "t1")
	if len(rows) != 1 {
		t.Fatalf("回填后应仍为 1 行（不得新增）：%+v", rows)
	}
	if st := persist.Sval(rows[0]["tool_call_status"]); st != "completed" {
		t.Fatalf("终态状态=%q，want completed", st)
	}
	if got := persist.Sval(rows[0]["created_at"]); got != created {
		t.Fatalf("回填应保留原 created_at：%q → %q", created, got)
	}
}

// TestAppendMessageToolReuseWithoutKey：role=tool 带 tool_call_id 且**不带 key** →
// 复用同轮同 call 的既有行（重启后重试路径无内存主键，避免重复 tool_pair）。
func TestAppendMessageToolReuseWithoutKey(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	sess := func(action string, data map[string]any) map[string]any {
		return dataCall(t, bus, "data-session-"+action, map[string]any{"instance_id": "ins-test", "data": data})
	}
	dataResult(t, sess("ensure-session", map[string]any{"session_id": "s1"}))
	dataResult(t, sess("ensure-turn", map[string]any{"turn_id": "t1", "session_id": "s1"}))

	dataResult(t, sess("append-message", map[string]any{
		"turn_id": "t1",
		"msg": map[string]any{
			"role": "tool", "tool_call_id": "c2", "tool_call_status": "running",
			"content": `{"call":{"tool_call_id":"c2","name":"t_y"}}`,
		},
	}))
	// 无 key 的终态回填 → 按 (turn_id, tool_call_id) 复用同一行
	dataResult(t, sess("append-message", map[string]any{
		"turn_id": "t1",
		"msg": map[string]any{
			"role": "tool", "tool_call_id": "c2", "tool_call_status": "failed",
			"content": `{"call":{"tool_call_id":"c2","name":"t_y"},"result":{"content":"boom","status":"failed"}}`,
		},
	}))
	rows := msgRowsOfTurn(t, "t1")
	if len(rows) != 1 {
		t.Fatalf("无 key 回填应复用同一行（恒 1 行）：%+v", rows)
	}
	if st := persist.Sval(rows[0]["tool_call_status"]); st != "failed" {
		t.Fatalf("状态=%q，want failed", st)
	}
}

// TestAppendMessageAssistantKeyedUpsert：assistant 分段落库（带 key）写同一行。
func TestAppendMessageAssistantKeyedUpsert(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	sess := func(action string, data map[string]any) map[string]any {
		return dataCall(t, bus, "data-session-"+action, map[string]any{"instance_id": "ins-test", "data": data})
	}
	dataResult(t, sess("ensure-session", map[string]any{"session_id": "s1"}))
	dataResult(t, sess("ensure-turn", map[string]any{"turn_id": "t1", "session_id": "s1"}))

	r1 := dataResult(t, sess("append-message", map[string]any{
		"turn_id": "t1",
		"msg":     map[string]any{"role": "assistant", "content": "第一段"},
	}))
	key := persist.Sval(r1["id"])
	if key == "" {
		t.Fatal("assistant 首段应回 id")
	}
	dataResult(t, sess("append-message", map[string]any{
		"turn_id": "t1", "key": key,
		"msg": map[string]any{"role": "assistant", "content": "第一段第二段"},
	}))

	var assistants []data.Record
	for _, r := range msgRowsOfTurn(t, "t1") {
		if persist.Sval(r["role"]) == "assistant" {
			assistants = append(assistants, r)
		}
	}
	if len(assistants) != 1 {
		t.Fatalf("assistant 行数=%d，want 1：%+v", len(assistants), assistants)
	}
	if c := persist.Sval(assistants[0]["content"]); c != "第一段第二段" {
		t.Fatalf("assistant 内容=%q，want 末次累积", c)
	}
}
