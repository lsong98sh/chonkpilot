// chat 组件（chonkpilot-chat）与宿主类型的 adapter（MOD-1 M1b）。
//
// 边界：chat 内核只认自有类型（`chat.Message` / `chat.ToolCall` …），本文件是**唯一**做
// 消息类型互转的地方——data 快照类型只在这里出现。
package server

import (
	"github.com/chonkpilot/chonkpilot-data"
	"github.com/lsong98sh/chonkpilot-agent/chat"
)

// toolCallTypeFunction 是快照 ToolCall.type 的固定取值（OpenAI 兼容形态，data 侧既有口径）。
const toolCallTypeFunction = "function"

// ─── 消息（DB 通道：messages 行 / 快照）───────────────────────────────

// ChatMessageFromData 把快照消息转成 chat 自有类型（Meta / ToolCalls 原样保留）。
func ChatMessageFromData(m data.ChatMsg) chat.Message {
	return chat.Message{
		Role:       m.Role,
		Kind:       m.Kind,
		Content:    m.Content,
		Reasoning:  m.Reasoning,
		ToolCallID: m.ToolCallID,
		ToolCalls:  ChatToolCallsFromData(m.ToolCalls),
		Meta:       m.Meta,
	}
}

// ChatMessageToData 把 chat 消息转回快照形态（ToolCall.type 固定 function）。
func ChatMessageToData(m chat.Message) data.ChatMsg {
	return data.ChatMsg{
		Role:       m.Role,
		Kind:       m.Kind,
		Content:    m.Content,
		Reasoning:  m.Reasoning,
		ToolCallID: m.ToolCallID,
		ToolCalls:  ChatToolCallsToData(m.ToolCalls),
		Meta:       m.Meta,
	}
}

// chatMessagesFromData 批量转换快照消息（nil 安全）。
func chatMessagesFromData(msgs []data.ChatMsg) []ChatMsg {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]ChatMsg, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, ChatMessageFromData(m))
	}
	return out
}

// dataMessagesFromChat 批量还原快照消息（落库 / 快照 wire 形态）。
func dataMessagesFromChat(msgs []ChatMsg) []data.ChatMsg {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]data.ChatMsg, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, ChatMessageToData(m))
	}
	return out
}

// ChatToolCallsFromData 把快照工具调用摊平为 chat 形态（Name/Arguments 取自 function）。
func ChatToolCallsFromData(calls []data.ToolCall) []chat.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]chat.ToolCall, 0, len(calls))
	for _, c := range calls {
		out = append(out, chat.ToolCall{
			ID:        c.ID,
			Name:      c.Function.Name,
			Arguments: c.Function.Arguments,
		})
	}
	return out
}

// ChatToolCallsToData 把 chat 工具调用还原为快照形态（type = function）。
func ChatToolCallsToData(calls []chat.ToolCall) []data.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]data.ToolCall, 0, len(calls))
	for _, c := range calls {
		var tc data.ToolCall
		tc.ID = c.ID
		tc.Type = toolCallTypeFunction
		tc.Function.Name = c.Name
		tc.Function.Arguments = c.Arguments
		out = append(out, tc)
	}
	return out
}
