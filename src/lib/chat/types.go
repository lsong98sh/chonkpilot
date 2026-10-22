// 组件自持类型（MOD-1 §3.2）：chat 内核只认这些类型，宿主 adapter 负责与
// data.ChatMsg / gateway.CallReq / facade.InboxItem 互转。**不 import 任何外部包类型**。
package chat

import (
	"encoding/json"
	"time"
)

// Message 是一条对话消息（对齐 OpenAI 协议；持久化 / 上游请求共用）。
// 对应 data.ChatMsg（adapter 转换）。
type Message struct {
	ID         string         `json:"id,omitempty"`
	SessionID  string         `json:"session_id,omitempty"`
	TurnID     string         `json:"turn_id,omitempty"`
	Role       string         `json:"role"` // user / assistant / tool / system
	Kind       string         `json:"kind,omitempty"`
	Content    string         `json:"content"`
	Reasoning  string         `json:"reasoning,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// ToolCall 是 LLM 请求的工具调用（chat 自有形态；Arguments 为 JSON 文本）。
// Context 是本次调用的归属（调用方填充，宿主 adapter 转为 gateway.Context）。
type ToolCall struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Arguments string      `json:"arguments"` // JSON 文本（对齐 OpenAI function.arguments）
	Context   CallContext `json:"context,omitempty"`
}

// InboxItem 是一条待处理发送（中途插话 / 异步工具回执 / 等下一轮）。
// 对应 facade.InboxItem（adapter 转换）。
type InboxItem struct {
	ID             string          `json:"id"`
	SessionID      string          `json:"session_id,omitempty"`
	TurnID         string          `json:"turn_id,omitempty"`
	Type           string          `json:"type"` // insert / tool_result / next_turn
	LLM            string          `json:"llm,omitempty"`
	Content        string          `json:"content"`
	Think          json.RawMessage `json:"think,omitempty"`
	Effort         json.RawMessage `json:"effort,omitempty"`
	ScenarioID     string          `json:"scenario_id,omitempty"`
	Status         string          `json:"status"` // pending / consumed / discarded
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	CreatedAt      string          `json:"created_at,omitempty"`
	// Result 非 nil 且 Type=="tool_result" 时 = 该工具调用的回执（AskID/OpID 由宿主映射到 CallID）。
	Result *ToolResult `json:"result,omitempty"`
}

// InboxTypeInsert 是 turn 中途插话（M6f-1）。
const InboxTypeInsert = "insert"

// InboxTypeToolResult 是异步工具回执（M6f-2）：`Result` 非 nil 时按同步结果路径续轮。
const InboxTypeToolResult = "tool_result"

// InsertResumeNote 是插入帧出栈后给外层帧的一次性续接说明：只随下一次请求临时携带
// （插在最前面的 system 消息），不落库、不进 messages / 快照 / 压缩。
// 文案与生产 src/lib/llm/server/turn.go 的 insertResumeNote 完全一致。
const InsertResumeNote = "【插话续接】在你等待工具结果期间，用户插入了一段对话（见上方的 user/assistant 消息），已按用户要求处理完毕。以下是该工具的真实结果，请继续完成原任务；不要重复调用该工具，也不要重复回答已经回答过的插话内容。"

// CallContext 是一次工具调用的归属上下文（对应 gateway.Context，adapter 转换）。
type CallContext struct {
	InstanceID string `json:"instance_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	TurnID     string `json:"turn_id,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
	WorkDir    string `json:"work_dir,omitempty"`
	DataDir    string `json:"data_dir,omitempty"`
	TopSession string `json:"top_session,omitempty"`
	Parent     string `json:"parent,omitempty"`
}

// ToolResult 是一次工具调用的结果（chat 自有形态；跨进程 JSON 亦为此形状）。
type ToolResult struct {
	CallID   string         `json:"tool_call_id"`
	Content  string         `json:"content"`
	IsError  bool           `json:"is_error,omitempty"`
	Meta     map[string]any `json:"_meta,omitempty"`
	Duration time.Duration  `json:"-"`
}

// ToolProgress 是工具执行期间的阶段变化（经 EventSink 输出，不在 Dispatch 返回值承载）。
type ToolProgress struct {
	CallID  string    `json:"tool_call_id"`
	OpID    string    `json:"op_id,omitempty"`
	Phase   string    `json:"phase"` // preparing / running / awaiting / detached / done / error
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
}

// ToolSpec 是目录中的一个工具（对应 gateway tools/list 单项，adapter 转换）。
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	Server      string          `json:"server,omitempty"`
	Meta        map[string]any  `json:"_meta,omitempty"`
}

// ServerInfo 是目录中的一个 MCP 服务（对应 gateway 注册表视图，adapter 转换）。
type ServerInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Transport string `json:"transport,omitempty"`
	Status    string `json:"status,omitempty"` // connected / failed / starting / stopped / unregistered
	Category  string `json:"category,omitempty"`
	Scope     string `json:"scope,omitempty"`
	ToolCount int    `json:"tool_count,omitempty"`
}

// StepEventType 是一次 LLM step 的流式事件类型（chat 自持；宿主 adapter 由 router.Event 转换）。
type StepEventType string

const (
	StepEventDelta     StepEventType = "delta"     // 文本增量
	StepEventReasoning StepEventType = "reasoning" // 推理增量（不直接上屏，随 Assistant 落库）
	StepEventToolCall  StepEventType = "tool_call" // 单个工具调用（完整片段）
	StepEventDone      StepEventType = "done"      // step 结束（Finish / Usage / 聚合 ToolCalls）
)

// StepEvent 是 LLMClient.Step 的流式事件（Err 仅 Done 时可能非空）。
type StepEvent struct {
	Type      StepEventType `json:"type"`
	Text      string        `json:"text,omitempty"`
	ToolCall  *ToolCall     `json:"tool_call,omitempty"`
	ToolCalls []ToolCall    `json:"tool_calls,omitempty"`
	Usage     Usage         `json:"usage,omitempty"`
	Finish    string        `json:"finish,omitempty"`
	Err       error         `json:"-"`
}

// EventType 是 EventSink 的事件类型（统一承载，由 Type 区分）。
type EventType string

const (
	EventLLMDelta           EventType = "llm_delta"
	EventLLMReceive         EventType = "llm_receive"
	EventLLMRetry           EventType = "llm_retry"
	EventTurnComplete       EventType = "turn_complete"
	EventToolResult         EventType = "tool_result"
	EventToolProgress       EventType = "tool_progress"
	EventAskUser            EventType = "ask_user"
	EventSessionInterrupted EventType = "session_interrupted"
)

// RetryInfo 是可见重试（llm_retry）的载荷。
type RetryInfo struct {
	Attempt   int           `json:"retry_attempt"`
	Count     int           `json:"retry_count"`
	Wait      time.Duration `json:"-"`
	WaitMilli int64         `json:"retry_wait_ms"`
}

// Event 是对外事件（只广播、不落库；落库走 TurnStore）。
// 字段按类型使用：ToolResult（tool_result）/ Progress（tool_progress）/ Retry（llm_retry）/
// Text（llm_delta / llm_receive / 错误文案）/ Status（终态 / 会话状态）/
// Code + Retryable + DelayMilli（session_interrupted，对齐宿主 `session-interrupted` 载荷）。
type Event struct {
	Type       EventType     `json:"type"`
	InstanceID string        `json:"instance_id,omitempty"`
	SessionID  string        `json:"session_id,omitempty"`
	TurnID     string        `json:"turn_id,omitempty"`
	At         time.Time     `json:"at"`
	Text       string        `json:"text,omitempty"`
	Status     string        `json:"status,omitempty"`
	Retry      *RetryInfo    `json:"retry,omitempty"`
	Progress   *ToolProgress `json:"progress,omitempty"`
	ToolResult *ToolResult   `json:"tool_result,omitempty"`
	// Code 是中断原因码（session_interrupted）：length（输出截断）/ stream（已连接后断流）。
	Code string `json:"code,omitempty"`
	// Retryable 表示中断是否可续写（session_interrupted；语义级续写恒可重试）。
	Retryable bool `json:"retryable,omitempty"`
	// DelayMilli 是自动续写宽限期毫秒数（session_interrupted；未启用续写 → 0）。
	DelayMilli int64 `json:"auto_continue_delay_ms,omitempty"`
}

// TurnEvent 是 TurnState 事件日志的一条（重放游标 Seq 单调；Payload 为 JSON 原文）。
type TurnEvent struct {
	Seq     uint64          `json:"seq"`
	Type    TurnEventType   `json:"type"`
	At      time.Time       `json:"at"`
	Payload json.RawMessage `json:"payload,omitempty"`
}
