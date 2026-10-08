// canonical（chonkpilot-router）↔ 快照（chonkpilot-data 的 ChatMsg / ToolCall）转换。
//
// 落位（2026-09-22 用户定案：**转换层归 llm，router 不访问 data**）：
//   - llm 从 data 读出 ChatMsg（拼接 / 组装）→ 经本文件转成 router canonical → 交给 router；
//   - router 只认自持的 canonical：canonical → 具体协议格式 → 发送 + 流式解析 → 回 router.Event
//     全在 router 内闭环，**router 零 Chonkpilot 依赖**（原 `chonkpilot-router/conv` 子 module 已删除）。
//   - 本文件**只做类型转换**，不承载任何协议 / 编排语义。
//
// 字段映射（与 data 侧 wire 白名单一致，逐字保持原 conv 口径）：
//   - 保留：Role / Kind / Content（文本）/ ToolCallID / ToolCalls / Reasoning；
//   - **Meta（`_meta`）不进入 canonical**：data 侧自述「不参与 LLM 请求（toWireMessages 显式白名单
//     字段）」→ 转换即丢弃（快照 / 落库仍由 llm 用 data 类型承载，不经 router）；
//   - 图片：canonical 的 ImagePart 由 llm 侧 llm_images.go 在**构造请求时**展开（非快照来源），
//     故 Message → ChatMsg 只取文本块（图片块不入快照）；
//   - ToolCall：canonical 无 `type` 字段 → 回写固定 "function"；canonical 的 Index 是流式期
//     序号（快照无该字段）→ 转快照丢弃，回读按位置重建。
package server

import (
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-router"
)

// toolCallTypeFunction 是快照 ToolCall.type 的固定取值（OpenAI 兼容形态，data 侧既有口径）。
const toolCallTypeFunction = "function"

// ToRouterMessage 转换单条快照消息：纯文本 → 单个 text 块（Content 为空 → 无内容块）。
func ToRouterMessage(m data.ChatMsg) router.Message {
	msg := router.Message{
		Role:       router.Role(m.Role),
		Kind:       m.Kind,
		ToolCallID: m.ToolCallID,
		ToolCalls:  ToRouterToolCalls(m.ToolCalls),
		Reasoning:  m.Reasoning,
	}
	if m.Content != "" {
		msg.Content = []router.Part{{Type: router.PartText, Text: m.Content}}
	}
	return msg
}

// FromRouterMessage 把 canonical 消息转回快照消息（文本块拼接；Meta 恒为空，见文件头）。
func FromRouterMessage(m router.Message) data.ChatMsg {
	return data.ChatMsg{
		Role:       string(m.Role),
		Kind:       m.Kind,
		Content:    textFromParts(m.Content),
		ToolCallID: m.ToolCallID,
		ToolCalls:  FromRouterToolCalls(m.ToolCalls),
		Reasoning:  m.Reasoning,
	}
}

// ToRouterToolCalls 把快照工具调用转换为 canonical（Index = 位置序）。
func ToRouterToolCalls(calls []data.ToolCall) []router.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]router.ToolCall, 0, len(calls))
	for i, c := range calls {
		out = append(out, router.ToolCall{
			ID:        c.ID,
			Name:      c.Function.Name,
			Arguments: c.Function.Arguments,
			Index:     i,
		})
	}
	return out
}

// FromRouterToolCalls 把 canonical 工具调用转回快照形态（type = "function"）。
func FromRouterToolCalls(calls []router.ToolCall) []data.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]data.ToolCall, 0, len(calls))
	for _, c := range calls {
		var tc data.ToolCall
		tc.Type = toolCallTypeFunction
		tc.ID = c.ID
		tc.Function.Name = c.Name
		tc.Function.Arguments = c.Arguments
		out = append(out, tc)
	}
	return out
}

// textFromParts 拼接文本块（canonical → 快照；图片块不入快照，见文件头）。
func textFromParts(parts []router.Part) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 && parts[0].Type == router.PartText {
		return parts[0].Text
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == router.PartText {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}
