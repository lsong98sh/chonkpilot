// Anthropic 流式载荷解析（LR-4）：SSE `data:` 载荷的 `type` 字段即事件类型。
//
// 说明：Anthropic 的 SSE 同时带 `event:` 行与 `data:` 载荷，且**载荷内的 `type` 与 `event:` 一致**
// —— 共享的 `adaptor.SSEDecoder` 只取 `data:` 载荷，故按载荷内 `type` 分派（少一份状态、抗事件名
// 大小写差异）。
package anthropic

// 流式事件的 `type` 取值（Anthropic 官方口径）。
const (
	eventMessageStart      = "message_start"
	eventContentBlockStart = "content_block_start"
	eventContentBlockDelta = "content_block_delta"
	eventContentBlockStop  = "content_block_stop"
	eventMessageDelta      = "message_delta"
	eventMessageStop       = "message_stop"
	eventPing              = "ping"
	eventError             = "error"
)

// `content_block_delta.delta.type` 取值。
const (
	deltaText      = "text_delta"       // 文本增量 → text_delta
	deltaThinking  = "thinking_delta"   // 思考链增量 → reasoning_delta
	deltaInputJSON = "input_json_delta" // 工具参数增量（片段）→ 按 index 聚合
	deltaSignature = "signature_delta"  // 思考签名增量 → 累加到 `done` 事件的 Signature（D-31）
)

// 内容块类型（`content_block_start.content_block.type`）。
const (
	contentBlockToolUse  = "tool_use"
	contentBlockThinking = "thinking"
)

// streamEvent 是一条流式载荷（各 type 取用各自字段）。
type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"` // content_block_*：内容块序号（= 工具调用的 index）
	Model   string `json:"model"` // 部分实现回显模型名
	Message *struct {
		Model string       `json:"model"`
		Usage *usageFields `json:"usage"`
	} `json:"message"` // message_start
	ContentBlock *struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Name      string `json:"name"`
		Signature string `json:"signature"` // thinking 块起始可能已带签名（部分实现）
	} `json:"content_block"` // content_block_start
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"` // signature_delta：思考块签名（D-31）
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"` // content_block_delta / message_delta
	Usage *usageFields `json:"usage"` // message_delta（输出侧）
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"` // error
}

// usageFields 是 Anthropic 的 usage 载荷（输入侧见 message_start，输出侧见 message_delta）。
type usageFields struct {
	InputTokens          int `json:"input_tokens"`
	OutputTokens         int `json:"output_tokens"`
	CacheReadInputTokens int `json:"cache_read_input_tokens"`
}
