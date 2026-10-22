// TurnState 与事件日志（MOD-1 M3）：状态只能经 `Apply(state, event)` 变更。
//
// 硬约束（见专题 §3.3 / §6.1）：
//   - 禁止就地修改状态；`Apply` 接收旧值、返回新值（切片按需复制）；
//   - 事件 `Seq` 单调递增（作用域 = per-turn），乱序 / 重复 → 显式失败；
//   - 未知事件类型 → 显式失败（重放不得"看似成功、实际丢步"）；
//   - 状态只保留可序列化字段（JSON 往返稳定，供崩溃恢复 / 重放）。
package chat

import (
	"encoding/json"
	"fmt"
)

// TurnStateVersion 是 TurnState 快照结构版本（不兼容 → 收敛到终态并保留已落消息）。
const TurnStateVersion = 1

// TurnStatus 是 turn 状态。
type TurnStatus string

const (
	TurnStatusPending      TurnStatus = "pending"       // 已受理、未开跑
	TurnStatusRunning      TurnStatus = "running"       // 正在跑（LLM step / 帧内处理）
	TurnStatusAwaitingTool TurnStatus = "awaiting_tool" // 等工具回执（含异步挂起）
	TurnStatusAwaitingUser TurnStatus = "awaiting_user" // 等用户（ask_user）
	TurnStatusCompleted    TurnStatus = "completed"
	TurnStatusInterrupted  TurnStatus = "interrupted"
	TurnStatusFailed       TurnStatus = "failed"
	TurnStatusCanceled     TurnStatus = "canceled"
)

// Terminal 报告是否终态（终态不可再推进，除清理 / 收敛）。
func (s TurnStatus) Terminal() bool {
	switch s {
	case TurnStatusCompleted, TurnStatusInterrupted, TurnStatusFailed, TurnStatusCanceled:
		return true
	}
	return false
}

// Inflight 是已派发、等回执的工具调用。
type Inflight struct {
	OpID   string `json:"op_id,omitempty"`
	CallID string `json:"call_id"`
	Async  bool   `json:"async,omitempty"`
}

// FrameState 是插入帧栈的一层（LIFO；含父帧引用与栈深，供恢复时重建）。
type FrameState struct {
	FrameID       string       `json:"frame_id"`
	ParentFrameID string       `json:"parent_frame_id,omitempty"`
	Depth         int          `json:"depth"`
	Stash         []ToolResult `json:"stash,omitempty"`
	ResumeNote    string       `json:"resume_note,omitempty"`
}

// TurnState 是 turn 的可序列化状态（快照 / 恢复 / 重放）。
type TurnState struct {
	Version    int          `json:"version"`
	SessionID  string       `json:"session_id"`
	TurnID     string       `json:"turn_id"`
	Status     TurnStatus   `json:"status"`
	Seq        uint64       `json:"seq"`
	Pending    []ToolCall   `json:"pending"`
	Inflight   []Inflight   `json:"inflight"`
	Frames     []FrameState `json:"frames"`
	Stash      []ToolResult `json:"stash"`
	ResumeNote string       `json:"resume_note"`
}

// NewTurnState 构造初始状态（pending；版本固定当前值）。
func NewTurnState(sessionID, turnID string) TurnState {
	return TurnState{
		Version:   TurnStateVersion,
		SessionID: sessionID,
		TurnID:    turnID,
		Status:    TurnStatusPending,
	}
}

// TurnEventType 是状态转移事件类型（与对外 EventType 区分：这里是持久化事件日志）。
type TurnEventType string

const (
	TurnEventStart        TurnEventType = "turn_start"
	TurnEventStepStart    TurnEventType = "step_start"
	TurnEventStepDone     TurnEventType = "step_done"
	TurnEventLLMDelta     TurnEventType = "llm_delta"
	TurnEventLLMDone      TurnEventType = "llm_done"
	TurnEventToolDispatch TurnEventType = "tool_dispatch"
	TurnEventToolResult   TurnEventType = "tool_result"
	TurnEventInboxInsert  TurnEventType = "inbox_insert"
	TurnEventInboxConsume TurnEventType = "inbox_consume"
	TurnEventContinue     TurnEventType = "continue"
	TurnEventCancel       TurnEventType = "cancel"
	TurnEventEnd          TurnEventType = "turn_end"
)

// 事件载荷（JSON 形状；生产 / 重放共用）。

// TurnStartPayload 是 turn_start 的载荷（缺省沿用状态上的 session/turn）。
type TurnStartPayload struct {
	SessionID string `json:"session_id,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
}

// StepDonePayload 是 step_done 的载荷（LLM 本轮请求的工具调用 + 是否进入等用户）。
type StepDonePayload struct {
	Pending      []ToolCall `json:"pending,omitempty"`
	AwaitingUser bool       `json:"awaiting_user,omitempty"`
	PendingTool  *ToolCall  `json:"pending_tool,omitempty"` // ask_user 一类（AwaitingUser=true 时）
}

// ToolDispatchPayload 是 tool_dispatch 的载荷。
type ToolDispatchPayload struct {
	OpID   string `json:"op_id,omitempty"`
	CallID string `json:"call_id"`
	Async  bool   `json:"async,omitempty"`
}

// InboxConsumePayload 是 inbox_consume 的载荷。
type InboxConsumePayload struct {
	FrameID string `json:"frame_id"`
}

// TurnEndPayload 是 turn_end 的载荷。
type TurnEndPayload struct {
	Status TurnStatus `json:"status"`
}

// Apply 应用一条事件，返回新状态（不修改入参）。
func Apply(state TurnState, ev TurnEvent) (TurnState, error) {
	if ev.Seq == 0 {
		return state, fmt.Errorf("turnstate: event %q seq must be > 0", ev.Type)
	}
	if ev.Seq <= state.Seq {
		return state, fmt.Errorf("turnstate: event seq %d <= state seq %d (duplicate/out-of-order)", ev.Seq, state.Seq)
	}
	if state.Version == 0 {
		state.Version = TurnStateVersion
	}
	if state.Version != TurnStateVersion {
		return state, fmt.Errorf("turnstate: unsupported version %d (want %d)", state.Version, TurnStateVersion)
	}

	next := state // 值拷贝；切片在写分支内复制
	switch ev.Type {
	case TurnEventStart:
		if state.Status != TurnStatusPending {
			return state, fmt.Errorf("turnstate: turn_start in status %q", state.Status)
		}
		var p TurnStartPayload
		if err := decodePayload(ev, &p); err != nil {
			return state, err
		}
		if p.SessionID != "" {
			next.SessionID = p.SessionID
		}
		if p.TurnID != "" {
			next.TurnID = p.TurnID
		}
		next.Status = TurnStatusRunning

	case TurnEventStepStart:
		if state.Status.Terminal() {
			return state, fmt.Errorf("turnstate: step_start in terminal status %q", state.Status)
		}
		next.Status = TurnStatusRunning

	case TurnEventStepDone:
		if state.Status.Terminal() {
			return state, fmt.Errorf("turnstate: step_done in terminal status %q", state.Status)
		}
		var p StepDonePayload
		if err := decodePayload(ev, &p); err != nil {
			return state, err
		}
		next.Pending = cloneToolCalls(p.Pending)
		switch {
		case p.AwaitingUser:
			next.Status = TurnStatusAwaitingUser
		case len(next.Pending) > 0:
			next.Status = TurnStatusAwaitingTool
		default:
			next.Status = TurnStatusRunning
		}

	case TurnEventLLMDelta, TurnEventLLMDone, TurnEventContinue:
		// 无状态变更（llm_delta 仅 verbose 记录；continue 仅标记输入来源）。

	case TurnEventToolDispatch:
		if state.Status.Terminal() {
			return state, fmt.Errorf("turnstate: tool_dispatch in terminal status %q", state.Status)
		}
		var p ToolDispatchPayload
		if err := decodePayload(ev, &p); err != nil {
			return state, err
		}
		next.Pending = withoutToolCall(state.Pending, p.CallID)
		next.Inflight = appendInflight(state.Inflight, Inflight{OpID: p.OpID, CallID: p.CallID, Async: p.Async})
		next.Status = TurnStatusAwaitingTool

	case TurnEventToolResult:
		var res ToolResult
		if err := decodePayload(ev, &res); err != nil {
			return state, err
		}
		next.Inflight = withoutInflight(state.Inflight, res.CallID)
		next.Pending = withoutToolCall(next.Pending, res.CallID)
		next.Stash = appendToolResult(state.Stash, res)
		if !state.Status.Terminal() && len(next.Pending) == 0 && len(next.Inflight) == 0 {
			next.Status = TurnStatusRunning
		}

	case TurnEventInboxInsert:
		var f FrameState
		if err := decodePayload(ev, &f); err != nil {
			return state, err
		}
		if f.FrameID == "" {
			return state, fmt.Errorf("turnstate: inbox_insert frame_id required")
		}
		next.Frames = appendFrame(state.Frames, f)
		if !state.Status.Terminal() {
			next.Status = TurnStatusRunning
		}

	case TurnEventInboxConsume:
		var p InboxConsumePayload
		if err := decodePayload(ev, &p); err != nil {
			return state, err
		}
		frames, err := popFrame(state.Frames, p.FrameID)
		if err != nil {
			return state, err
		}
		next.Frames = frames

	case TurnEventCancel:
		next.Status = TurnStatusCanceled

	case TurnEventEnd:
		var p TurnEndPayload
		if err := decodePayload(ev, &p); err != nil {
			return state, err
		}
		if !p.Status.Terminal() {
			return state, fmt.Errorf("turnstate: turn_end status %q is not terminal", p.Status)
		}
		next.Status = p.Status

	default:
		return state, fmt.Errorf("turnstate: unknown event type %q (replay must fail loud)", ev.Type)
	}

	next.Seq = ev.Seq
	return next, nil
}

// decodePayload 解析事件载荷（空载荷 → 零值，合法）。
func decodePayload(ev TurnEvent, out any) error {
	if len(ev.Payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(ev.Payload, out); err != nil {
		return fmt.Errorf("turnstate: decode %s payload: %w", ev.Type, err)
	}
	return nil
}

func cloneToolCalls(in []ToolCall) []ToolCall {
	if len(in) == 0 {
		return nil
	}
	out := make([]ToolCall, len(in))
	copy(out, in)
	return out
}

func withoutToolCall(in []ToolCall, callID string) []ToolCall {
	if len(in) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(in))
	for _, c := range in {
		if c.ID != callID {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func appendInflight(in []Inflight, v Inflight) []Inflight {
	for _, x := range in {
		if x.CallID == v.CallID {
			return in // 已存在（重复 dispatch）→ 幂等
		}
	}
	out := make([]Inflight, len(in), len(in)+1)
	copy(out, in)
	return append(out, v)
}

func withoutInflight(in []Inflight, callID string) []Inflight {
	if len(in) == 0 {
		return nil
	}
	out := make([]Inflight, 0, len(in))
	for _, x := range in {
		if x.CallID != callID {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func appendToolResult(in []ToolResult, v ToolResult) []ToolResult {
	out := make([]ToolResult, len(in), len(in)+1)
	copy(out, in)
	return append(out, v)
}

func appendFrame(in []FrameState, f FrameState) []FrameState {
	out := make([]FrameState, len(in), len(in)+1)
	copy(out, in)
	return append(out, f)
}

func popFrame(in []FrameState, frameID string) ([]FrameState, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("turnstate: inbox_consume on empty frame stack")
	}
	top := in[len(in)-1]
	if top.FrameID != frameID {
		return nil, fmt.Errorf("turnstate: inbox_consume %q but top frame is %q (LIFO violated)", frameID, top.FrameID)
	}
	out := make([]FrameState, len(in)-1)
	copy(out, in[:len(in)-1])
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// ─── 事件日志分级（turnEventLog）────────────────────────────────────

// TurnEventLogLevel 是事件日志分级。
type TurnEventLogLevel string

const (
	TurnEventLogNone    TurnEventLogLevel = "none"    // 不写（不可重放）
	TurnEventLogMinimal TurnEventLogLevel = "minimal" // 仅 turn_start / turn_end
	TurnEventLogOutline TurnEventLogLevel = "outline" // 状态转移（默认；满足重放判据）
	TurnEventLogVerbose TurnEventLogLevel = "verbose" // outline + llm_delta
)

// ParseTurnEventLogLevel 解析配置值；空 / 未知 → 默认 outline（配置层另有校验告警）。
func ParseTurnEventLogLevel(s string) TurnEventLogLevel {
	switch TurnEventLogLevel(s) {
	case TurnEventLogNone:
		return TurnEventLogNone
	case TurnEventLogMinimal:
		return TurnEventLogMinimal
	case TurnEventLogVerbose:
		return TurnEventLogVerbose
	default:
		return TurnEventLogOutline
	}
}

// Records 报告该级别是否记录某事件类型。
func (l TurnEventLogLevel) Records(t TurnEventType) bool {
	switch l {
	case TurnEventLogNone:
		return false
	case TurnEventLogMinimal:
		return t == TurnEventStart || t == TurnEventEnd
	case TurnEventLogVerbose:
		return true
	default: // outline
		return t != TurnEventLLMDelta && t != TurnEventLLMDone
	}
}
