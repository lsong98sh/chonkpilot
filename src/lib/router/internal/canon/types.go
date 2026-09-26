// canonical 类型（跨协议统一请求 / 响应 / 事件）= LR-1 冻结的全局契约。
//
// 与 docs/spec/40-roadmap/40-演进计划.md §LR §4 接口草案逐字对齐（字段含义见各处注释）；
// 本文件只放类型，不承载协议语义：厂商字段差异由各协议适配器（LR-3 / LR-4）吸收。
//
// 落位：定义处 = `internal/canon`（本包）；**根包 `router` 以 `type X = canon.X` 别名再导出**
// —— 对外签名（`router.Message` 等）逐字不变，而 `internal/adaptor/**` 得以引用 canonical
// 却**不反向 import 根包**（消除 `router → internal/adaptor → router` 的 import 环，见
// `src/lib/router/doc.go`）。
package canon

import (
	"encoding/json"
	"time"
)

// Role 是消息角色（三协议并集，适配器按目标协议映射）。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// 内容块类型（Part.Type）。
const (
	PartText  = "text"
	PartImage = "image"
)

// Message 是 canonical 消息。
type Message struct {
	Role       Role
	Kind       string     // 透传语义（"text"/"notify"/"continue"…）；Router 不解释，仅随协议映射
	Content    []Part     // 统一内容块；纯文本 = 单个 TextPart
	ToolCalls  []ToolCall // role=assistant 发起
	ToolCallID string     // role=tool 时配对
	Reasoning  string     // 思考链（回传；字段名差异由适配器吸收）
	// ReasoningSignature 是 Reasoning 所属思考块的**签名**（Anthropic 扩展思考必需；其余协议留空）。
	// 落位理由（D-31）：canonical 把 thinking 折叠为 Message.Reasoning（**不是**一个 Part：思考链
	// 与正文同属一条 assistant 消息、无图片那样的块级载荷），签名与之严格配对 → 同落 Message。
	// Anthropic 要求回传的 thinking 块带签名（缺签名 → 上游 400），故签名必须随 Reasoning 保真往返。
	ReasoningSignature string
}

// Part 是统一内容块（text / image）。
type Part struct {
	Type  string // PartText | PartImage
	Text  string
	Image *ImagePart
}

// ImagePart 是图片块。
type ImagePart struct {
	MIME    string // 图片 MIME（如 image/png）
	DataURL string // "data:<mime>;base64,…"
}

// ToolCall 是一次工具调用（Index = 流式期序号；拼装完成后 Arguments 为完整 JSON 串）。
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // 原始 JSON 串（拼装完成后）
	Index     int    // 流式期 index（拼装态）
}

// ToolDef 是提交给 LLM 的工具定义（由 llm 侧从 gateway 工具清单构建）。
type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Spec 是一个已注册 provider（由 usr `llms` 记录装载；echo 为内置）。
type Spec struct {
	Name         string // = llms.name（选择键）
	Protocol     string // ProtocolOpenAI | ProtocolResponses | ProtocolAnthropic | ProtocolEcho
	BaseURL      string
	APIKey       string // 敏感：Router 不落任何日志（对齐 llm 侧掩码口径）
	DefaultModel string // Options.Model 为空时回落到此
}

// 协议名（Spec.Protocol 取值；Router 按此分派适配器）。
const (
	ProtocolOpenAI    = "openai"    // OpenAI 兼容 Chat Completions
	ProtocolResponses = "responses" // OpenAI Responses API
	ProtocolAnthropic = "anthropic" // Anthropic /v1/messages
	ProtocolEcho      = "echo"      // 内置 echo（不发任何 HTTP）
)

// Reasoning 是思考强度（nil = 不下发）。
type Reasoning struct {
	Effort string // "high" | "medium" | "low"
}

// CallOptions 是「一次」调用的可选参数（指针字段 nil = 不下发，保留 API 默认）。
type CallOptions struct {
	Model           string
	Temperature     *float64
	MaxTokens       *int
	TopP            *float64
	Reasoning       *Reasoning
	ResponseTimeout time.Duration // 首字节超时（<=0 = 适配器默认）
	StreamTimeout   time.Duration // 流 chunk 间隔超时（<=0 = 适配器默认）
}

// Request 是「一次」调用请求（无会话 / 轮次字段，见包注释边界）。
type Request struct {
	Provider string // Spec.Name；**空串 = 无可用 provider → 回落内置兜底 echo**（D-30）；其余未注册 → Error{Kind: invalid}
	Messages []Message
	Tools    []ToolDef
	Options  CallOptions
}

// EventType 是统一流式事件类型（LR-5：七类，terminal = done / error）。
//
// 语义收口（D-32）：**带 `_delta` 后缀的类型一律只表示增量**（`text_delta` / `reasoning_delta` /
// `tool_call_delta`）；**`tool_call` 表示完整的一次工具调用**（载荷 = 拼装完成的 `ToolCall`）。
type EventType string

const (
	EvTextDelta      EventType = "text_delta"
	EvReasoningDelta EventType = "reasoning_delta"
	EvToolCallDelta  EventType = "tool_call_delta" // 增量（片段）——本批适配器不发片段，仅保留类型语义
	EvToolCall       EventType = "tool_call"       // 完整值（拼装完成的一次工具调用，D-32）
	EvUsage          EventType = "usage"
	EvDone           EventType = "done"
	EvError          EventType = "error"
)

// ToolCallDelta 是 tool_call 增量（按 Index 聚合，见 LR-6）。
type ToolCallDelta struct {
	Index          int
	ID             string // 仅在首个非空分片出现
	Name           string
	ArgumentsDelta string
}

// Event 是一个流式事件（字段按 Type 取用）。
type Event struct {
	Type         EventType
	Text         string         // text_delta / reasoning_delta
	ToolCall     *ToolCallDelta // tool_call_delta（增量）
	ToolCallFull *ToolCall      // tool_call（完整值；D-32）
	Usage        *Usage         // usage
	FinishReason string         // done："stop"|"length"|"tool_calls"|…
	Model        string
	Signature    string // done：本轮思考块签名（Anthropic thinking signature；其余协议留空）
	Err          *Error // error
}

// Usage 是一次调用的 token 统计（**0 = 上游未提供**；字段映射与缺失语义见 `internal/adaptor/usage.go`，LR-9）。
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	ReasoningTokens  int // 0 = 上游未提供
	CachedTokens     int // 0 = 上游未提供
}
