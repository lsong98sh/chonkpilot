// MOD-1 M1b 契约测试：三条跨进程通道（JSON / env / DB）的**字段名**一致性。
//
// 硬判据：任一侧改字段名（gateway.Context JSON tag、mcp-server 的 CHONKPILOT_* 注入、
// data.ChatMsg / facade.InboxItem 的行字段）→ 本文件失败。
// 说明：env 通道的注入点位于 `mcp-server`（跨 module，且常量在 mcp-tools internal 包内
// 不可 import）→ 这里对注入源码做字面断言，等价于「改名即失败」。
package server

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-chat"
	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
)

// jsonKeys 返回结构体按 JSON tag 展开后的键集合（排序）。
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func assertKeys(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s 字段名不一致：\n got  = %v\n want = %v", name, got, want)
	}
}

// TestContractJSONContextKeys：JSON 通道（tools/call.context 与 _meta["chonkpilot"]）字段名。
func TestContractJSONContextKeys(t *testing.T) {
	ctx := mcpgateway.Context{
		WorkDir: "w", DataDir: "d", Session: "s", Turn: "t", InstanceID: "i",
		ToolCallID: "c", TopSession: "ts", Parent: "p", Purpose: "pu",
	}
	assertKeys(t, "gateway.Context", jsonKeys(t, ctx), []string{
		"data_dir", "instance_id", "parent", "purpose", "session",
		"tool_call_id", "top_session", "turn", "work_dir",
	})
}

// chatEnvContract 是 JSON 键 → 进程环境变量（进程外 executor 通道）的契约表。
// 其余 JSON 键（session / turn / tool_call_id / top_session / parent / purpose）只走 JSON / 进程内 ctx。
// 2026-10-21（GATEWAY-2/L3）：meta/domain 已进程内化，purpose 不再经环境变量传递。
var chatEnvContract = map[string]string{
	"instance_id": "CHONKPILOT_INSTANCE",
	"work_dir":    "CHONKPILOT_WORKDIR",
	"data_dir":    "CHONKPILOT_DATADIR",
}

// TestContractEnvNames：env 通道 = mcp-server 注入点的字面断言（改名即失败）。
func TestContractEnvNames(t *testing.T) {
	raw, err := os.ReadFile("../../mcp-server/server/config.go")
	if err != nil {
		t.Fatalf("读 mcp-server config.go: %v", err)
	}
	src := string(raw)
	pairs := []string{
		`"CHONKPILOT_INSTANCE", cx.InstanceID`,
		`"CHONKPILOT_WORKDIR", cx.WorkDir`,
		`"CHONKPILOT_DATADIR", cx.DataDir`,
	}
	for _, want := range pairs {
		if !strings.Contains(src, want) {
			t.Fatalf("env 注入点缺失：%s（JSON 键 → 环境变量契约被破坏）", want)
		}
	}
	// 反向守卫：JSON 键集合 = env 键 ∪ 仅 JSON 键。
	jsonOnly := []string{"session", "turn", "tool_call_id", "top_session", "parent", "purpose"}
	for _, k := range jsonOnly {
		if _, ok := chatEnvContract[k]; ok {
			t.Fatalf("%s 不应有 env 通道（仅 JSON）", k)
		}
	}
}

// TestContractChatContextRoundTrip：gateway.Context ↔ chat.CallContext 全字段互转。
func TestContractChatContextRoundTrip(t *testing.T) {
	want := mcpgateway.Context{
		WorkDir: "w", DataDir: "d", Session: "s", Turn: "t", InstanceID: "i",
		ToolCallID: "c", TopSession: "ts", Parent: "p", Purpose: "pu",
	}
	got := ChatCallContextToGateway(ChatCallContextFromGateway(want))
	if got != want {
		t.Fatalf("上下文往返不一致：\n got  = %+v\n want = %+v", got, want)
	}
}

// TestContractDBMessageKeys：DB 通道（messages 行 / 快照）字段名 + chat 往返。
func TestContractDBMessageKeys(t *testing.T) {
	msg := data.ChatMsg{
		Role: "assistant", Kind: "text", Content: "hi", ToolCallID: "c",
		Reasoning: "r", Meta: map[string]any{"async": "always"},
		ToolCalls: []data.ToolCall{{ID: "c1", Type: "function"}},
	}
	msg.ToolCalls[0].Function.Name = "f"
	msg.ToolCalls[0].Function.Arguments = `{}`

	want := []string{"_meta", "content", "kind", "reasoning", "role", "tool_call_id", "tool_calls"}
	assertKeys(t, "data.ChatMsg", jsonKeys(t, msg), want)
	assertKeys(t, "chat.Message", jsonKeys(t, ChatMessageFromData(msg)), want)

	back := ChatMessageToData(ChatMessageFromData(msg))
	if !reflect.DeepEqual(back, msg) {
		t.Fatalf("Message 往返不一致：\n got  = %+v\n want = %+v", back, msg)
	}
}

// TestContractDBInboxKeys：DB 通道（inbox_items 行）字段名 + chat 往返。
func TestContractDBInboxKeys(t *testing.T) {
	item := facade.InboxItem{
		ID: "i", SessionID: "s", TurnID: "t", Type: "insert", LLM: "l",
		Content: "c", Think: map[string]any{"k": "v"}, Effort: "high",
		ScenarioID: "sc", Status: "pending", IdempotencyKey: "ik", CreatedAt: "2026-10-20",
	}
	want := []string{
		"content", "created_at", "effort", "id", "idempotency_key", "llm",
		"scenario_id", "session_id", "status", "think", "turn_id", "type",
	}
	assertKeys(t, "facade.InboxItem", jsonKeys(t, item), want)
	assertKeys(t, "chat.InboxItem", jsonKeys(t, ChatInboxFromFacade(item)), want)

	back := ChatInboxToFacade(ChatInboxFromFacade(item))
	if !reflect.DeepEqual(back, item) {
		t.Fatalf("Inbox 往返不一致：\n got  = %+v\n want = %+v", back, item)
	}
}

// TestContractToolCallsRoundTrip：ToolCall 快照形态（type=function）往返。
func TestContractToolCallsRoundTrip(t *testing.T) {
	calls := []data.ToolCall{}
	c := data.ToolCall{ID: "c1", Type: "function"}
	c.Function.Name = "read_file"
	c.Function.Arguments = `{"path":"a"}`
	calls = append(calls, c)
	back := ChatToolCallsToData(ChatToolCallsFromData(calls))
	if !reflect.DeepEqual(back, calls) {
		t.Fatalf("ToolCall 往返不一致：\n got  = %+v\n want = %+v", back, calls)
	}
}

// TestContractRouterMessageRoundTrip：chat.Message → data → router canonical → data，
// 仅按既有口径丢弃 `_meta`（不参与 LLM 请求）。
func TestContractRouterMessageRoundTrip(t *testing.T) {
	msg := chat.Message{
		Role: "assistant", Content: "hi", Reasoning: "r", ToolCallID: "c0",
		ToolCalls: []chat.ToolCall{{ID: "c1", Name: "f", Arguments: `{}`}},
	}
	dataMsg := ChatMessageToData(msg)
	back := FromRouterMessage(ToRouterMessage(ChatMessageFromData(dataMsg)))
	if back.Role != msg.Role || back.Content != msg.Content || back.Reasoning != msg.Reasoning ||
		back.ToolCallID != msg.ToolCallID {
		t.Fatalf("router 往返字段丢失：got %+v want %+v", back, msg)
	}
	if len(back.ToolCalls) != 1 || back.ToolCalls[0].Name != "f" || back.ToolCalls[0].Arguments != `{}` {
		t.Fatalf("router 往返 ToolCalls 不一致：%+v", back.ToolCalls)
	}
	if len(back.Meta) != 0 {
		t.Fatalf("Meta 不应进入 router 往返：%+v", back.Meta)
	}
}

// TestContractChatMessagePersistWireEquivalence：M2 验收——chat 自持消息落库前还原的快照 wire
// 与 `data.ChatMsg` 逐字段一致（tool_calls 的 type=function / function 嵌套形态不变）。
func TestContractChatMessagePersistWireEquivalence(t *testing.T) {
	chatMsg := chat.Message{
		Role: "assistant", Kind: "text", Content: "答", Reasoning: "r",
		ToolCalls: []chat.ToolCall{{ID: "c1", Name: "f", Arguments: `{}`}},
		Meta:      map[string]any{"async": "always"},
	}
	var snap data.ToolCall
	snap.ID = "c1"
	snap.Type = "function"
	snap.Function.Name = "f"
	snap.Function.Arguments = `{}`
	want := data.ChatMsg{
		Role: "assistant", Kind: "text", Content: "答", Reasoning: "r",
		ToolCalls: []data.ToolCall{snap},
		Meta:      map[string]any{"async": "always"},
	}

	gotRaw, _ := json.Marshal(ChatMessageToData(chatMsg))
	wantRaw, _ := json.Marshal(want)
	var gotMap, wantMap map[string]any
	_ = json.Unmarshal(gotRaw, &gotMap)
	_ = json.Unmarshal(wantRaw, &wantMap)
	if !reflect.DeepEqual(gotMap, wantMap) {
		t.Fatalf("落库 wire 不一致：\n got  = %v\n want = %v", gotMap, wantMap)
	}

	// 读回：快照 → chat 自持类型，逐字段等价（Meta 保留）。
	back := ChatMessageFromData(want)
	if !reflect.DeepEqual(back, chatMsg) {
		t.Fatalf("读回不一致：\n got  = %+v\n want = %+v", back, chatMsg)
	}
}

// TestContractCatalogViews：gateway tools/list · servers/list 视图 → chat 自有类型。
func TestContractCatalogViews(t *testing.T) {
	spec := ChatToolSpecFromView(map[string]any{
		"name": "read_file", "description": "读取", "node": "self",
		"inputSchema": map[string]any{"type": "object"},
		"_meta":       map[string]any{"server": map[string]any{"alias": "core"}},
	})
	if spec.Name != "read_file" || spec.Server != "core" || len(spec.InputSchema) == 0 || spec.Meta == nil {
		t.Fatalf("ToolSpec 转换异常：%+v", spec)
	}

	info := ChatServerInfoFromView(map[string]any{
		"name": "core", "type": "proxied", "status": "connected", "category": "core",
		"tools": []any{map[string]any{"name": "a"}, map[string]any{"name": "b"}},
	})
	if info.ID != "core" || info.Transport != "proxied" || info.Status != "connected" ||
		info.Category != "core" || info.ToolCount != 2 {
		t.Fatalf("ServerInfo 转换异常：%+v", info)
	}
}
