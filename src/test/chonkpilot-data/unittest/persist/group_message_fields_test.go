// group_message_fields_test.go — 群聊发言标识（sender_agent / mentions）的落库与消息面贯通
// （SUBCHAT-3 阶段 E；群聊方案 §10 C2 · C6）。
//
// 断言（**mq 驱动 + mq 监听**，50-测试体系 §1）：
//   - `data-session-append-message` 带 `sender_agent` / `mentions` → 落 messages 行；
//   - `data-session-history` 的消息视图**带回**这两列（群聊窗口据此显示"谁说的" + 调度器据此定向叫号）；
//   - **不带**两者 → 视图**不出现**这两键（普通会话/单聊消息逐字节兼容）。
package persist_test

import (
	"testing"
)

// historyMessages 取 `data-session-history` 应答里的消息视图数组。
func historyMessages(t *testing.T, res map[string]any) []any {
	t.Helper()
	msgs, _ := res["messages"].(map[string]any)
	if msgs == nil {
		t.Fatalf("history 应答缺 messages 段：%+v", res)
	}
	list, _ := msgs["messages"].([]any)
	return list
}

// TestGroupMessageSenderAgentAndMentions 群聊发言标识落库 + 视图回带 + 缺省不回归。
func TestGroupMessageSenderAgentAndMentions(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	const sess, turn = "smoke-grp-chat", "smoke-grp-t1"
	pubFire(bus, "data-session-ensure-session", jb(map[string]any{
		"req_id": "g1", "instance_id": "ins-test", "session_id": sess, "parent_session_id": "smoke-top",
	}))
	pubFire(bus, "data-session-ensure-turn", jb(map[string]any{
		"req_id": "g2", "instance_id": "ins-test", "turn_id": turn, "session_id": sess,
	}))

	// ① 群聊发言：带发言人与 @ 名单。
	dataCall(t, bus, "data-session-append-message", map[string]any{
		"req_id": "g3", "instance_id": "ins-test", "turn_id": turn,
		"msg": map[string]any{
			"role": "assistant", "content": "审完，两处问题。@程序员, @审查员",
			"sender_agent": "审查员", "mentions": []any{"程序员", "审查员"},
		},
	})
	// ② 普通消息（不带群聊标识）：视图不应出现这两键。
	dataCall(t, bus, "data-session-append-message", map[string]any{
		"req_id": "g4", "instance_id": "ins-test", "turn_id": turn,
		"msg": map[string]any{"role": "user", "kind": "text", "content": "hi"},
	})

	res := dataResult(t, dataCall(t, bus, "data-session-history", map[string]any{
		"req_id": "g5", "instance_id": "ins-test", "session_id": sess,
	}))
	msgs := historyMessages(t, res)
	if len(msgs) != 2 {
		t.Fatalf("history 应有 2 条消息：%+v", msgs)
	}

	var got map[string]any
	for _, m := range msgs {
		row, _ := m.(map[string]any)
		if row["content"] == "审完，两处问题。@程序员, @审查员" {
			got = row
		}
	}
	if got == nil {
		t.Fatalf("未在 history 中找到群聊发言：%+v", msgs)
	}
	if got["sender_agent"] != "审查员" {
		t.Fatalf("消息视图应带 sender_agent：%+v", got)
	}
	mentions, _ := got["mentions"].([]any)
	if len(mentions) != 2 || mentions[0] != "程序员" || mentions[1] != "审查员" {
		t.Fatalf("消息视图应带 mentions=[程序员 审查员]：%+v", got["mentions"])
	}

	// 缺省不回归：普通消息视图无 sender_agent / mentions 两键。
	for _, m := range msgs {
		row, _ := m.(map[string]any)
		if row["content"] != "hi" {
			continue
		}
		if _, has := row["sender_agent"]; has {
			t.Fatalf("普通消息不得带 sender_agent（缺省不写键）：%+v", row)
		}
		if _, has := row["mentions"]; has {
			t.Fatalf("普通消息不得带 mentions（缺省不写键）：%+v", row)
		}
	}
}
