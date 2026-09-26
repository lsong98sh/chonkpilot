// LR-9 白盒：usage 归一 —— 三协议形态 + **部分字段缺失**（缺失即 0，见 `usage.go` 文件头口径）。
package adaptor

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// TestNormalizeUsage：字段映射 + total 缺失求和 + 缺失字段一律 0。
func TestNormalizeUsage(t *testing.T) {
	cases := []struct {
		name                      string
		prompt, completion, total int
		reasoning, cached         int
		want                      canon.Usage
	}{
		{
			name:   "OpenAI 兼容全量（含 details）",
			prompt: 10, completion: 5, total: 15, reasoning: 3, cached: 4,
			want: canon.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, ReasoningTokens: 3, CachedTokens: 4},
		},
		{
			name:   "OpenAI 缺 total → 分项求和",
			prompt: 10, completion: 5, total: 0, reasoning: 0, cached: 0,
			want: canon.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
		{
			name:   "Anthropic（不下发 total / reasoning / cache）",
			prompt: 11, completion: 7, total: 0, reasoning: 0, cached: 0,
			want: canon.Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18},
		},
		{
			name:   "Anthropic 带 cache_read（无 total）",
			prompt: 11, completion: 7, total: 0, reasoning: 0, cached: 4,
			want: canon.Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18, CachedTokens: 4},
		},
		{
			name:   "上游未提供任何字段 → 全 0（未知）",
			prompt: 0, completion: 0, total: 0, reasoning: 0, cached: 0,
			want: canon.Usage{},
		},
		{
			name:   "仅 total（分项缺失）→ 不做反向推导",
			prompt: 0, completion: 0, total: 9,
			want: canon.Usage{TotalTokens: 9},
		},
		{
			name:   "仅 cached（其余缺失）",
			cached: 6,
			want:   canon.Usage{CachedTokens: 6},
		},
	}
	for _, c := range cases {
		got := NormalizeUsage(c.prompt, c.completion, c.total, c.reasoning, c.cached)
		if got != c.want {
			t.Fatalf("%s: NormalizeUsage=(%+v) want %+v", c.name, got, c.want)
		}
	}
}
