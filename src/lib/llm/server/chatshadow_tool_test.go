// chat 工具端口适配器（MOD-1 M5b-②）契约与工具环影子等价测试。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-chat"
	"github.com/chonkpilot/chonkpilot-router"
)

// TestChatToolCatalogTools：ToolDef → chat.ToolSpec（InputSchema 原文；Servers 恒空）。
func TestChatToolCatalogTools(t *testing.T) {
	cat := newChatToolCatalog(nil, func(instanceID string) []ToolDef {
		if instanceID != "ins-test" {
			t.Fatalf("instance 透传错：%q", instanceID)
		}
		return []ToolDef{
			{Name: "core_file_read", Description: "read", Parameters: map[string]any{"type": "object"}, Meta: map[string]any{"async": "never"}},
			{Name: "no_schema"},
		}
	})
	tools, err := cat.Tools(context.Background(), "ins-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "core_file_read" || tools[1].Name != "no_schema" {
		t.Fatalf("工具列表错：%+v", tools)
	}
	var schema map[string]any
	if err := json.Unmarshal(tools[0].InputSchema, &schema); err != nil || schema["type"] != "object" {
		t.Fatalf("InputSchema 错：%s %v", tools[0].InputSchema, err)
	}
	if tools[1].InputSchema != nil {
		t.Fatalf("无参数工具不应产出 schema：%s", tools[1].InputSchema)
	}
	if tools[0].Meta["async"] != "never" {
		t.Fatalf("_meta 未透传：%+v", tools[0].Meta)
	}
	if servers, err := cat.Servers(context.Background(), "ins-test"); err != nil || servers != nil {
		t.Fatalf("Servers 应恒空：%+v %v", servers, err)
	}
}

// TestChatToolInvokerDispatch：同步结果 / 策略拒绝 / 异步挂起 / ask 挂起 / 取消标记 / 错误。
func TestChatToolInvokerDispatch(t *testing.T) {
	s := newTestServer(t, httptest.NewServer(http.NotFoundHandler()))
	registerTestInstance(t, s)
	fakeGWCallCtxMu.Lock()
	fakeGWCallCtx = map[string][2]string{}
	fakeGWCallCtxMu.Unlock()

	inv := newChatToolInvoker(s, "ins-test", "s-tool", "t-tool", "/work", "/data")
	ctx := context.Background()

	// ① 同步：fake gateway 固定回 "file content"。
	out, err := inv.Dispatch(ctx, chat.ToolCall{ID: "tc-1", Name: "core_file_read", Arguments: `{"path":"a.txt"}`})
	if err != nil {
		t.Fatal(err)
	}
	if out.Pending || out.Result.CallID != "tc-1" || out.Result.Content != "file content" || out.Result.IsError {
		t.Fatalf("同步结果错：%+v", out)
	}

	// ② 策略拒绝：不发 gateway，失败文本回喂。
	deny := newChatToolInvoker(s, "ins-test", "s-tool", "t-tool", "/work", "/data")
	deny.policy = func(name string, _ map[string]any) string {
		if name == "blocked_tool" {
			return "错误: 工具不在白名单内"
		}
		return ""
	}
	out, err = deny.Dispatch(ctx, chat.ToolCall{ID: "tc-2", Name: "blocked_tool"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Result.IsError || out.Result.Content == "" || out.Pending {
		t.Fatalf("策略拒绝结果错：%+v", out)
	}

	// ③ 异步：pending + task_id 作为 op 句柄。
	out, err = inv.Dispatch(ctx, chat.ToolCall{ID: "tc-3", Name: "core_file_read", Arguments: `{"_async":true}`})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Pending || out.OpID != "tk-test" {
		t.Fatalf("异步挂起错：%+v", out)
	}

	// ④ ask：走宿主通道挂起，不下发 gateway。
	askInv := newChatToolInvoker(s, "ins-test", "s-tool", "t-tool", "/work", "/data")
	askInv.ask = func(call chat.ToolCall, _ map[string]any) string {
		if call.ID != "tc-4" {
			t.Fatalf("ask 收到的 call 错：%+v", call)
		}
		return "ask-1"
	}
	out, err = askInv.Dispatch(ctx, chat.ToolCall{ID: "tc-4", Name: "ask_user", Arguments: `{"question":"q"}`})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Pending || out.OpID != "ask-1" {
		t.Fatalf("ask 挂起错：%+v", out)
	}

	// ⑤ 取消标记：结果照常回喂 + _meta.status=cancelled。
	out, err = inv.Dispatch(ctx, chat.ToolCall{ID: "tc-5", Name: "core_file_read", Arguments: `{"_cancel":true}`})
	if err != nil {
		t.Fatal(err)
	}
	if out.Pending || out.Result.Meta["status"] != "cancelled" || out.Result.Content == "" {
		t.Fatalf("取消结果错：%+v", out)
	}

	// ⑥ 缺 instance：适配器把 gateway 错误编码为失败结果（不返回 error）。
	bad := newChatToolInvoker(s, "", "s-tool", "t-tool", "/work", "/data")
	out, err = bad.Dispatch(ctx, chat.ToolCall{ID: "tc-6", Name: "core_file_read"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Result.IsError || out.Result.Content == "" {
		t.Fatalf("缺 instance 应失败回喂：%+v", out)
	}
}

// TestChatToolInvokerCancelDetach：执行池控制面转发（空 instance 回落本轮实例）。
func TestChatToolInvokerCancelDetach(t *testing.T) {
	s := newTestServer(t, httptest.NewServer(http.NotFoundHandler()))
	registerTestInstance(t, s)
	resetExecControl()

	inv := newChatToolInvoker(s, "ins-test", "s-tool", "t-tool", "/work", "/data")
	if ok, err := inv.Cancel(context.Background(), "", "tk-1"); err != nil || !ok {
		t.Fatalf("Cancel: %v %v", ok, err)
	}
	if got := execCancelledRefs(); len(got) != 1 || got[0] != "tk-1" {
		t.Fatalf("取消未转发：%v", got)
	}
	if got := execCancelledInstances(); len(got) != 1 || got[0] != "ins-test" {
		t.Fatalf("取消 instance 回落错：%v", got)
	}
	// 测试用执行池的 DetachExec 固定回 "tk-bg"（见 testExecSink）。
	if id, err := inv.Detach(context.Background(), "other", "tk-2"); err != nil || id != "tk-bg" {
		t.Fatalf("Detach: %q %v", id, err)
	}
	if got := execDetachedInstances(); len(got) != 1 || got[0] != "other" {
		t.Fatalf("转后台 instance 错：%v", got)
	}
}

// toolLoopLLM 是本用例的确定性 LLM 桩：第 1 次请求回工具调用，第 2 次回终稿。
func toolLoopLLM(t *testing.T) *httptest.Server {
	t.Helper()
	var calls int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			args := jb(map[string]any{"path": "a.txt"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-read", "type": "function",
					"function": map[string]any{"name": "core_file_read", "arguments": string(args)},
				}}}, "tool_calls"),
			})
			return
		}
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "done"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
}

// TestChatToolLoopThroughAdapters：引擎经工具端口跑完整工具环；两遍影子轨迹逐项相等。
func TestChatToolLoopThroughAdapters(t *testing.T) {
	s := newTestServer(t, httptest.NewServer(http.NotFoundHandler()))
	registerTestInstance(t, s)

	st := newSessionStore(s.bus, "ins-test")
	if err := st.EnsureSession("s-tool"); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureTurn("t-tool", "s-tool"); err != nil {
		t.Fatal(err)
	}
	store := newChatTurnStore(s, "ins-test", "s-tool", "t-tool")
	sink := newChatEventSink(s, "ins-test", "s-tool", "t-tool", nil)

	run := func() (chat.Trace, chat.TurnResult) {
		llm := toolLoopLLM(t)
		t.Cleanup(llm.Close)
		shadow := chat.NewRecorder()
		eng := chat.New(chat.Deps{
			InstanceID: "ins-test", SessionID: "s-tool",
			Context: shadowContext{},
			Store:   &chat.ShadowTurnStore{Inner: store, Rec: shadow},
			Sink:    sink,
			LLM:     newChatLLMClient(s, router.Spec{Name: "chat-tool", Protocol: ProtocolOpenAI, BaseURL: llm.URL, DefaultModel: "m1"}, ChatOptions{Model: "m1"}),
			Catalog: newChatToolCatalog(s, func(string) []ToolDef {
				return []ToolDef{{Name: "core_file_read", Description: "read", Parameters: map[string]any{"type": "object"}}}
			}),
			Tools:  newChatToolInvoker(s, "ins-test", "s-tool", "t-tool", "/work", "/data"),
			Clock:  shadowClock{at: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)},
			Shadow: shadow,
			Config: chat.RuntimeConfig{MaxSteps: 4},
		})
		res, err := eng.RunTurn(context.Background(), "t-tool")
		if err != nil {
			t.Fatalf("RunTurn: %v", err)
		}
		return shadow.Trace(), res
	}

	wantTrace, want := run()
	gotTrace, _ := run()

	if want.FinalText != "done" || want.Finish != "stop" {
		t.Fatalf("终态错：%+v", want)
	}
	if len(want.ToolCalls) != 1 || want.ToolCalls[0].Name != "core_file_read" ||
		want.ToolCalls[0].Result.Content != "file content" || want.ToolCalls[0].Result.IsError {
		t.Fatalf("工具记录错：%+v", want.ToolCalls)
	}
	if err := chat.Verify(wantTrace, gotTrace); err != nil {
		t.Fatalf("工具环影子轨迹不等价：%v", err)
	}
	if len(wantTrace.Steps) != 2 {
		t.Fatalf("应有 2 次 step（工具前后各一）：%d", len(wantTrace.Steps))
	}
	// 落库：assistant（含 tool_calls）+ tool（结果）各一条。
	msgs := st.LoadMessages("t-tool")
	var sawAssistant, sawTool bool
	for _, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) == 1 {
			sawAssistant = true
		}
		if m.Role == "tool" && m.Content == "file content" && m.ToolCallID == "tc-read" {
			sawTool = true
		}
	}
	if !sawAssistant || !sawTool {
		t.Fatalf("落库缺工具环消息：%+v", msgs)
	}
}
