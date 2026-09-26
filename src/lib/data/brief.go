// 【简化态原文】构造（口径 X ②，2026-09-25）——**压缩侧与组装侧共用的唯一实现**。
//
// 三段上下文的中间层：完整区保留完整原文；简化区**仅保留 text**（去掉 reasoning /
// tool_call / tool_result）；再更早为摘要区。本文件的规则即"仅保留 text"的逐字落地，
// 消费方按自身消息类型薄封装（内核 `ChatMsg` / 门面 `facade.Message`），规则同源不分叉。
//
// 口径一致性（P4）：压缩侧/组装侧判定简化区用的 `brief_tokens` 与本构造同源
// （`EstimateTokensOfMessages(BriefMessages(turn))` ↔ `EstimateTokensOfFacadeMessages(BriefFacadeMessages(turn))`）。
package data

import (
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// briefMessagesOf 是【简化态原文】构造的唯一实现（泛型内核；两侧薄封装共用）：
//
//   - role=tool（工具结果）→ **整条丢弃**（去掉 tool_result）；
//   - role=assistant → **仅保留正文**（去掉 reasoning 与 tool_calls）；正文为空白
//     （纯工具调用轮，无 text）→ 整条丢弃；
//   - 其余（user / system）→ 原样保留（本就只有 text）。
//
// textOnly 负责产出"剥掉 reasoning / tool_calls 的副本"（不修改入参元素）。
func briefMessagesOf[T any](msgs []T, role func(T) string, content func(T) string, textOnly func(T) T) []T {
	out := make([]T, 0, len(msgs))
	for _, m := range msgs {
		switch role(m) {
		case "tool":
			continue
		case "assistant":
			if strings.TrimSpace(content(m)) == "" {
				continue
			}
			out = append(out, textOnly(m))
		default:
			out = append(out, m)
		}
	}
	return out
}

// BriefMessages 构造一段消息的【简化态原文】（内核 `ChatMsg`；组装侧用）。
// 只返回新切片（不修改入参元素），保证 tc.hist / 快照不被污染。
func BriefMessages(msgs []ChatMsg) []ChatMsg {
	return briefMessagesOf(msgs,
		func(m ChatMsg) string { return m.Role },
		func(m ChatMsg) string { return m.Content },
		func(m ChatMsg) ChatMsg { m.Reasoning = ""; m.ToolCalls = nil; return m },
	)
}

// BriefFacadeMessages 同 BriefMessages（门面 DTO；压缩侧用）——**同一构造规则的薄封装**。
// 只返回新切片（不修改入参元素）。
func BriefFacadeMessages(msgs []facade.Message) []facade.Message {
	return briefMessagesOf(msgs,
		func(m facade.Message) string { return m.Role },
		func(m facade.Message) string { return m.Content },
		func(m facade.Message) facade.Message { m.Reasoning = ""; m.ToolCalls = nil; return m },
	)
}
