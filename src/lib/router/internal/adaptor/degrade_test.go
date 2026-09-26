// LR-7 白盒：声明式降级（degrade.go）—— 硬不支持明确报错；软不支持就地剔除（策略见 degrade.go 表）。
package adaptor

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// fullCaps 是全能力矩阵（什么都不降级）。
func fullCaps() canon.Caps {
	return canon.Caps{
		Stream: true, Tools: true, ToolChoice: true, ParallelToolCalls: true,
		Reasoning: true, ReasoningEffort: true, Images: true, TopP: true,
		MaxTokensRequired: false, UsageInStream: true, SystemRole: true,
	}
}

// TestDegradeHardUnsupportedTools：caps.Tools=false 却带 tools → 明确报错（invalid，不静默丢工具）。
func TestDegradeHardUnsupportedTools(t *testing.T) {
	caps := fullCaps()
	caps.Tools = false
	req := canon.Request{Tools: []canon.ToolDef{{Name: "core_file_read"}}}
	if err := Degrade(caps, &req); err == nil || err.Kind != canon.ErrorInvalid || err.Retryable {
		t.Fatalf("err=%+v want invalid/不可重试", err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("硬不支持时不应就地剔除（请求原样保留，便于调用方判断）：%+v", req.Tools)
	}
}

// TestDegradeHardUnsupportedImages：caps.Images=false 却带图片块 → 明确报错；文本消息不受影响。
func TestDegradeHardUnsupportedImages(t *testing.T) {
	caps := fullCaps()
	caps.Images = false
	req := canon.Request{Messages: []canon.Message{{
		Role:    canon.RoleUser,
		Content: []canon.Part{{Type: canon.PartText, Text: "看图"}, {Type: canon.PartImage, Image: &canon.ImagePart{DataURL: "data:image/png;base64,AA"}}},
	}}}
	if err := Degrade(caps, &req); err == nil || err.Kind != canon.ErrorInvalid {
		t.Fatalf("err=%+v want invalid", err)
	}
	// 仅 Type=PartImage（Image 指针为空）同样视为图片块。
	req2 := canon.Request{Messages: []canon.Message{{Role: canon.RoleUser, Content: []canon.Part{{Type: canon.PartImage}}}}}
	if err := Degrade(caps, &req2); err == nil {
		t.Fatal("Type=image 的块应判为图片 → 报错")
	}
	textOnly := canon.Request{Messages: []canon.Message{{Role: canon.RoleUser, Content: []canon.Part{{Type: canon.PartText, Text: "纯文本"}}}}}
	if err := Degrade(caps, &textOnly); err != nil {
		t.Fatalf("纯文本请求不应报错：%v", err)
	}
}

// TestDegradeSoftDrops：caps.Reasoning / ReasoningEffort / TopP 不支持 → **就地剔除**（不报错、
// 不静默保留），并把剔除后的请求交给编码器。
func TestDegradeSoftDrops(t *testing.T) {
	topP := 0.5
	cases := []struct {
		name       string
		mutate     func(*canon.Caps)
		wantReason bool // 剔除后是否仍保留 Reasoning
		wantTopP   bool
	}{
		{"Reasoning 不支持", func(c *canon.Caps) { c.Reasoning = false }, false, true},
		{"ReasoningEffort 不支持", func(c *canon.Caps) { c.ReasoningEffort = false }, false, true},
		{"TopP 不支持", func(c *canon.Caps) { c.TopP = false }, true, false},
	}
	for _, c := range cases {
		caps := fullCaps()
		c.mutate(&caps)
		req := canon.Request{Options: canon.CallOptions{Reasoning: &canon.Reasoning{Effort: "high"}, TopP: &topP}}
		if err := Degrade(caps, &req); err != nil {
			t.Fatalf("%s: 软降级不应报错，实得 %v", c.name, err)
		}
		if (req.Options.Reasoning != nil) != c.wantReason {
			t.Fatalf("%s: Reasoning 保留=%v want %v", c.name, req.Options.Reasoning != nil, c.wantReason)
		}
		if (req.Options.TopP != nil) != c.wantTopP {
			t.Fatalf("%s: TopP 保留=%v want %v", c.name, req.Options.TopP != nil, c.wantTopP)
		}
	}
}

// TestDegradeFullCapsUntouched：全能力 → 请求逐字段不变（不误删）。
func TestDegradeFullCapsUntouched(t *testing.T) {
	topP, temp := 0.7, 0.2
	req := canon.Request{
		Messages: []canon.Message{{Role: canon.RoleUser, Content: []canon.Part{{Type: canon.PartText, Text: "hi"}}}},
		Tools:    []canon.ToolDef{{Name: "t"}},
		Options:  canon.CallOptions{Temperature: &temp, TopP: &topP, Reasoning: &canon.Reasoning{Effort: "low"}},
	}
	before := req
	if err := Degrade(fullCaps(), &req); err != nil {
		t.Fatalf("全能力不应报错：%v", err)
	}
	if len(req.Tools) != len(before.Tools) || req.Options.TopP == nil || req.Options.Temperature == nil || req.Options.Reasoning == nil {
		t.Fatalf("全能力下请求被改动：%+v want %+v", req, before)
	}
}
