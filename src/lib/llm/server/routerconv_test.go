// 白盒：canonical（router.Message / router.ToolCall）↔ 快照（data.ChatMsg / data.ToolCall）
// 双向往返等价（迁自原 `chonkpilot-router/conv` 的 LR-1 白盒，落位见 routerconv.go 文件头）。
//
// 覆盖：Role / Kind / Content（文本）/ ToolCallID / Reasoning / 多枚 tool_calls；
// 以及两条**设计口径**（见 routerconv.go 文件头）：① Meta 不进入 canonical（不下发 LLM）；
// ② 多模态图片块只在 canonical 侧（快照为纯文本）。
package server

import (
	"reflect"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-router"
)

// snapToolCall 构造快照工具调用（type 固定 function，与 llm 侧落库口径一致）。
func snapToolCall(id, name, args string) data.ToolCall {
	var c data.ToolCall
	c.Type = "function"
	c.ID = id
	c.Function.Name = name
	c.Function.Arguments = args
	return c
}

// toRouterMessages 快照消息序列 → canonical（顺序保持；测试用，逐条 ToRouterMessage）。
// 生产侧无批转换入口——组装/请求构造都逐条经 ToRouterMessage。
func toRouterMessages(msgs []data.ChatMsg) []router.Message {
	out := make([]router.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, ToRouterMessage(m))
	}
	return out
}

// TestRouterConvChatMsgRoundTripEquivalence：快照 → canonical → 快照，逐字段等价。
func TestRouterConvChatMsgRoundTripEquivalence(t *testing.T) {
	msgs := []data.ChatMsg{
		{Role: "system", Content: "sys"},
		{Role: "user", Kind: "text", Content: "问"},
		{Role: "assistant", Content: "答", Reasoning: "思考链",
			ToolCalls: []data.ToolCall{
				snapToolCall("tc-1", "core_file_read", `{"path":"a.txt"}`),
				snapToolCall("tc-2", "core_file_write", `{"path":"b.txt","text":"x"}`),
			}},
		{Role: "tool", ToolCallID: "tc-1", Content: "结果"},
		{Role: "user", Kind: "notify", Content: "[工具通知] 完成"},
		{Role: "assistant", Content: ""}, // 空内容（如仅工具调用后落库）→ 无内容块
	}
	canon := toRouterMessages(msgs)
	if len(canon) != len(msgs) {
		t.Fatalf("转换条数=%d want %d", len(canon), len(msgs))
	}
	// canonical 侧形状抽查：文本 → 单 text 块；reasoning / tool_calls / tool_call_id 落位。
	if p := canon[1].Content; len(p) != 1 || p[0].Type != router.PartText || p[0].Text != "问" {
		t.Fatalf("user 内容块=%+v want 单 text 块", p)
	}
	if canon[2].Reasoning != "思考链" || len(canon[2].ToolCalls) != 2 {
		t.Fatalf("assistant canonical=%+v want reasoning + 2 tool_calls", canon[2])
	}
	if canon[2].ToolCalls[1].Name != "core_file_write" || canon[2].ToolCalls[1].Index != 1 || canon[2].ToolCalls[1].Arguments != `{"path":"b.txt","text":"x"}` {
		t.Fatalf("tool_calls[1]=%+v want {core_file_write, Index 1}", canon[2].ToolCalls[1])
	}
	if canon[3].ToolCallID != "tc-1" {
		t.Fatalf("tool 消息 ToolCallID=%q want tc-1", canon[3].ToolCallID)
	}
	if canon[5].Content != nil {
		t.Fatalf("空 Content → %+v want 无内容块", canon[5].Content)
	}

	// 回写：与输入逐字段等价。
	back := make([]data.ChatMsg, 0, len(canon))
	for _, m := range canon {
		back = append(back, FromRouterMessage(m))
	}
	if !reflect.DeepEqual(back, msgs) {
		t.Fatalf("往返不一致：\n got=%+v\nwant=%+v", back, msgs)
	}
}

// TestRouterConvMessageRoundTripStable：canonical → 快照 → canonical 稳定（幂等）。
func TestRouterConvMessageRoundTripStable(t *testing.T) {
	in := []router.Message{
		{Role: router.RoleUser, Kind: "notify", Content: []router.Part{{Type: router.PartText, Text: "n"}}},
		{Role: router.RoleAssistant, Reasoning: "r", ToolCalls: []router.ToolCall{
			{ID: "tc-1", Name: "f", Arguments: `{"a":1}`, Index: 0},
			{ID: "tc-2", Name: "g", Arguments: `{"b":2}`, Index: 1},
		}},
		{Role: router.RoleTool, ToolCallID: "tc-1", Content: []router.Part{{Type: router.PartText, Text: "done"}}},
	}
	snap := make([]data.ChatMsg, 0, len(in))
	for _, m := range in {
		snap = append(snap, FromRouterMessage(m))
	}
	got := toRouterMessages(snap)
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("canonical 往返不稳定：\n got=%+v\nwant=%+v", got, in)
	}
}

// TestRouterConvMessageDropsMeta：Meta（`_meta`）不进入 canonical（不下发 LLM）→ 回写后为空。
func TestRouterConvMessageDropsMeta(t *testing.T) {
	in := data.ChatMsg{Role: "tool", ToolCallID: "tc-1", Content: "结果", Meta: map[string]any{"async": true, "category": "core"}}
	canon := ToRouterMessage(in)
	if canon.Role != router.RoleTool || canon.ToolCallID != "tc-1" || len(canon.Content) != 1 {
		t.Fatalf("canonical=%+v want 保留 Role/ToolCallID/文本", canon)
	}
	if back := FromRouterMessage(canon); back.Meta != nil {
		t.Fatalf("Meta=%v want nil（canonical 无 Meta 承载位，不下发 LLM）", back.Meta)
	}
}

// TestRouterConvMessageImagePartsNotInSnapshot：图片块属 canonical 侧（请求期展开）→ 快照只存文本。
func TestRouterConvMessageImagePartsNotInSnapshot(t *testing.T) {
	img := &router.ImagePart{MIME: "image/png", DataURL: "data:image/png;base64,AAAA"}
	mixed := router.Message{Role: router.RoleUser, Content: []router.Part{
		{Type: router.PartText, Text: "看图 "},
		{Type: router.PartImage, Image: img},
		{Type: router.PartText, Text: "并说明"},
	}}
	if got := FromRouterMessage(mixed).Content; got != "看图 并说明" {
		t.Fatalf("Content=%q want 文本块拼接（图片块不入快照）", got)
	}
	onlyImage := router.Message{Role: router.RoleUser, Content: []router.Part{{Type: router.PartImage, Image: img}}}
	if got := FromRouterMessage(onlyImage).Content; got != "" {
		t.Fatalf("仅图片 → Content=%q want 空串", got)
	}
}

// TestRouterConvToolCallsEmptyToNil：空工具调用 → nil（快照侧 omitempty 口径，避免写出 `[]`）；
// 回写 type 恒为 function。
func TestRouterConvToolCallsEmptyToNil(t *testing.T) {
	if got := ToRouterToolCalls(nil); got != nil {
		t.Fatalf("ToRouterToolCalls(nil)=%+v want nil", got)
	}
	if got := FromRouterToolCalls(nil); got != nil {
		t.Fatalf("FromRouterToolCalls(nil)=%+v want nil", got)
	}
	back := FromRouterToolCalls([]router.ToolCall{{ID: "tc-1", Name: "f", Arguments: "{}"}})
	if len(back) != 1 || back[0].Type != "function" {
		t.Fatalf("回写 ToolCall=%+v want type=function", back)
	}
}
