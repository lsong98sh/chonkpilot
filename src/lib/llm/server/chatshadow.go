// chat 引擎宿主适配器（MOD-1 M5b-①）：router→LLMClient。
//
// 本文件**不改生产路径**：现有 turn 仍走 `turnCtx`；适配器供引擎执行路径使用。
// 约束：适配器内部可依赖 router / data / mq；chat 组件本身仍零依赖（见 chat/deps_test.go）。
package server

import (
	"context"
	"time"

	"github.com/lsong98sh/chonkpilot-agent/chat"
	"github.com/lsong98sh/chonkpilot-agent/router"
)

// ─── LLMClient（router 报文级 step）───────────────────────────────────

// chatLLMClient 把 `chat.LLMClient` 接到 `Server.chat`（router）。
type chatLLMClient struct {
	s    *Server
	spec router.Spec
	opts ChatOptions // 模板：模型/超时/think/effort/图片；模型可由 StepRequest 覆盖
}

// newChatLLMClient 构造 LLM step 适配器。
func newChatLLMClient(s *Server, spec router.Spec, opts ChatOptions) chat.LLMClient {
	return &chatLLMClient{s: s, spec: spec, opts: opts}
}

func (c *chatLLMClient) Step(ctx context.Context, req chat.StepRequest, opts chat.StepOptions) (<-chan chat.StepEvent, error) {
	co := c.opts
	if req.Model != "" {
		co.Model = req.Model
	}
	co.MaxRetries = opts.MaxRetries
	co.RetryBudget = opts.RetryBudget
	if opts.OnRetry != nil {
		onRetry := opts.OnRetry
		co.RetryObserver = retryObserverFunc(func(attempt int, wait time.Duration) {
			onRetry(attempt, wait)
		})
	}

	// LLM-P4：schema 已是 JSON 原文（`ToolSpec.InputSchema`）→ 直接透传给 router，
	// 不再「unmarshal 成 map → router 再 marshal」的每 step 双向往返。
	// 空 schema 保持 nil（router 侧仍按 `json.Marshal(nil)` = `null` 下发，与改前逐字节一致）。
	tools := make([]ToolDef, 0, len(req.Tools))
	for _, t := range req.Tools {
		def := ToolDef{Name: t.Name, Description: t.Description}
		if len(t.InputSchema) > 0 {
			def.ParamJSON = t.InputSchema
		}
		tools = append(tools, def)
	}

	stream, err := c.s.chat(ctx, c.spec, req.Messages, tools, co)
	if err != nil {
		return nil, err
	}
	out := make(chan chat.StepEvent, 8)
	go func() {
		defer close(out)
		send := func(ev chat.StepEvent) bool {
			select {
			case out <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for ev := range stream {
			if ev.Err != nil {
				send(chat.StepEvent{Type: chat.StepEventDone, Err: ev.Err})
				return
			}
			if ev.Content != "" {
				if !send(chat.StepEvent{Type: chat.StepEventDelta, Text: ev.Content}) {
					return
				}
			}
			if ev.Reasoning != "" {
				if !send(chat.StepEvent{Type: chat.StepEventReasoning, Text: ev.Reasoning}) {
					return
				}
			}
			if ev.Done {
				send(chat.StepEvent{Type: chat.StepEventDone, ToolCalls: ev.ToolCalls, Finish: ev.FinishReason})
				return
			}
		}
	}()
	return out, nil
}

// retryObserverFunc 把 router 的回调折成单一函数（`OnExhausted` 由终态本身承载）。
type retryObserverFunc func(attempt int, wait time.Duration)

func (f retryObserverFunc) OnRetry(_ context.Context, rc router.RetryContext, wait time.Duration) {
	f(rc.Attempt, wait)
}

func (retryObserverFunc) OnExhausted(context.Context, router.RetryContext, *router.Error) {}

// 编译期断言：LLM 端口适配器满足 chat 端口。
var _ chat.LLMClient = (*chatLLMClient)(nil)
