// LLM 调用契约的**共享类型**（LR-11 后本文件不再含任何出网实现）。
//
// 出网、协议分派（openai chat / responses / anthropic / 内置兜底 echo）与流式解析全部归
// `chonkpilot-router`（见 `routercall.go` 的 `Server.chat`）；本文件只保留 llm 侧与 router 之间
// 的**契约类型**：调用选项 `ChatOptions`、消费方事件 `StreamEvent`、错误分类 `ErrKind`/`LLMError`、
// 消息别名与协议常量。
package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
)

// 超时分级（对齐 llm-error-handling.md §一）：首字节 ResponseTimeout / chunk 间隔 StreamTimeout。
// 这两个常量是**回落默认**：usr 配置 responseTimeout/streamTimeout（T-27 接线）未配置/非正值
// 时生效，保证未配置时的行为与旧硬编码一致（router 侧同名默认 `adaptor.Default*Timeout`）。
const (
	ResponseTimeout = 120 * time.Second // 首字节超时（连接建立后第一个字节）
	StreamTimeout   = 60 * time.Second  // 流 chunk 间隔超时（两次 chunk 之间）
)

// ChatMsg / ToolCall 是 chat 消息类型，统一别名到 chonkpilot-data（快照契约一致）：
// data.ChatMsg 含 Kind 标记（user 消息 text=用户提问 / notify=工具完成通知），
// 压缩模块（chonkpilot-plugin-compress）据此在快照内定位保留段、排除工具通知。
type ChatMsg = data.ChatMsg
type ToolCall = data.ToolCall

// StreamEvent 是流式事件（llm 侧**消费契约**，形状不随出网实现变更）：`Content`/`Reasoning`
// 为增量，终态 = `Done`（附带 `FinishReason` 与拼装完成的 `ToolCalls`），失败 = `Err`。
type StreamEvent struct {
	Content      string
	Reasoning    string // 思考链增量（reasoning_content，若有）
	ToolCalls    []ToolCall
	Done         bool
	FinishReason string
	Model        string // 上游回显的模型名（chunk / 响应对象的 model 字段，多数实现每个 chunk 都带；探活用，缺失为空）
	Err          error  // *LLMError（错误分类）
}

// ErrKind 是 LLM 错误分类（S6：retryable vs 不可重试）。取值与 router `ErrorKind` 同名同义
// （router 分类 → 本分类的折算见 `routercall.go` 的 `routerKindToLLM`）。
type ErrKind string

const (
	ErrNetwork   ErrKind = "network"    // 连接失败/重置/EOF（retryable）
	ErrTimeout   ErrKind = "timeout"    // 首字节/流间隔超时（retryable）
	ErrRateLimit ErrKind = "rate_limit" // 429（retryable）
	ErrServer    ErrKind = "server"     // 5xx（retryable）
	ErrAuth      ErrKind = "auth"       // 401/403（不可重试）
	ErrProtocol  ErrKind = "protocol"   // 其他（不可重试）
)

// LLMError 是带分类的 LLM 错误。
type LLMError struct {
	Kind      ErrKind
	Message   string
	Retryable bool
}

func (e *LLMError) Error() string { return fmt.Sprintf("[%s] %s", e.Kind, e.Message) }

func llmErr(kind ErrKind, msg string) *LLMError {
	return &LLMError{Kind: kind, Message: msg, Retryable: kind == ErrNetwork || kind == ErrTimeout || kind == ErrRateLimit || kind == ErrServer}
}

// provider.protocol 已知取值（P1-2）：空/未知 → openai（loadLLMProvider 对未知值 warn）。
const (
	ProtocolOpenAI    = "openai"    // OpenAI 兼容 Chat Completions（默认，/chat/completions）
	ProtocolResponses = "responses" // OpenAI Responses API（/responses；DeepSeek 已支持）
	// ProtocolEcho 是内置 echo provider 的协议（P1-3）：**不发任何 HTTP**，直接回显本轮
	// 最后一条真实用户消息（D-30 后仅作 router **内置兜底**，见 routercall.go）。
	ProtocolEcho = "echo"
)

// NormalizeLLMProtocol 归一协议取值：responses → responses；echo → echo；
// 其余（空/openai/未知）→ openai。
func NormalizeLLMProtocol(p string) string {
	switch strings.TrimSpace(p) {
	case ProtocolResponses:
		return ProtocolResponses
	case ProtocolEcho:
		return ProtocolEcho
	}
	return ProtocolOpenAI
}

// ChatOptions 是「一次来回（`step`）」调用的可选参数（→ router `CallOptions`，见 routercall.go）。
type ChatOptions struct {
	Model       string   // 模型名（空 = 使用 provider spec 的 DefaultModel）
	Think       string   // 思考模式（high/medium/low，映射到 reasoning_effort）
	Effort      string   // 思考力度（high/medium/low，映射到 reasoning_effort；优先于 Think）
	Temperature *float64 // 采样温度（nil = 不发送，使用 API 默认）
	MaxTokens   *int     // 最大生成长度（nil = 不发送）
	TopP        *float64 // 核采样（nil = 不发送；Responses 协议按文档支持 top_p）

	// 超时（可选，<=0 = 用 router 侧默认 ResponseTimeout/StreamTimeout）：来自 usr 配置
	// responseTimeout/streamTimeout（T-27 接线，读取见 server.loadLLMRuntimeConfig）。
	ResponseTimeout time.Duration
	StreamTimeout   time.Duration

	// Images 图片（多模态）输入选项（P2-8）：UploadDir 非空时把用户消息里的
	// `![名](路径)` 引用展开为图片内容块；空 → 纯文本（历史行为）。见 llm_images.go。
	Images ImageOptions
}
