// chat 宿主适配器（MOD-1 M5b-①）：LLMClient 契约验证。
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lsong98sh/chonkpilot-agent/chat"
	"github.com/lsong98sh/chonkpilot-agent/router"
)

// TestChatLLMClientStep：chat.LLMClient → router → StepEvent 流（文本 + Done）。
func TestChatLLMClientStep(t *testing.T) {
	rec := &llmRecorder{}
	llm := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	cl := newChatLLMClient(s, router.Spec{
		Name: "chat-shadow", Protocol: ProtocolOpenAI, BaseURL: llm.URL, DefaultModel: "m1",
	}, ChatOptions{Model: "m1"})

	ch, err := cl.Step(context.Background(), chat.StepRequest{
		InstanceID: "ins-test", SessionID: "s-chat", TurnID: "t-chat",
		Messages: []chat.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "hi"}},
	}, chat.StepOptions{})
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	var text string
	var done chat.StepEvent
	for ev := range ch {
		switch ev.Type {
		case chat.StepEventDelta:
			text += ev.Text
		case chat.StepEventDone:
			done = ev
		}
	}
	if text != "retry-ok" {
		t.Fatalf("文本增量=%q", text)
	}
	if done.Err != nil || done.Finish != "stop" {
		t.Fatalf("done=%+v", done)
	}
	if reqs := rec.requests(); len(reqs) != 1 || len(reqs[0]) != 2 || reqs[0][1].Content != "hi" {
		t.Fatalf("router 收到的消息错：%+v", reqs)
	}
}
