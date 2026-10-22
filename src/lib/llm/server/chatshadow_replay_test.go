// chat 离线重放影子（MOD-1 A4-P2）测试：
//   - 脚本化 LLM / 工具结果映射的纯单测；
//   - mq 驱动跑真实 turn → 直接调 shadowReplayTurn 断言等价；
//   - env 未设时零调用（默认关闭）。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lsong98sh/chonkpilot-chat"
	"github.com/chonkpilot/chonkpilot-data/persist"
)

// TestReplayLLMScript：由 2 条 assistant 消息（1 条带 tool_calls）生成脚本 → 两次 Step 的
// Done 文本 / 工具调用 / Finish 正确；超出脚本 → 显式错误。
func TestReplayLLMScript(t *testing.T) {
	msgs := []ChatMsg{
		{Role: "user", Kind: "text", Content: "hi"},
		{Role: "assistant", Reasoning: "想一想", ToolCalls: []chat.ToolCall{
			{ID: "c1", Name: "core_file_read", Arguments: `{"path":"a.txt"}`},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "file content"},
		{Role: "assistant", Content: "final"},
	}
	l := newReplayLLM(msgs)
	if n := l.stepCount(); n != 2 {
		t.Fatalf("脚本条数=%d want 2", n)
	}
	step := func() []chat.StepEvent {
		ch, err := l.Step(context.Background(), chat.StepRequest{}, chat.StepOptions{})
		if err != nil {
			t.Fatalf("Step: %v", err)
		}
		var out []chat.StepEvent
		for ev := range ch {
			out = append(out, ev)
		}
		return out
	}

	first := step()
	var done chat.StepEvent
	hasReasoning := false
	for _, ev := range first {
		switch ev.Type {
		case chat.StepEventReasoning:
			if ev.Text == "想一想" {
				hasReasoning = true
			}
		case chat.StepEventDone:
			done = ev
		}
	}
	if !hasReasoning {
		t.Fatalf("第一步应先发 Reasoning：%+v", first)
	}
	if done.Err != nil || done.Finish != "tool_calls" || len(done.ToolCalls) != 1 ||
		done.ToolCalls[0].ID != "c1" || done.ToolCalls[0].Name != "core_file_read" {
		t.Fatalf("第一步 Done 错：%+v", done)
	}

	second := step()
	var done2 chat.StepEvent
	var text2 string
	for _, ev := range second {
		switch ev.Type {
		case chat.StepEventDelta:
			text2 += ev.Text
		case chat.StepEventDone:
			done2 = ev
		}
	}
	if text2 != "final" || done2.Text != "final" {
		t.Fatalf("第二步正文错：delta=%q done=%q", text2, done2.Text)
	}
	if done2.Err != nil || done2.Finish != "stop" || len(done2.ToolCalls) != 0 {
		t.Fatalf("第二步 Done 错：%+v", done2)
	}

	third := step()
	var done3 chat.StepEvent
	for _, ev := range third {
		if ev.Type == chat.StepEventDone {
			done3 = ev
		}
	}
	if done3.Err == nil {
		t.Fatal("超出脚本应返回显式错误（不得静默返回空回复）")
	}
}

// TestReplayToolInvoker：tool 行（content = ToolContent JSON）→ Dispatch 返回正确文本 / IsError；
// 缺失 call → IsError 的明确失败结果。
func TestReplayToolInvoker(t *testing.T) {
	raw := func(callID, text, status string) string {
		b, _ := json.Marshal(persist.ToolContent{
			Call:   &persist.ToolCallContent{ToolCallID: callID, Name: "core_file_read"},
			Result: &persist.ToolResultContent{Content: text, Status: status},
		})
		return string(b)
	}
	inv := newReplayToolInvoker([]ChatMsg{
		{Role: "tool", ToolCallID: "c1", Content: raw("c1", "ok", "completed")},
		{Role: "tool", ToolCallID: "c2", Content: raw("c2", "boom", "failed")},
		{Role: "tool", ToolCallID: "c3", Content: "plain text"},
	})

	cases := []struct {
		callID  string
		content string
		isError bool
	}{
		{"c1", "ok", false},
		{"c2", "boom", true},
		{"c3", "plain text", false}, // 读侧已归一为裸结果文本 → 回退原始 content
	}
	for _, c := range cases {
		out, err := inv.Dispatch(context.Background(), chat.ToolCall{ID: c.callID, Name: "core_file_read"})
		if err != nil {
			t.Fatalf("Dispatch(%s): %v", c.callID, err)
		}
		if out.Result.Content != c.content || out.Result.IsError != c.isError {
			t.Fatalf("Dispatch(%s)=%+v want content=%q isError=%v", c.callID, out.Result, c.content, c.isError)
		}
	}

	missing, err := inv.Dispatch(context.Background(), chat.ToolCall{ID: "c9"})
	if err != nil {
		t.Fatalf("Dispatch(c9): %v", err)
	}
	if !missing.Result.IsError || !strings.Contains(missing.Result.Content, "c9") {
		t.Fatalf("缺失工具结果应显式失败：%+v", missing.Result)
	}
	if ok, err := inv.Cancel(context.Background(), "", "c1"); err != nil || ok {
		t.Fatalf("Cancel 应返回 (false, nil)：%v %v", ok, err)
	}
	if id, err := inv.Detach(context.Background(), "", "c1"); err != nil || id != "" {
		t.Fatalf("Detach 应返回 (\"\", nil)：%q %v", id, err)
	}
}

// TestShadowReplayDiff：比对口径确实敏感（等价 = ""；内容 / 条数 / 工具调用不同 → 有摘要）。
func TestShadowReplayDiff(t *testing.T) {
	prod := []chat.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "", ToolCalls: []chat.ToolCall{{ID: "c1", Name: "echo", Arguments: `{"x":1}`}}},
		{Role: "tool", ToolCallID: "c1", Content: "ok"},
		{Role: "assistant", Content: "done"},
	}
	if rep := shadowReplayDiff(prod, append([]chat.Message(nil), prod...)); rep != "" {
		t.Fatalf("同序列应等价，got:\n%s", rep)
	}

	bad := append([]chat.Message(nil), prod...)
	bad[3].Content = "other"
	if rep := shadowReplayDiff(prod, bad); !strings.Contains(rep, "第 4 条不一致") {
		t.Fatalf("内容不同应报第 4 条：%s", rep)
	}
	if rep := shadowReplayDiff(prod, prod[:3]); !strings.Contains(rep, "重放缺少第 4 条") {
		t.Fatalf("少一条应报缺失：%s", rep)
	}
	if rep := shadowReplayDiff(prod, append(append([]chat.Message(nil), prod...), chat.Message{Role: "assistant", Content: "extra"})); !strings.Contains(rep, "重放多出第 5 条") {
		t.Fatalf("多一条应报多出：%s", rep)
	}
	// 忽略 id / kind / created_at 等非协议字段：role/content/tool_call_id/tool_calls 相同即等价。
	meta := append([]chat.Message(nil), prod...)
	meta[0].ID, meta[0].Kind, meta[1].SessionID = "m-1", "text", "s-x"
	if rep := shadowReplayDiff(prod, meta); rep != "" {
		t.Fatalf("忽略字段不应产生差异：\n%s", rep)
	}
}

// TestShadowReplayTextTurn（e2e，mq 驱动）：跑一轮纯文本 turn（session-start + session-send，
// 等 session-complete）→ 直接调 shadowReplayTurn → report 为空、无 error。
func TestShadowReplayTextTurn(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Setenv(chatShadowEnv, "0") // 生产触发点关闭，避免与本用例的直接调用重复

	startTurn(t, s, "s-shadow-text", "t-shadow-text")
	sendAndWait(t, s, "s-shadow-text", "t-shadow-text", "hello replay")

	prod := newSessionStore(s.bus, "ins-test").LoadMessages("t-shadow-text")
	if len(prod) != 2 || prod[0].Role != "user" || prod[1].Role != "assistant" {
		t.Fatalf("文本轮消息形状应为 user/assistant：%+v", prod)
	}
	report, err := s.shadowReplayTurn("ins-test", "s-shadow-text", "t-shadow-text")
	if err != nil {
		t.Fatalf("shadowReplayTurn: %v", err)
	}
	if report != "" {
		t.Fatalf("文本轮重放与生产不等价：\n%s", report)
	}
}

// TestShadowReplayToolTurn（e2e，mq 驱动）：跑一轮「tool call → 结果 → 文本」，再重放 → 等价。
func TestShadowReplayToolTurn(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Setenv(chatShadowEnv, "0")

	startTurn(t, s, "s-shadow-tool", "t-shadow-tool")
	sendAndWait(t, s, "s-shadow-tool", "t-shadow-tool", "sync tool please")

	msgs := newSessionStore(s.bus, "ins-test").LoadMessages("t-shadow-tool")
	if len(msgs) < 4 {
		t.Fatalf("工具轮应有 4 条消息（user/assistant/tool/assistant）：%+v", msgs)
	}
	report, err := s.shadowReplayTurn("ins-test", "s-shadow-tool", "t-shadow-tool")
	if err != nil {
		t.Fatalf("shadowReplayTurn: %v", err)
	}
	if report != "" {
		t.Fatalf("工具轮重放与生产不等价：\n%s", report)
	}
}

// TestShadowReplayEnvOff：env 未设（"0"）时正常跑一轮 turn，生产触发点不得调用 shadowReplayTurn。
func TestShadowReplayEnvOff(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Setenv(chatShadowEnv, "0")
	shadowReplayCalls.Store(0)

	if chatShadowEnabled() {
		t.Fatal("env != 1 时 chatShadowEnabled 应为 false")
	}

	startTurn(t, s, "s-shadow-off", "t-shadow-off")
	sendAndWait(t, s, "s-shadow-off", "t-shadow-off", "env off please")

	// 触发点在后台 goroutine 时才可能自增；给足窗口确保不是"还没来得及调用"。
	time.Sleep(300 * time.Millisecond)
	if n := shadowReplayCalls.Load(); n != 0 {
		t.Fatalf("env 未开启时不得调用 shadowReplayTurn（调用次数=%d）", n)
	}
}
