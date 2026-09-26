// Package openai 是 OpenAI **家族**协议的适配器：本文件 = chat completions（LR-3）；`responses.go` =
// OpenAI Responses API（协议名 `responses`，D-29；DeepSeek `/responses` 亦走此协议，见该文件头差异表）。
//
// 覆盖的协议要素：
//   - 请求：`messages[].role/content`（文本 → string；含图片 → `[{type:text},{type:image_url}]`）、
//     `tools[].function.parameters`、`tool_choice=auto`、`stream:true`、`stream_options.include_usage`、
//     `temperature` / `top_p`、上限字段 `max_tokens` 或 `max_completion_tokens`（按模型择一，可配）；
//   - 响应：SSE `data:` 载荷（`choices[].delta`）→ canonical 七类事件；`usage` 在末片；
//   - 非 2xx：状态码 + 体（含**非 JSON** 体）→ 分类错误（`adaptor.StatusError`）。
//
// 已知差异坑的处置（OpenAI 兼容端点的现实差异）：
//
//	坑                                    处置
//	reasoning_content（非标准扩展）         尽力解析：`reasoning_content` / `reasoning` / `thinking` 三种别名依次尝试
//	                                        → reasoning_delta（都空 → 空，不当正文）
//	max_tokens vs max_completion_tokens    按模型名择一（o1/o3/o4/gpt-5* → max_completion_tokens），
//	                                       也可用 `Options.MaxTokensParam` 显式指定（见 maxTokensParam）
//	无 `[DONE]` 的流                        `finish_reason` 出现过 → 以 **EOF** 收尾、终态照投；`[DONE]` 与
//	                                        `finish_reason` **都未见** → 判为断链（`network`，可重试）
//	错误体非 JSON（网关 HTML 兜底页）        `ErrorMessage` 原样取值 → 仍走完整 Kind/Retry-After 分类
//	usage 在末片                            `stream_options.include_usage=true`；末片 usage 记为终态前唯一 usage 事件
//	忽略 `stream=true` 回单发 JSON           `adaptor.Exchange` 按 Content-Type 判定：整体单发解析（`choices[].message`）
//	data 行里的非 JSON 噪声                  跳过该载荷（不断流），与 llm 侧既有口径一致
//	上游把错误放进流内 `{"error":…}`        按 `error.type` 分类（`adaptor.ErrorTypeKind`）→ 返回错误（Router 投 error 事件）
package openai

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
	// MaxTokensParam 指定输出上限的字段名："" 或 "auto" → 按模型名择一；
	// 也可显式 "max_tokens" / "max_completion_tokens"（见 maxTokensParam）。
	MaxTokensParam string
}

// Adapter 是 OpenAI 兼容（chat completions）适配器。
type Adapter struct {
	opts Options
}

// New 构造适配器。
func New(opts Options) *Adapter { return &Adapter{opts: opts} }

// Protocol 返回归一协议名。
func (a *Adapter) Protocol() string { return canon.ProtocolOpenAI }

// Caps 返回 OpenAI 兼容协议的能力矩阵（LR-7 声明式降级据此进行）。
//
// 说明：`ReasoningEffort` 声明为 true（下发 `reasoning_effort`）—— 与 llm 侧既有行为一致
// （usr 配了 thinking/reasoningEffort 才下发）；严格 OpenAI 端点仅在 o 系模型接受该字段，
// 是否配置由调用方（llm / usr 配置）决定，本适配器不擅自吞掉。读侧的 `reasoning_content`
// 属非标准扩展，尽力解析、缺失不报错。
func (a *Adapter) Caps() canon.Caps {
	return canon.Caps{
		Stream:            true,
		Tools:             true,
		ToolChoice:        true,
		ParallelToolCalls: true,
		Reasoning:         true,
		ReasoningEffort:   true,
		Images:            true,
		TopP:              true,
		MaxTokensRequired: false,
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
	body, berr := buildRequest(spec, req, caps, a.opts.MaxTokensParam)
	if berr != nil {
		return berr
	}

	model := body.Model
	ex := adaptor.NewExchange(a.opts.HTTPClient, req.Options.ResponseTimeout, req.Options.StreamTimeout)
	acc := adaptor.NewToolCallAccumulator()
	var sawDone bool
	var finish string
	var usage *canon.Usage

	err := ex.PostJSON(ctx, endpoint(spec.BaseURL), authHeaders(spec.APIKey), body, func(payload []byte) error {
		if adaptor.IsDone(payload) {
			sawDone = true
			return adaptor.ErrStreamDone // `[DONE]`：终止标记 → 立即收尾（不等上游关连接）；终态事件在本方法末尾统一投一次
		}
		var chunk chatChunk
		if json.Unmarshal(payload, &chunk) != nil {
			return nil // 非 JSON 噪声载荷（兼容端点心跳 / 文本提示）：跳过，不断流
		}
		if chunk.Error != nil {
			return canon.NewError(adaptor.ErrorTypeKind(chunk.Error.Type), "上游流内错误: "+chunk.Error.Message)
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if u := chunk.Usage.canon(); u != nil {
			usage = u // 末片 usage（stream_options.include_usage）
		}
		for i := range chunk.Choices {
			c := chunk.Choices[i]
			if c.FinishReason != nil {
				finish = *c.FinishReason
			}
			d := c.body()
			if d.Content != "" {
				if err := adaptor.Emit(ctx, out, canon.Event{Type: canon.EvTextDelta, Text: d.Content, Model: model}); err != nil {
					return err
				}
			}
			if r := d.reasoning(); r != "" {
				if err := adaptor.Emit(ctx, out, canon.Event{Type: canon.EvReasoningDelta, Text: r, Model: model}); err != nil {
					return err
				}
			}
			for _, tc := range d.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				acc.Add(idx, tc.ID, tc.Function.Name, tc.Function.Arguments)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// 断链判定（与 Anthropic / Responses 适配器同口径）：OpenAI 家族的正常收尾必有 `[DONE]`
	// 或 `finish_reason`（兼容端点可省 `[DONE]` 但会带 `finish_reason`）；两者都未出现 = 流被
	// 截断（EOF 落在事件边界上，SSE 层无法察觉）→ network（可重试），由 llm 侧走断链续写。
	if !sawDone && finish == "" {
		return canon.NewError(canon.ErrorNetwork, "流在 [DONE]/finish_reason 前结束（断链）")
	}

	// 终态（只投一次）：拼装完成的 tool_call → usage（末片）→ done。
	toolEvents, terr := acc.Events(model)
	if terr != nil {
		return terr
	}
	for _, ev := range toolEvents {
		if err := adaptor.Emit(ctx, out, ev); err != nil {
			return err
		}
	}
	if usage != nil {
		if err := adaptor.Emit(ctx, out, canon.Event{Type: canon.EvUsage, Usage: usage, Model: model}); err != nil {
			return err
		}
	}
	return adaptor.Emit(ctx, out, canon.Event{
		Type:         canon.EvDone,
		FinishReason: finishReason(finish, len(toolEvents) > 0),
		Model:        model,
	})
}

// endpoint 拼 chat completions 端点（base 尾斜杠容忍）。
func endpoint(base string) string {
	return strings.TrimSuffix(strings.TrimSpace(base), "/") + "/chat/completions"
}

// authHeaders 组装鉴权头：apiKey 非空才带 `Authorization: Bearer`（空 = 无鉴权端点 / 本地 mock 可连）。
func authHeaders(apiKey string) map[string]string {
	if strings.TrimSpace(apiKey) == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + strings.TrimSpace(apiKey)}
}

// finishReason 归一终态原因：有工具调用但无 finish_reason（无 `[DONE]` 的流以 EOF 收尾）→ tool_calls；
// 其余原样透传（"stop" / "length" / "tool_calls" / "content_filter" …）。
func finishReason(finish string, hasToolCalls bool) string {
	if finish == "" && hasToolCalls {
		return "tool_calls"
	}
	return finish
}
