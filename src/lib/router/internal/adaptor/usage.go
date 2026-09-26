// usage 归一共用件（LR-9）—— 三协议 usage 载荷 → canonical `Usage{Prompt,Completion,Total,
// Reasoning,Cached}` 的**唯一**归一入口，纯标准库实现。
//
// 统一映射表（各家字段 → canonical 字段）：
//
//	canonical            OpenAI 兼容（chat）                Anthropic（/v1/messages）        OpenAI Responses
//	PromptTokens         usage.prompt_tokens                message_start.usage.input_tokens  usage.input_tokens
//	CompletionTokens     usage.completion_tokens            message_delta.usage.output_tokens usage.output_tokens
//	TotalTokens          usage.total_tokens（缺失 → 求和）   **不下发** → 由前两项求和         usage.total_tokens
//	ReasoningTokens      …completion_tokens_details.        **不下发**（thinking 计入 output） …output_tokens_details.
//	                     reasoning_tokens                                                    reasoning_tokens
//	CachedTokens         …prompt_tokens_details.cached_tokens cache_read_input_tokens         …input_tokens_details.
//	                                                                                         cached_tokens
//
// **缺失字段的语义（本件定稿，写入注释即口径）**：canonical `Usage` 的每个字段 **`0` = 上游未提供
// （未知）**，不区分「上游明确给了 0」与「上游没给」（归一口径：**缺失即 0**，不做 `*int` 零值区分
// —— 对消费方（计费展示 / 日志）而言「0 或未知」都不参与判断，且避免指针化污染 canonical）。
// 唯一例外是 `TotalTokens`：上游未提供（0）而分项非零时**由分项求和补齐**（确定性推导，不引入不确定值）。
package adaptor

import "github.com/chonkpilot/chonkpilot-router/internal/canon"

// NormalizeUsage 把各协议 usage 的分项归一到 canonical `Usage`。
//
//   - total 为 0（上游未提供）而任一分项非零 → `total = prompt + completion`（Anthropic 恒走此路）；
//   - 三个分项全 0 且 total 为 0 → 原样 0（未知，见文件头）；
//   - reasoning / cached 缺失即 0（见文件头「缺失字段的语义」）。
func NormalizeUsage(prompt, completion, total, reasoning, cached int) canon.Usage {
	if total == 0 && (prompt > 0 || completion > 0) {
		total = prompt + completion
	}
	return canon.Usage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		ReasoningTokens:  reasoning,
		CachedTokens:     cached,
	}
}
