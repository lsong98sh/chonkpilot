// chat 上下文端口适配器（MOD-1 M6d）契约测试。
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chonkpilot/chonkpilot-chat"
)

// TestChatContextProviderBuild（A4-P2 修正后）：历史 = 已压缩轮次（当前轮被 exclude_turn 排除），
// 当前轮**已落库的用户输入**应包含；当前轮的 assistant / tool 不应包含（本轮由引擎重新生成）；
// 系统提示词与生产 resolveTurnScenario 同源。
func TestChatContextProviderBuild(t *testing.T) {
	llm := httptest.NewServer(http.NotFoundHandler())
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	st := newSessionStore(s.bus, "ins-test")
	if err := st.EnsureSession("s-ctx"); err != nil {
		t.Fatal(err)
	}
	for _, turn := range []string{"t-old", "t-now"} {
		if err := st.EnsureTurn(turn, "s-ctx"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.AppendFullKeyed("t-old", ChatMsg{Role: "user", Content: "old"}, ""); err != nil {
		t.Fatal(err)
	}
	// 当前轮：用户输入 + 本轮已产生的 assistant / tool（后者不得进上下文）。
	if _, err := st.AppendFullKeyed("t-now", ChatMsg{Role: "user", Kind: "text", Content: "now"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendFullKeyed("t-now", ChatMsg{Role: "assistant", Content: "now-reply"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendFullKeyed("t-now", ChatMsg{Role: "tool", ToolCallID: "tc-now", Content: "now-tool"}, ""); err != nil {
		t.Fatal(err)
	}

	ctx, err := newChatContextProvider(s, "", "").Build(context.Background(), chat.ContextRequest{
		InstanceID: "ins-test", SessionID: "s-ctx", TurnID: "t-now",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var hasOld, hasNow, hasReply, hasTool bool
	for _, m := range ctx.Messages {
		switch m.Content {
		case "old":
			hasOld = true
		case "now":
			hasNow = true
		case "now-reply":
			hasReply = true
		case "now-tool":
			hasTool = true
		}
	}
	if !hasOld {
		t.Fatalf("历史应包含上一轮消息 old：%+v", ctx.Messages)
	}
	if !hasNow {
		t.Fatalf("当前轮已落库用户输入应包含在 Messages：%+v", ctx.Messages)
	}
	if hasReply {
		t.Fatalf("当前轮 assistant 不应包含（本轮由引擎重新生成）：%+v", ctx.Messages)
	}
	if hasTool {
		t.Fatalf("当前轮 tool 不应包含（本轮由引擎重新生成）：%+v", ctx.Messages)
	}
	want := s.resolveTurnScenario("ins-test", "s-ctx", "", "").sysPrompt
	if ctx.SystemPrompt == "" || ctx.SystemPrompt != want {
		t.Fatalf("系统提示词应与 resolveTurnScenario 同源：%q != %q", ctx.SystemPrompt, want)
	}
}

// TestChatContextProviderRequiresIDs：缺 instance / session 必须明确报错（不静默返回空上下文）。
func TestChatContextProviderRequiresIDs(t *testing.T) {
	llm := httptest.NewServer(http.NotFoundHandler())
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	p := newChatContextProvider(s, "", "")
	if _, err := p.Build(context.Background(), chat.ContextRequest{SessionID: "s-ctx"}); err == nil {
		t.Fatal("缺 instance_id 应返回 error")
	}
	if _, err := p.Build(context.Background(), chat.ContextRequest{InstanceID: "ins-test"}); err == nil {
		t.Fatal("缺 session_id 应返回 error")
	}
}
