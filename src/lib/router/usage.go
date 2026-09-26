// usage 归一的**对外口径**（LR-9）。
//
// 定义处 = `internal/canon.Usage`（本文件以别名再导出）；**归一实现** = `internal/adaptor.NormalizeUsage`
// （各协议适配器共用的唯一入口）。本文件的职责 = 把 LR-9 的统一口径**登记在对外层**，供 llm 侧
// （LR-11 接线）与文档对照。
//
// 统一映射表（各家字段 → canonical 字段）：
//
//	canonical           OpenAI 兼容（chat）                     Anthropic（/v1/messages）         OpenAI Responses
//	PromptTokens        usage.prompt_tokens                     message_start.usage.input_tokens   usage.input_tokens
//	CompletionTokens    usage.completion_tokens                 message_delta.usage.output_tokens  usage.output_tokens
//	TotalTokens         usage.total_tokens（缺失 → 求和）        **不下发** → 由前两项求和          usage.total_tokens
//	ReasoningTokens     …completion_tokens_details.             **不下发**（thinking 计入 output） …output_tokens_details.
//	                    reasoning_tokens                                                          reasoning_tokens
//	CachedTokens        …prompt_tokens_details.cached_tokens    cache_read_input_tokens            …input_tokens_details.
//	                                                                                              cached_tokens
//
// **缺失字段的语义（定稿口径）**：每个字段 **`0` = 上游未提供（未知）** —— 不区分「上游明确给了 0」
// 与「上游没给」（**缺失即 0**，不做指针化零值区分）；唯一例外是 `TotalTokens`，上游未提供（0）而
// 分项非零时**由分项求和补齐**（确定性推导）。理由：消费方（计费展示 / 日志 / 归因）对「0 或未知」
// 的处理一致，指针化只会把不确定性扩散到整个 canonical 层。
package router

import "github.com/chonkpilot/chonkpilot-router/internal/canon"

// Usage 是一次「一次调用」的 token 统计（字段 0 = 上游未提供；映射表见文件头）。
type Usage = canon.Usage
