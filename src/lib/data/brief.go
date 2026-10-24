// 【简化态原文】构造（口径 X ②，2026-09-25）。
//
// 2026-10-24（Persist Provider）：内核版 `BriefMessages` 的唯一实现已内化到
// `chonkpilot-session`（组件与数据面同源），本文件转发内核版 + 保留门面 DTO 版。
package data

import (
	"strings"

	"github.com/chonkpilot/chonkpilot-session"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// BriefMessages 构造一段消息的【简化态原文】（内核 `ChatMsg`；组装侧用）。
// 只返回新切片（不修改入参元素），保证历史 / 快照不被污染。
func BriefMessages(msgs []ChatMsg) []ChatMsg { return session.BriefMessages(msgs) }

// BriefFacadeMessages 同 BriefMessages（门面 DTO；压缩侧用）——**同一构造规则**。
// 只返回新切片（不修改入参元素）。
func BriefFacadeMessages(msgs []facade.Message) []facade.Message {
	out := make([]facade.Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "tool":
			continue
		case "assistant":
			if strings.TrimSpace(m.Content) == "" {
				continue
			}
			m.Reasoning = ""
			m.ToolCalls = nil
			out = append(out, m)
		default:
			out = append(out, m)
		}
	}
	return out
}
