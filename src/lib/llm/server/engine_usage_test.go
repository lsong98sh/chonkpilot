package server

import (
	"strings"
	"testing"

	"github.com/lsong98sh/chonkpilot-agent/chat"
)

// TestEstimateStepInputTokens：本地估算口径 = 字符数 / 2（与组装侧 `session.EstimateTokens` 同源），
// 覆盖 messages 正文、tool_call 名/参数、工具 schema（名/描述/参数 JSON）。
func TestEstimateStepInputTokens(t *testing.T) {
	req := chat.StepRequest{Messages: []chat.Message{
		{Role: "user", Content: strings.Repeat("a", 100)}, // 50
	}}
	if got := estimateStepInputTokens(req); got != 50 {
		t.Fatalf("纯正文估算 = %d，want 50", got)
	}

	req.Messages = append(req.Messages, chat.Message{
		Role:      "assistant",
		ToolCalls: []chat.ToolCall{{Name: "abcd", Arguments: "abcdefgh"}}, // 2 + 4
	})
	if got := estimateStepInputTokens(req); got != 56 {
		t.Fatalf("正文 + tool_call 估算 = %d，want 56", got)
	}

	req.Tools = []chat.ToolSpec{{
		Name:        "tool",                      // 2
		Description: "abcd",                      // 2
		InputSchema: []byte(`{"type":"object"}`), // 17 runes → 8
	}}
	if got := estimateStepInputTokens(req); got != 68 {
		t.Fatalf("含工具 schema 估算 = %d，want 68", got)
	}
}

// TestUsageEventSendPhase：send 阶段只带估算与上限，不带真实 usage 字段。
func TestUsageEventSendPhase(t *testing.T) {
	maxCtx, maxOut := 128000, 4096
	tc := &turnCtx{
		req:                StartReq{InstanceID: "inst", Session: "sess", Turn: "turn", Parents: []string{"parent"}},
		llmName:            "prov",
		llmModel:           "model-a",
		llmMaxContextToken: &maxCtx,
		llmMaxOutputToken:  &maxOut,
	}
	ev := tc.usageEvent("send", 3, 1234, nil)

	if ev["phase"] != "send" || ev["step"] != int64(3) || ev["est_prompt_tokens"] != 1234 {
		t.Fatalf("send 基础字段不符: %+v", ev)
	}
	if ev["llm"] != "prov" || ev["model"] != "model-a" {
		t.Fatalf("provider/model 不符: %+v", ev)
	}
	if ev["max_context_token"] != 128000 || ev["max_output_token"] != 4096 {
		t.Fatalf("max token 不符: %+v", ev)
	}
	if _, ok := ev["prompt_tokens"]; ok {
		t.Fatalf("send 阶段不应带真实 usage: %+v", ev)
	}
	if ev["ts"] == "" || ev["ts"] == nil {
		t.Fatalf("缺少 ts: %+v", ev)
	}
}

// TestUsageEventDonePhase：done 阶段带真实 usage；上游 model 回显优先于本轮请求 model。
func TestUsageEventDonePhase(t *testing.T) {
	tc := &turnCtx{
		req:      StartReq{InstanceID: "inst", Session: "sess", Turn: "turn"},
		llmName:  "prov",
		llmModel: "request-model",
	}
	ev := tc.usageEvent("done", 1, 900, &UsageReport{
		PromptTokens: 1000, CompletionTokens: 30, TotalTokens: 1030,
		ReasoningTokens: 10, CachedTokens: 800, Model: "upstream-model",
	})

	if ev["phase"] != "done" || ev["prompt_tokens"] != 1000 || ev["cached_tokens"] != 800 {
		t.Fatalf("done usage 字段不符: %+v", ev)
	}
	if ev["model"] != "upstream-model" {
		t.Fatalf("model 应以上游回显为准: %+v", ev)
	}
	if _, ok := ev["max_context_token"]; ok {
		t.Fatalf("未配置上下文窗口时不应写该键: %+v", ev)
	}
}
