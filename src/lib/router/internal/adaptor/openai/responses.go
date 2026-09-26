// OpenAI Responses API 适配器（协议名 `responses`；D-29）—— 与 chat completions 同为 OpenAI 家族
// 协议，故与 `openai.go` 同包（**不新增 module**），复用 `Options` / `internal/adaptor` 的共享件
// （`Exchange` 出网与流式读取、`SSEDecoder`、`StatusError`、`ToolCallAccumulator`）。
//
// 契约来源：OpenAI Responses API 规范 + DeepSeek《使用 Responses API》指南
// （https://api-docs.deepseek.com/zh-cn/guides/responses_api）；与 llm 侧既有
// `llm_responses.go`（protocol=responses 分支）逐项对齐，便于 LR-11 替换时行为可比。
//
// 与 chat completions 的**实质差异**（逐条处置）：
//
//	差异                                      处置
//	端点 `/responses`（非 `/chat/completions`） 独立适配器（本文件）；不回落、不静默改写
//	请求无 `messages`                           `input` 输入 item 列表（message / function_call /
//	                                            function_call_output / reasoning）
//	system 提示走顶层 `instructions`            历史最前的连续 system 消息 → `instructions`；其余
//	                                            system 保留为 message item（与 llm 侧同口径）
//	工具定义**扁平**（无 `function` 嵌套层）     `{type:function, name, description, parameters}`
//	输出上限字段 `max_output_tokens`            `Options.MaxTokens` 同名映射
//	思考强度 `reasoning.effort`（非顶层）       `Options.Reasoning.Effort` → `reasoning:{effort}`
//	函数调用按 **item_id** 聚合（无 `index`）    首次出现的 item_id 顺序即 canonical `Index`
//	事件**语义化**（无 `[DONE]`）               `response.*.delta` / `output_item.*` /
//	                                            `function_call_arguments.*` → canonical 七类事件
//	终态：completed / incomplete / failed       completed → done(stop|tool_calls)；incomplete →
//	                                            done(length)；failed → 分类错误（**不投 done**）
//	usage 在 `response.completed`               → `usage` 事件（终态前一条）
//	无 `finish_reason`                          由终态事件类型 + 是否有工具调用推导
//
// 断链判定（同 Anthropic 适配器）：EOF 落在事件边界但**未收到终态事件** → `network`（可重试）。
package openai

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot-router/internal/adaptor"
	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// Responses 流式事件的 `type` 取值（OpenAI / DeepSeek 官方口径）。
const (
	evResponsesOutputTextDelta = "response.output_text.delta"
	evResponsesReasoningDelta  = "response.reasoning_text.delta"
	evResponsesItemAdded       = "response.output_item.added"
	evResponsesItemDone        = "response.output_item.done"
	evResponsesArgsDelta       = "response.function_call_arguments.delta"
	evResponsesArgsDone        = "response.function_call_arguments.done"
	evResponsesCompleted       = "response.completed"
	evResponsesIncomplete      = "response.incomplete"
	evResponsesFailed          = "response.failed"
)

// 输入 item / 内容块类型（wire 层）。
const (
	itemFunctionCall       = "function_call"
	itemFunctionCallOutput = "function_call_output"
	itemMessage            = "message"
	itemReasoning          = "reasoning"

	partInputText     = "input_text"
	partOutputText    = "output_text"
	partInputImage    = "input_image"
	partReasoningText = "reasoning_text"
)

// ResponsesAdapter 是 OpenAI Responses API 适配器。
type ResponsesAdapter struct {
	opts Options
}

// NewResponsesAdapter 构造 Responses 适配器。
func NewResponsesAdapter(opts Options) *ResponsesAdapter { return &ResponsesAdapter{opts: opts} }

// Protocol 返回归一协议名（canon.ProtocolResponses）。
func (a *ResponsesAdapter) Protocol() string { return canon.ProtocolResponses }

// Caps 返回 Responses 协议的能力矩阵（LR-7 声明式降级据此进行）：与 chat completions 同族，差别
// 仅在思考强度字段（`reasoning.effort`）与工具定义为扁平形态（对外能力等价）。
func (a *ResponsesAdapter) Caps() canon.Caps {
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

// Stream 组装请求 → 发送 → 解析语义化 SSE → 按序写 canonical 事件（细节见文件头）。
// 失败返回分类错误（Router 据此投唯一一条 error 事件）；成功路径投且只投一条 done。
func (a *ResponsesAdapter) Stream(ctx context.Context, spec canon.Spec, req canon.Request, out chan<- canon.Event) error {
	caps := a.Caps()
	if err := adaptor.Degrade(caps, &req); err != nil {
		return err
	}
	body, berr := buildResponsesRequest(spec, req)
	if berr != nil {
		return berr
	}

	model := body.Model
	ex := adaptor.NewExchange(a.opts.HTTPClient, req.Options.ResponseTimeout, req.Options.StreamTimeout)
	acc := adaptor.NewToolCallAccumulator()
	idx := newResponsesIndex()
	var terminal bool
	var finish string
	var usage *canon.Usage

	err := ex.PostJSON(ctx, responsesEndpoint(spec.BaseURL), authHeaders(spec.APIKey), body, func(payload []byte) error {
		if adaptor.IsDone(payload) {
			// 文档明确 Responses 流无 `[DONE]`；兼容实现仍发 → 视作流结束（同 completed：stop），
			// 并立即收尾（不等上游关连接）。
			terminal = true
			finish = "stop"
			return adaptor.ErrStreamDone
		}
		var ev responsesEvent
		if json.Unmarshal(payload, &ev) != nil {
			return nil // 非 JSON 噪声载荷：跳过，不断流
		}
		if m := ev.Model(); m != "" {
			model = m
		}
		switch ev.Type {
		case evResponsesOutputTextDelta:
			if ev.Delta != "" {
				if err := adaptor.Emit(ctx, out, canon.Event{Type: canon.EvTextDelta, Text: ev.Delta, Model: model}); err != nil {
					return err
				}
			}
		case evResponsesReasoningDelta:
			if ev.Delta != "" {
				if err := adaptor.Emit(ctx, out, canon.Event{Type: canon.EvReasoningDelta, Text: ev.Delta, Model: model}); err != nil {
					return err
				}
			}
		case evResponsesItemAdded:
			if ev.Item.Type == itemFunctionCall {
				acc.Add(idx.of(ev.Item.ID), ev.Item.CallID, ev.Item.Name, ev.Item.Arguments)
			}
		case evResponsesArgsDelta:
			acc.Add(idx.of(ev.ItemID), "", "", ev.Delta)
		case evResponsesArgsDone:
			acc.Set(idx.of(ev.ItemID), ev.Arguments)
		case evResponsesItemDone:
			if ev.Item.Type == itemFunctionCall {
				acc.Set(idx.of(ev.Item.ID), ev.Item.Arguments)
			}
		case evResponsesCompleted:
			terminal = true
			finish = "stop"
			if u := ev.Response.Usage.canon(); u != nil {
				usage = u
			}
			return adaptor.ErrStreamDone // 终态事件 → 立即收尾（不等上游关连接）
		case evResponsesIncomplete:
			terminal = true
			finish = responsesLengthFinish(ev.Response.IncompleteDetails.Reason)
			return adaptor.ErrStreamDone // 终态事件 → 立即收尾
		case evResponsesFailed:
			return responsesFailureError(&ev)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !terminal {
		return canon.NewError(canon.ErrorNetwork, "流在终态事件（completed/incomplete/failed）前结束（断链）")
	}

	// 终态（只投一次）：拼装完成的 tool_call → usage → done。
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
		FinishReason: responsesFinish(finish, len(toolEvents) > 0),
		Model:        model,
	})
}

// responsesEndpoint 拼 `/responses` 端点（base 尾斜杠容忍；base 已含 `/v1` 时得到 `/v1/responses`，
// 与 OpenAI 官方路径一致）。
func responsesEndpoint(base string) string {
	return strings.TrimSuffix(strings.TrimSpace(base), "/") + "/responses"
}

// responsesFinish 归一终态原因：有工具调用（或上游显式 `tool_calls`）→ `tool_calls`（对齐 llm 侧
// `finishOrCalls`）；其余原样透传（`stop` / `length` …）。
func responsesFinish(finish string, hasToolCalls bool) string {
	if finish == "tool_calls" || hasToolCalls {
		return "tool_calls"
	}
	return finish
}

// responsesLengthFinish 把 `incomplete_details.reason` 映射为 canonical finish_reason：
// `max_output_tokens` → `length`（触发 llm 侧既有自动续写）；其余非空值原样透传；空 → `length`。
func responsesLengthFinish(reason string) string {
	switch strings.TrimSpace(reason) {
	case "", "max_output_tokens":
		return "length"
	default:
		return reason
	}
}

// responsesFailureError 把 `response.failed`（HTTP 200 + 流内失败）归为分类错误（与状态码分类
// 互补）：`rate_limit_exceeded` → rate_limit；`server_error` / `internal_error` → server；
// 其余 → protocol。
func responsesFailureError(ev *responsesEvent) *canon.Error {
	kind := canon.ErrorProtocol
	code, msg := "", ""
	if ev.Response.Error != nil {
		code, msg = ev.Response.Error.Code, ev.Response.Error.Message
	}
	switch code {
	case "rate_limit_exceeded":
		kind = canon.ErrorRateLimit
	case "server_error", "internal_error":
		kind = canon.ErrorServer
	}
	if strings.TrimSpace(msg) == "" {
		msg = code
	}
	if strings.TrimSpace(msg) == "" {
		return canon.NewError(kind, "responses 流失败（response.failed）")
	}
	return canon.NewError(kind, "responses 流失败: "+msg)
}
