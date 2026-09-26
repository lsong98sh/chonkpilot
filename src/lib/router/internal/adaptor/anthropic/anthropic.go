// Package anthropic 是 Anthropic `/v1/messages` 协议的适配器（LR-4）。
//
// 覆盖的协议要素：
//   - 请求：`system` **顶层**、`messages[].content` 内容块（`text` / `image` / `tool_use` /
//     `tool_result`）、`tools[].input_schema`、`max_tokens`（**必填**，缺省见 defaultMaxTokens）、
//     `stream:true`、`thinking`（`budget_tokens`）、`temperature` / `top_p`；
//   - 响应：SSE（`event:` 行由 `data:` 载荷内的 `type` 字段承载）—— `message_start` /
//     `content_block_start` / `content_block_delta`（`text_delta` / `thinking_delta` /
//     `input_json_delta`）/ `content_block_stop` / `message_delta`（`stop_reason` + usage）/
//     `message_stop` / `ping` / `error` → canonical 七类事件。
//
// 与 canonical 的**双向**映射及其差异（逐条处置）：
//
//	差异                                     处置
//	system 无独立消息角色                      canonical `role=system` 的文本 → 顶层 `system`（多段以空行拼接）
//	tool_result 只能落在 **user** 消息内       canonical `role=tool` → 并入 user 消息的 `tool_result` 块
//	相邻同角色消息                            合并为一条（tool_result 并入 user 后保持 user/assistant 交替语义）
//	首条消息必须是 user                       非 user 起首 → **明确报错**（invalid；不擅自插入空 user 消息）
//	空内容块被拒                              空文本 / 空结果块不发；全部消息为空 → **明确报错**（invalid）
//	thinking 回传需带 signature               D-31：canonical `Message.ReasoningSignature` 承载签名 →
//	                                          `Reasoning` 与 `ReasoningSignature` **齐备**时才回传
//	                                          `thinking` 块；缺签名则**不回传**（回传缺签名会被上游 400）
//	thinking 与 temperature/top_p 互斥        启用 thinking 时**省略** temperature / top_p（软降级，见下）
//	tool_use.input 必须是对象                 canonical 的 `Arguments`（JSON 串）解析为对象；非法 → 报错
//	usage 分两处（message_start / message_delta）跨事件累加后于终态前投一条 usage 事件（口径见
//	                                          `adaptor.NormalizeUsage`，LR-9）
//	无 total_tokens                           由 prompt + completion 求和
//	思考签名分片（signature_delta）           增量拼接 → 随 `done` 事件以 canonical `Event.Signature`
//	                                          回带上层（供多轮请求侧回传保真）
package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/chonkpilot/chonkpilot-router/internal/adaptor"
	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// Options 是适配器构造选项。
type Options struct {
	// HTTPClient 出网客户端（nil = 自建默认客户端；连接池寿命由调用方决定）。
	HTTPClient *http.Client
}

// Adapter 是 Anthropic `/v1/messages` 适配器。
type Adapter struct {
	opts Options
}

// New 构造适配器。
func New(opts Options) *Adapter { return &Adapter{opts: opts} }

// Protocol 返回归一协议名。
func (a *Adapter) Protocol() string { return canon.ProtocolAnthropic }

// Caps 返回 Anthropic 协议的能力矩阵（LR-7 声明式降级据此进行）。
func (a *Adapter) Caps() canon.Caps {
	return canon.Caps{
		Stream:            true,
		Tools:             true,
		ToolChoice:        true,
		ParallelToolCalls: true,
		Reasoning:         true,
		ReasoningEffort:   true, // 档位 → thinking.budget_tokens（见 thinkingBudget）
		Images:            true,
		TopP:              true,
		MaxTokensRequired: true, // max_tokens 必填 → 缺值补 defaultMaxTokens
		UsageInStream:     true,
		SystemRole:        true,
	}
}

// Stream 组装请求 → 发送 → 解析流 → 按序写 canonical 事件（细节见包注释）。
// 失败返回分类错误（Router 据此投唯一一条 error 事件）；成功路径投且只投一条 done。
func (a *Adapter) Stream(ctx context.Context, spec canon.Spec, req canon.Request, out chan<- canon.Event) error {
	caps := a.Caps()
	if err := adaptor.Degrade(caps, &req); err != nil {
		return err
	}
	body, berr := buildRequest(spec, req)
	if berr != nil {
		return berr
	}

	model := body.Model
	ex := adaptor.NewExchange(a.opts.HTTPClient, req.Options.ResponseTimeout, req.Options.StreamTimeout)
	acc := adaptor.NewToolCallAccumulator()
	var stopReason string
	var stopped bool
	var signature strings.Builder // 思考块签名（signature_delta 增量拼接，D-31）
	var promptTokens, completionTokens, cachedTokens int
	var hasUsage bool

	err := ex.PostJSON(ctx, endpoint(spec.BaseURL), headers(spec.APIKey), body, func(payload []byte) error {
		var ev streamEvent
		if json.Unmarshal(payload, &ev) != nil {
			return nil // 非 JSON 噪声载荷：跳过，不断流
		}
		if ev.Model != "" {
			model = ev.Model
		}
		switch ev.Type {
		case eventMessageStart:
			if ev.Message != nil {
				if ev.Message.Model != "" {
					model = ev.Message.Model
				}
				if ev.Message.Usage != nil {
					promptTokens = ev.Message.Usage.InputTokens
					cachedTokens = ev.Message.Usage.CacheReadInputTokens
					hasUsage = true
				}
			}
		case eventContentBlockStart:
			// tool_use 块起始：id / name 只在此出现 → 先按 content block index 落位
			// （后续 input_json_delta 只有参数片段，且共享同一 index）；
			// thinking 块起始若已带签名 → 先落位（后续 signature_delta 继续追加）。
			if ev.ContentBlock != nil {
				switch ev.ContentBlock.Type {
				case contentBlockToolUse:
					acc.Add(ev.Index, ev.ContentBlock.ID, ev.ContentBlock.Name, "")
				case contentBlockThinking:
					signature.WriteString(ev.ContentBlock.Signature)
				}
			}
		case eventContentBlockDelta:
			if ev.Delta == nil {
				return nil
			}
			switch ev.Delta.Type {
			case deltaText:
				if ev.Delta.Text != "" {
					if err := adaptor.Emit(ctx, out, canon.Event{Type: canon.EvTextDelta, Text: ev.Delta.Text, Model: model}); err != nil {
						return err
					}
				}
			case deltaThinking:
				if ev.Delta.Thinking != "" {
					if err := adaptor.Emit(ctx, out, canon.Event{Type: canon.EvReasoningDelta, Text: ev.Delta.Thinking, Model: model}); err != nil {
						return err
					}
				}
			case deltaInputJSON:
				acc.Add(ev.Index, "", "", ev.Delta.PartialJSON)
			case deltaSignature:
				// 签名（D-31）：canonical `Event.Signature` 承载，随 done 事件回带上层；
				// 多轮请求时由上层（llm）写入 `Message.ReasoningSignature` 供请求侧回传。
				signature.WriteString(ev.Delta.Signature)
			}
		case eventMessageDelta:
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				stopReason = ev.Delta.StopReason
			}
			if ev.Usage != nil {
				completionTokens = ev.Usage.OutputTokens
				hasUsage = true
			}
		case eventMessageStop:
			stopped = true
			return adaptor.ErrStreamDone // 终态事件 → 立即收尾（不等上游关连接）
		case eventError:
			if ev.Error == nil {
				return canon.NewError(canon.ErrorProtocol, "上游流内错误（无 error 字段）")
			}
			return canon.NewError(adaptor.ErrorTypeKind(ev.Error.Type), "上游流内错误: "+ev.Error.Message)
		case eventPing:
			// 心跳：无载荷语义
		}
		return nil
	})
	if err != nil {
		return err
	}
	// 断链判定：Anthropic 的正常收尾必有 `message_delta.stop_reason` 或 `message_stop`；
	// 两者都没有 = 流被截断（EOF 落在事件边界上，SSE 层无法察觉）→ network（可重试）。
	if !stopped && stopReason == "" {
		return canon.NewError(canon.ErrorNetwork, "流在 message_stop 前结束（断链）")
	}

	// 终态（只投一次）：拼装完成的 tool_call → usage → done（含思考签名）。
	toolEvents, terr := acc.Events(model)
	if terr != nil {
		return terr
	}
	for _, ev := range toolEvents {
		if err := adaptor.Emit(ctx, out, ev); err != nil {
			return err
		}
	}
	if hasUsage {
		// Anthropic 不下发 total（且有单一 output 口径，reasoning 计入 output）→ 由前两项求和，见
		// `adaptor.NormalizeUsage`（LR-9）。
		usage := adaptor.NormalizeUsage(promptTokens, completionTokens, 0, 0, cachedTokens)
		if err := adaptor.Emit(ctx, out, canon.Event{Type: canon.EvUsage, Usage: &usage, Model: model}); err != nil {
			return err
		}
	}
	return adaptor.Emit(ctx, out, canon.Event{
		Type:         canon.EvDone,
		FinishReason: finishReason(stopReason, len(toolEvents) > 0),
		Model:        model,
		Signature:    signature.String(),
	})
}

// endpoint 拼 `/v1/messages` 端点：base 已含版本段（以 `/v1` 结尾）→ 只补 `/messages`；
// 否则补 `/v1/messages`（容忍尾斜杠 / 大小写差异）。
func endpoint(base string) string {
	b := strings.TrimSuffix(strings.TrimSpace(base), "/")
	if strings.HasSuffix(b, "/v1") {
		return b + "/messages"
	}
	return b + "/v1/messages"
}

// headers 组装请求头：`anthropic-version` 恒发；`x-api-key` 仅在 apiKey 非空时发（空 = 无鉴权
// 兼容端点 / 本地 mock 可连）。
func headers(apiKey string) map[string]string {
	h := map[string]string{"anthropic-version": apiVersion}
	if strings.TrimSpace(apiKey) != "" {
		h["x-api-key"] = strings.TrimSpace(apiKey)
	}
	return h
}

// finishReason 把 Anthropic `stop_reason` 归一到 canonical 口径：
// `end_turn` / `stop_sequence` → `stop`；`tool_use` → `tool_calls`；`max_tokens` → `length`；
// 空 / 未知原样透传。有工具调用但无 stop_reason → `tool_calls`。
func finishReason(stopReason string, hasToolCalls bool) string {
	switch stopReason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "":
		if hasToolCalls {
			return "tool_calls"
		}
	}
	return stopReason
}
