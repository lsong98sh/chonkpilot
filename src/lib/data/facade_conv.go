// 门面 DTO ↔ 内核契约类型的翻译（23 §7「门面 = 翻译层」；翻译在实现侧，门面不依赖内核）。
//
// 方向：`facade`（唯一定义）← `data`（内核/实现）。内核仍持有契约类型
// （ChatMsg/ToolCall/Snapshot，types.go），本文件只做**逐字段搬运 + 形状打平**
// （工具调用的 `function:{name,arguments}` 嵌套 → 门面的平铺领域字段）。
package data

import "github.com/chonkpilot/chonkpilot-data/facade"

// SnapshotToFacade 把内核快照翻成门面 DTO（sessionID = 快照归属会话，内核快照里不存该列）。
func SnapshotToFacade(sessionID string, s Snapshot) facade.Snapshot {
	out := facade.Snapshot{SessionID: sessionID, Turn: s.SnapshotTurn}
	for _, m := range s.History {
		out.Messages = append(out.Messages, MessageToFacade(m))
	}
	return out
}

// SnapshotFromFacade 把门面 DTO 翻回内核快照（SessionID 不落内核类型，由存储接口单独携带）。
func SnapshotFromFacade(f facade.Snapshot) Snapshot {
	out := Snapshot{SnapshotTurn: f.Turn}
	for _, m := range f.Messages {
		out.History = append(out.History, MessageFromFacade(m))
	}
	return out
}

// MessageToFacade 把内核消息翻成门面 DTO（工具调用嵌套 → 平铺）。
func MessageToFacade(m ChatMsg) facade.Message {
	out := facade.Message{
		Role:       m.Role,
		Kind:       m.Kind,
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		Reasoning:  m.Reasoning,
		Meta:       m.Meta,
	}
	for _, tc := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCallToFacade(tc))
	}
	return out
}

// MessageFromFacade 把门面 DTO 翻回内核消息（平铺 → 工具调用嵌套）。
func MessageFromFacade(m facade.Message) ChatMsg {
	out := ChatMsg{
		Role:       m.Role,
		Kind:       m.Kind,
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		Reasoning:  m.Reasoning,
		Meta:       m.Meta,
	}
	for _, tc := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCallFromFacade(tc))
	}
	return out
}

// ToolCallToFacade 把内核工具调用翻成门面 DTO（嵌套 function{name,arguments} → 平铺两格）。
func ToolCallToFacade(tc ToolCall) facade.ToolCall {
	return facade.ToolCall{ID: tc.ID, Type: tc.Type, Name: tc.Function.Name, Arguments: tc.Function.Arguments}
}

// ToolCallFromFacade 把门面 DTO 翻回内核工具调用（平铺 → 嵌套）。
func ToolCallFromFacade(tc facade.ToolCall) ToolCall {
	var k ToolCall
	k.ID, k.Type = tc.ID, tc.Type
	k.Function.Name, k.Function.Arguments = tc.Name, tc.Arguments
	return k
}
