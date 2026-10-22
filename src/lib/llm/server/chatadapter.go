// chat 组件（chonkpilot-chat）与宿主类型的 adapter（MOD-1 M1b）。
//
// 边界：chat 内核只认自有类型（`chat.Message` / `chat.ToolCall` / `chat.CallContext` …），
// 本文件是**唯一**做类型互转的地方——data / facade / gateway / router 类型只在这里出现。
// M1b 只落地「转换层 + Clock 端口实现」；TurnStore / EventSink / ToolInvoker / ToolCatalog
// 的落地依赖数据面与 gateway 方法面，分别归 M2 / M3（无行为变更）。
package server

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lsong98sh/chonkpilot-chat"
	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	mcpgateway "github.com/lsong98sh/chonkpilot-mcp-gateway/gateway"
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

// ─── INBOX（DB 通道：inbox_items 行）────────────────────────────────

// ChatInboxFromFacade 把 inbox 行转成 chat 类型（Think/Effort 保持原始 JSON）。
func ChatInboxFromFacade(it facade.InboxItem) chat.InboxItem {
	return chat.InboxItem{
		ID:             it.ID,
		SessionID:      it.SessionID,
		TurnID:         it.TurnID,
		Type:           it.Type,
		LLM:            it.LLM,
		Content:        it.Content,
		Think:          rawJSON(it.Think),
		Effort:         rawJSON(it.Effort),
		ScenarioID:     it.ScenarioID,
		Status:         it.Status,
		IdempotencyKey: it.IdempotencyKey,
		CreatedAt:      it.CreatedAt,
	}
}

// ChatInboxToFacade 把 chat inbox 项还原为数据面行。
func ChatInboxToFacade(it chat.InboxItem) facade.InboxItem {
	return facade.InboxItem{
		ID:             it.ID,
		SessionID:      it.SessionID,
		TurnID:         it.TurnID,
		Type:           it.Type,
		LLM:            it.LLM,
		Content:        it.Content,
		Think:          anyJSON(it.Think),
		Effort:         anyJSON(it.Effort),
		ScenarioID:     it.ScenarioID,
		Status:         it.Status,
		IdempotencyKey: it.IdempotencyKey,
		CreatedAt:      it.CreatedAt,
	}
}

// rawJSON 把数据面的 any 形态（string / map / 已编码 JSON）转成原始 JSON。
func rawJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// anyJSON 把原始 JSON 还原为数据面 any 形态（空 → nil）。
func anyJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

// ─── 调用上下文（JSON 通道：tools/call 的 context / _meta["chonkpilot"]）───

// ChatCallContextFromGateway 把 gateway 调用上下文转成 chat 类型（session/turn 键名对齐）。
func ChatCallContextFromGateway(c mcpgateway.Context) chat.CallContext {
	return chat.CallContext{
		InstanceID: c.InstanceID,
		SessionID:  c.Session,
		TurnID:     c.Turn,
		ToolCallID: c.ToolCallID,
		Purpose:    c.Purpose,
		WorkDir:    c.WorkDir,
		DataDir:    c.DataDir,
		TopSession: c.TopSession,
		Parent:     c.Parent,
	}
}

// ChatCallContextToGateway 把 chat 调用上下文还原为 gateway 形态。
func ChatCallContextToGateway(c chat.CallContext) mcpgateway.Context {
	return mcpgateway.Context{
		InstanceID: c.InstanceID,
		Session:    c.SessionID,
		Turn:       c.TurnID,
		ToolCallID: c.ToolCallID,
		Purpose:    c.Purpose,
		WorkDir:    c.WorkDir,
		DataDir:    c.DataDir,
		TopSession: c.TopSession,
		Parent:     c.Parent,
	}
}

// ─── 目录（gateway tools/list · servers/list 视图 → chat 自有类型）────────

// ChatToolSpecFromView 把 gateway `tools/list` 单项（JSON map）转成 chat.ToolSpec。
func ChatToolSpecFromView(v map[string]any) chat.ToolSpec {
	spec := chat.ToolSpec{}
	spec.Name, _ = v["name"].(string)
	spec.Description, _ = v["description"].(string)
	if schema, ok := v["inputSchema"]; ok {
		if b, err := json.Marshal(schema); err == nil {
			spec.InputSchema = b
		}
	}
	if meta, ok := v["_meta"].(map[string]any); ok {
		spec.Meta = meta
		if srv, ok := meta["server"].(map[string]any); ok {
			spec.Server, _ = srv["alias"].(string)
			if spec.Server == "" {
				spec.Server, _ = srv["node"].(string)
			}
		}
	}
	if spec.Server == "" {
		spec.Server, _ = v["node"].(string)
	}
	return spec
}

// ChatServerInfoFromView 把 gateway `servers/list` 单项（JSON map）转成 chat.ServerInfo。
func ChatServerInfoFromView(v map[string]any) chat.ServerInfo {
	info := chat.ServerInfo{}
	info.ID, _ = v["name"].(string)
	info.Name = info.ID
	info.Transport, _ = v["type"].(string) // dir / spawned / proxied
	info.Status, _ = v["status"].(string)
	info.Category, _ = v["category"].(string)
	if tools, ok := v["tools"].([]any); ok {
		info.ToolCount = len(tools)
	}
	return info
}

// ─── 工具结果（tools/call 结果 → chat.ToolResult）───────────────────

// ChatToolResultFromMCP 把下游 CallToolResult 归一为 chat.ToolResult：
// 文本块按换行拼接；无文本时用 structuredContent 的 JSON；isError 原样保留。
func ChatToolResultFromMCP(callID string, res *mcp.CallToolResult) chat.ToolResult {
	out := chat.ToolResult{CallID: callID}
	if res == nil {
		return out
	}
	out.IsError = res.IsError
	var texts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && tc.Text != "" {
			texts = append(texts, tc.Text)
		}
	}
	out.Content = strings.Join(texts, "\n")
	if out.Content == "" && res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			out.Content = string(b)
		}
	}
	return out
}

// ─── Clock 端口（系统时钟；测试可注入假时钟）────────────────────────

// chatSystemClock 是 chat.Clock 的系统实现。
type chatSystemClock struct{}

func (chatSystemClock) Now() time.Time                         { return time.Now() }
func (chatSystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

var _ chat.Clock = chatSystemClock{}
