// canonical 类型的**别名再导出**：定义处 = `internal/canon`（见该包文件头）。
//
// 用别名（`type X = canon.X`）而非新类型：`router.Message` / `router.Event` 等**对外签名与用法
// 逐字不变**（llm 侧 `routerconv.go` 零改），同时让 `internal/adaptor/**` 能引用 canonical
// 而不反向 import 根包 —— 消除 `router → internal/adaptor → router` 的 import 环。
package router

import "github.com/chonkpilot/chonkpilot-router/internal/canon"

type (
	// Role / Message / Part / ImagePart：canonical 消息模型。
	Role      = canon.Role
	Message   = canon.Message
	Part      = canon.Part
	ImagePart = canon.ImagePart
	// ToolCall / ToolDef：工具调用（拼装态）与工具定义。
	ToolCall = canon.ToolCall
	ToolDef  = canon.ToolDef
	// Spec：一个已注册 provider。
	Spec = canon.Spec
	// Reasoning / CallOptions / Request：「一次」调用的可选参数与请求。
	Reasoning   = canon.Reasoning
	CallOptions = canon.CallOptions
	Request     = canon.Request
	// Phase / RetryContext / RetryPolicy / RetryObserver / DefaultPolicy：报文级重试（RETRY-2）。
	Phase         = canon.Phase
	RetryContext  = canon.RetryContext
	RetryPolicy   = canon.RetryPolicy
	RetryObserver = canon.RetryObserver
	DefaultPolicy = canon.DefaultPolicy
	// EventType / ToolCallDelta / Event：流式事件。
	EventType     = canon.EventType
	ToolCallDelta = canon.ToolCallDelta
	Event         = canon.Event
)

// 角色常量（Role 取值）。
const (
	RoleSystem    = canon.RoleSystem
	RoleUser      = canon.RoleUser
	RoleAssistant = canon.RoleAssistant
	RoleTool      = canon.RoleTool
)

// 内容块类型（Part.Type 取值）。
const (
	PartText  = canon.PartText
	PartImage = canon.PartImage
)

// 协议名（Spec.Protocol 取值）。
const (
	ProtocolOpenAI    = canon.ProtocolOpenAI
	ProtocolResponses = canon.ProtocolResponses
	ProtocolAnthropic = canon.ProtocolAnthropic
	ProtocolEcho      = canon.ProtocolEcho
)

// 失败阶段（Error.Phase 取值；RETRY-2 仅内部用于重试分流）。
const (
	PhaseDial      = canon.PhaseDial
	PhaseFirstByte = canon.PhaseFirstByte
	PhaseStream    = canon.PhaseStream
)

// 事件类型（EventType 取值；LR-5 七类，terminal = done / error）。
// 语义收口（D-32）：`*_delta` 只表示增量；`tool_call` 表示完整的一次工具调用。
const (
	EvTextDelta      = canon.EvTextDelta
	EvReasoningDelta = canon.EvReasoningDelta
	EvToolCallDelta  = canon.EvToolCallDelta
	EvToolCall       = canon.EvToolCall
	EvUsage          = canon.EvUsage
	EvDone           = canon.EvDone
	EvError          = canon.EvError
)
