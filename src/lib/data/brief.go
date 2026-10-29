// 【简化态原文】构造（口径 X ②，2026-09-25）。
//
// 2026-10-24（Persist Provider）：内核 `ChatMsg` 版的唯一实现已内化到
// `chonkpilot-session`（组件侧直接调用），本文件只保留门面 DTO 版。
package data

import (
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// BriefFacadeMessages 构造门面 DTO 消息的【简化态原文】（压缩侧用）；与内核版同一构造规则。
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
