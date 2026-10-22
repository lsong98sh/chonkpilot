package chat

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// fakeHost 是端口契约的最小实现（M1a 编译期 + 行为哨兵；L2′ 完整测试宿主见 M8 chatest）。
type fakeHost struct {
	emitted []Event
}

func (f *fakeHost) Build(context.Context, ContextRequest) (Context, error) {
	return Context{SystemPrompt: "sys", Messages: []Message{{Role: "user", Content: "hi"}}}, nil
}

func (f *fakeHost) SaveMessage(context.Context, Message) error { return nil }
func (f *fakeHost) CommitInbox(context.Context, string, Message) error {
	return nil
}
func (f *fakeHost) AppendEvent(context.Context, TurnEvent) error { return nil }
func (f *fakeHost) Now() time.Time                               { return time.Unix(0, 0) }
func (f *fakeHost) After(d time.Duration) <-chan time.Time       { return time.After(d) }
func (f *fakeHost) Emit(_ context.Context, ev Event)             { f.emitted = append(f.emitted, ev) }
func (f *fakeHost) Dispatch(_ context.Context, call ToolCall) (ToolOutcome, error) {
	return ToolOutcome{Result: ToolResult{CallID: call.ID, Content: "ok"}}, nil
}
func (f *fakeHost) Cancel(context.Context, string, string) (bool, error)   { return true, nil }
func (f *fakeHost) Detach(context.Context, string, string) (string, error) { return "op-1", nil }
func (f *fakeHost) Servers(context.Context, string) ([]ServerInfo, error)  { return nil, nil }
func (f *fakeHost) Tools(context.Context, string) ([]ToolSpec, error)      { return nil, nil }

var (
	_ ContextProvider = (*fakeHost)(nil)
	_ TurnStore       = (*fakeHost)(nil)
	_ Clock           = (*fakeHost)(nil)
	_ EventSink       = (*fakeHost)(nil)
	_ ToolInvoker     = (*fakeHost)(nil)
	_ ToolCatalog     = (*fakeHost)(nil)
)

// TestPortsWired：端口可被同一宿主实现，且 Dispatch/EventSink 的自有类型可往返。
func TestPortsWired(t *testing.T) {
	ctx := context.Background()
	h := &fakeHost{}

	c, err := h.Build(ctx, ContextRequest{InstanceID: "ins", SessionID: "s", TurnID: "t"})
	if err != nil || c.SystemPrompt != "sys" || len(c.Messages) != 1 {
		t.Fatalf("Build = %+v err=%v", c, err)
	}

	out, err := h.Dispatch(ctx, ToolCall{ID: "call-1", Name: "x", Arguments: `{}`})
	if err != nil || out.Pending || out.Result.CallID != "call-1" {
		t.Fatalf("Dispatch = %+v err=%v", out, err)
	}

	h.Emit(ctx, Event{Type: EventToolResult, ToolResult: &out.Result})
	if len(h.emitted) != 1 || h.emitted[0].Type != EventToolResult {
		t.Fatalf("Emit 未记录：%+v", h.emitted)
	}

	// 类型 JSON 形状（契约测试基线；字段名与 spec 一致）
	raw, err := json.Marshal(Message{Role: "tool", ToolCallID: "call-1", Content: "ok"})
	if err != nil || string(raw) != `{"role":"tool","content":"ok","tool_call_id":"call-1"}` {
		t.Fatalf("Message JSON = %s err=%v", raw, err)
	}
}
