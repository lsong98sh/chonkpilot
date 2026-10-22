// chat 引擎宿主适配器（MOD-1 M5b-①）：router→LLMClient / data→TurnStore / MQ→EventSink。
//
// 本文件**不改生产路径**：现有 turn 仍走 `turnCtx`；适配器供影子运行 / 后续切换使用。
// 约束：适配器内部可依赖 router / data / mq；chat 组件本身仍零依赖（见 chat/deps_test.go）。
package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/chonkpilot/chonkpilot-chat"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-router"
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

	tools := make([]ToolDef, 0, len(req.Tools))
	for _, t := range req.Tools {
		var params any
		if len(t.InputSchema) > 0 {
			_ = json.Unmarshal(t.InputSchema, &params)
		}
		tools = append(tools, ToolDef{Name: t.Name, Description: t.Description, Parameters: params})
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

// ─── TurnStore（data 落库 / 事件日志 / inbox 原子消费）───────────────

// chatTurnStore 是 per-turn 的 TurnStore 适配器（turn 由构造时绑定）。
type chatTurnStore struct {
	s          *Server
	st         *sessionStore
	instanceID string
	sessionID  string
	turnID     string
}

// newChatTurnStore 构造 TurnStore 适配器。
func newChatTurnStore(s *Server, instanceID, sessionID, turnID string) chat.TurnStore {
	return &chatTurnStore{
		s: s, st: newSessionStore(s.bus, instanceID),
		instanceID: instanceID, sessionID: sessionID, turnID: turnID,
	}
}

func (a *chatTurnStore) SaveMessage(_ context.Context, msg chat.Message) error {
	_, err := a.st.AppendFullKeyed(a.turnID, msg, "")
	return err
}

// CommitInbox 同事务落消息 + 置 consumed（`data-inbox-commit`，MOD-1 M6）：消息主键由
// inbox_id 确定性推出，重复提交幂等（不重复落库）。
func (a *chatTurnStore) CommitInbox(_ context.Context, inboxID string, msg chat.Message) error {
	var m map[string]any
	if b, err := json.Marshal(ChatMessageToData(msg)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	_, err := dataRequest(a.s.bus, msgkeys.TopicDataInboxCommit, map[string]any{
		"instance_id": a.instanceID,
		"data": map[string]any{
			"inbox_id":   inboxID,
			"turn_id":    a.turnID,
			"session_id": a.sessionID,
			"msg":        m,
		},
	})
	return err
}

func (a *chatTurnStore) AppendEvent(_ context.Context, ev chat.TurnEvent) error {
	var payload any
	if len(ev.Payload) > 0 {
		_ = json.Unmarshal(ev.Payload, &payload)
	}
	_, err := dataRequest(a.s.bus, msgkeys.TopicDataTurnEventAppend, map[string]any{
		"instance_id": a.instanceID,
		"data": map[string]any{
			"session_id": a.sessionID,
			"turn_id":    a.turnID,
			"seq":        ev.Seq,
			"type":       string(ev.Type),
			"at":         ev.At.UTC().Format(time.RFC3339Nano),
			"payload":    payload,
		},
	})
	return err
}

// ─── EventSink（MQ 事件）─────────────────────────────────────────────

// chatEventSink 把引擎事件映射到既有对外主题（零新增主题）。
//
// 映射表（未列出的类型 = 有意不映射）：
//   - `llm_delta`            → `llm-receive`（NotifyTypeText）
//   - `llm_retry`            → `llm-retry`
//   - `turn_complete`        → `llm-complete`
//   - `session_interrupted`  → `llm-interrupted`（与生产 `raiseInterrupted` 同形载荷）
//   - `tool_result`          → 不映射：UI 呈现来自落库消息（`data-session-append-message` 桥转发）
//   - `tool_progress`        → 不映射：任务事件（`task-*`）承载
//   - `ask_user`             → 不映射：宿主 `ToolInvoker` 的 ask 钩子（`ask-user` 卡片）承载
type chatEventSink struct {
	s          *Server
	instanceID string
	sessionID  string
	turnID     string
	parents    []string
}

// newChatEventSink 构造事件适配器。
func newChatEventSink(s *Server, instanceID, sessionID, turnID string, parents []string) chat.EventSink {
	return &chatEventSink{s: s, instanceID: instanceID, sessionID: sessionID, turnID: turnID, parents: parents}
}

func (e *chatEventSink) Emit(_ context.Context, ev chat.Event) {
	switch ev.Type {
	case chat.EventLLMDelta:
		if ev.Text == "" {
			return
		}
		e.s.publish("llm-receive", map[string]any{
			"type":        NotifyTypeText,
			"payload":     map[string]any{"text": ev.Text},
			"instance_id": e.instanceID, "session": e.sessionID, "turn": e.turnID,
		})
	case chat.EventLLMRetry:
		if ev.Retry == nil {
			return
		}
		e.s.publish("llm-retry", map[string]any{
			"instance_id": e.instanceID, "session": e.sessionID, "turn": e.turnID,
			"retry_attempt": ev.Retry.Attempt, "retry_count": ev.Retry.Count,
			"retry_wait_ms": ev.Retry.WaitMilli,
		})
	case chat.EventTurnComplete:
		payload := map[string]any{
			"instance_id": e.instanceID, "session": e.sessionID, "turn": e.turnID,
			"status": ev.Status, "parents": e.parents,
		}
		if ev.Text != "" {
			payload["text"] = ev.Text
		}
		e.s.publish("llm-complete", payload)
	case chat.EventSessionInterrupted:
		// 与生产 `turnCtx.raiseInterrupted` 同形：code / retryable / auto_continue_delay_ms
		// + 可选 message（空则省略）。
		payload := map[string]any{
			"instance_id": e.instanceID, "session": e.sessionID, "turn": e.turnID,
			"code": ev.Code, "retryable": ev.Retryable,
			"auto_continue_delay_ms": ev.DelayMilli,
		}
		if ev.Text != "" {
			payload["message"] = ev.Text
		}
		e.s.publish("llm-interrupted", payload)
	}
}

// 编译期断言：三个适配器满足 chat 端口。
var (
	_ chat.LLMClient = (*chatLLMClient)(nil)
	_ chat.TurnStore = (*chatTurnStore)(nil)
	_ chat.EventSink = (*chatEventSink)(nil)
)
