// D-30 白盒：内置兜底适配器（echo）——协议 / 能力声明 / 固定文案取文口径 / 取消感知 / 不因带工具降级失败。
package echo

import (
	"context"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// runStream 跑一次 Stream 并收集事件（Stream 返回后关闭通道）。
func runStream(t *testing.T, ctx context.Context, spec canon.Spec, req canon.Request) ([]canon.Event, error) {
	t.Helper()
	out := make(chan canon.Event, 8)
	done := make(chan error, 1)
	go func() {
		err := New().Stream(ctx, spec, req, out)
		close(out)
		done <- err
	}()
	var evs []canon.Event
	for ev := range out {
		evs = append(evs, ev)
	}
	return evs, <-done
}

// TestProtocolAndCaps：协议名 = echo；能力只声明流式（无工具 / 无思考 / 无图片 / 无 usage）。
func TestProtocolAndCaps(t *testing.T) {
	a := New()
	if a.Protocol() != canon.ProtocolEcho {
		t.Fatalf("Protocol=%q want %q", a.Protocol(), canon.ProtocolEcho)
	}
	if got := a.Caps(); got != (canon.Caps{Stream: true}) {
		t.Fatalf("Caps=%+v want 只声明流式", got)
	}
}

// TestStreamRepliesFixedText：以**固定文案**回复最后一条真实用户消息（排除 notify / continue /
// resume 系统注入消息）→ 单段 text_delta + done{stop}；Model 回落 spec.DefaultModel。
func TestStreamRepliesFixedText(t *testing.T) {
	msgs := []canon.Message{
		{Role: canon.RoleSystem, Content: []canon.Part{{Type: canon.PartText, Text: "系统提示"}}},
		{Role: canon.RoleUser, Kind: "text", Content: []canon.Part{{Type: canon.PartText, Text: "问题一"}}},
		{Role: canon.RoleAssistant, Content: []canon.Part{{Type: canon.PartText, Text: "回答一"}}},
		{Role: canon.RoleUser, Kind: "text", Content: []canon.Part{{Type: canon.PartText, Text: "问题二"}}},
		{Role: canon.RoleUser, Kind: "notify", Content: []canon.Part{{Type: canon.PartText, Text: "工具通知"}}},
		{Role: canon.RoleUser, Kind: "resume", Content: []canon.Part{{Type: canon.PartText, Text: "恢复续轮"}}},
	}
	spec := canon.Spec{Name: "echo", Protocol: canon.ProtocolEcho, DefaultModel: "echo"}
	evs, err := runStream(t, context.Background(), spec, canon.Request{Messages: msgs})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	const want = "收到问题二，目前无法回复，请设置LLM。"
	if len(evs) != 2 || evs[0].Type != canon.EvTextDelta || evs[0].Text != want {
		t.Fatalf("事件=%+v want [text_delta %q, done]", evs, want)
	}
	if evs[0].Model != "echo" || evs[1].Type != canon.EvDone || evs[1].FinishReason != "stop" || evs[1].Model != "echo" {
		t.Fatalf("事件=%+v want Model=echo / done{stop}", evs)
	}
}

// TestReplyTextFixedTemplate（**文案断言**，防再漂移）：固定文案逐字 = 收到<内容>，目前无法回复，请设置LLM。
// —— 用户输入只出现在"收到"与句末提示之间；空输入 → 空串（不发 text_delta）。
func TestReplyTextFixedTemplate(t *testing.T) {
	if got, want := replyPrefix+replySuffix, "收到，目前无法回复，请设置LLM。"; got != want {
		t.Fatalf("文案骨架=%q want %q", got, want)
	}
	for _, in := range []string{"你好", "multi\nline", "a，目前无法回复"} {
		got := replyText(in)
		if want := "收到" + in + "，目前无法回复，请设置LLM。"; got != want {
			t.Fatalf("replyText(%q)=%q want %q", in, got, want)
		}
		if !strings.HasPrefix(got, replyPrefix) || !strings.HasSuffix(got, replySuffix) {
			t.Fatalf("replyText(%q)=%q 未按固定文案包裹", in, got)
		}
	}
	if got := replyText(""); got != "" {
		t.Fatalf("空输入 replyText=%q want \"\"（不发 text_delta）", got)
	}
}

// TestStreamWithoutRealUserMessage：无真实用户消息 → 只投 done（不发 text_delta）。
func TestStreamWithoutRealUserMessage(t *testing.T) {
	spec := canon.Spec{Name: "echo", Protocol: canon.ProtocolEcho, DefaultModel: "echo"}
	msgs := []canon.Message{
		{Role: canon.RoleSystem, Content: []canon.Part{{Type: canon.PartText, Text: "系统提示"}}},
		{Role: canon.RoleUser, Kind: "continue", Content: []canon.Part{{Type: canon.PartText, Text: "自动续写"}}},
	}
	evs, err := runStream(t, context.Background(), spec, canon.Request{Messages: msgs})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(evs) != 1 || evs[0].Type != canon.EvDone {
		t.Fatalf("事件=%+v want 仅 done", evs)
	}
}

// TestStreamIgnoresImagesAndTools：兜底**不因**请求带图片 / 工具而失败（忽略）——图片块不参与取文，
// 工具项被忽略。
func TestStreamIgnoresImagesAndTools(t *testing.T) {
	req := canon.Request{
		Messages: []canon.Message{{Role: canon.RoleUser, Content: []canon.Part{
			{Type: canon.PartText, Text: "看图"},
			{Type: canon.PartImage, Image: &canon.ImagePart{MIME: "image/png", DataURL: "data:image/png;base64,AA"}},
		}}},
		Tools: []canon.ToolDef{{Name: "t"}},
	}
	spec := canon.Spec{Name: "echo", Protocol: canon.ProtocolEcho, DefaultModel: "echo"}
	evs, err := runStream(t, context.Background(), spec, req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	const want = "收到看图，目前无法回复，请设置LLM。"
	if len(evs) != 2 || evs[0].Type != canon.EvTextDelta || evs[0].Text != want || evs[1].Type != canon.EvDone {
		t.Fatalf("事件=%+v want [text_delta %q, done]（图片块不参与取文）", evs, want)
	}
}

// TestStreamModelOverride：`Options.Model` 非空 → 覆盖 spec.DefaultModel。
func TestStreamModelOverride(t *testing.T) {
	spec := canon.Spec{Name: "echo", Protocol: canon.ProtocolEcho, DefaultModel: "echo"}
	evs, err := runStream(t, context.Background(), spec, canon.Request{
		Messages: []canon.Message{{Role: canon.RoleUser, Content: []canon.Part{{Type: canon.PartText, Text: "q"}}}},
		Options:  canon.CallOptions{Model: "m-x"},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if evs[0].Model != "m-x" || evs[1].Model != "m-x" {
		t.Fatalf("Model 未被 Options 覆盖：%+v", evs)
	}
}

// TestStreamContextCanceled：消费方已取消 → 立即返回错误、不投事件（不阻塞）。
func TestStreamContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	spec := canon.Spec{Name: "echo", Protocol: canon.ProtocolEcho, DefaultModel: "echo"}
	out := make(chan canon.Event, 4)
	err := New().Stream(ctx, spec, canon.Request{
		Messages: []canon.Message{{Role: canon.RoleUser, Content: []canon.Part{{Type: canon.PartText, Text: "q"}}}},
	}, out)
	if err == nil {
		t.Fatal("已取消的 ctx 应返回错误")
	}
	if len(out) != 0 {
		t.Fatalf("取消后不应投事件，实得 %d", len(out))
	}
}

// TestTextOfSkipsEmptyBlocks：多文本块拼接、空块跳过。
func TestTextOfSkipsEmptyBlocks(t *testing.T) {
	got := textOf([]canon.Part{
		{Type: canon.PartText, Text: "甲"},
		{Type: canon.PartText, Text: ""},
		{Type: canon.PartImage, Image: &canon.ImagePart{DataURL: "data:image/png;base64,AA"}},
		{Type: canon.PartText, Text: "乙"},
	})
	if got != "甲乙" {
		t.Fatalf("textOf=%q want 甲乙", got)
	}
	if !strings.Contains(got, "甲") {
		t.Fatal("拼接丢失文本")
	}
}
