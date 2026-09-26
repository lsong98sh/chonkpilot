// OpenAI 兼容流式载荷解析（LR-3）：单条 `data:` 载荷 → chunk 结构（含非流式单发形态）。
package openai

import (
	"github.com/chonkpilot/chonkpilot-router/internal/adaptor"
	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// chatChunk 是一条流式载荷（也兼容「忽略 stream=true 的单发响应」：choices[].message）。
type chatChunk struct {
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
	Usage   *chunkUsage   `json:"usage"`
	Error   *chunkError   `json:"error"`
}

// chunkChoice 是单条选择项：流式用 `delta`；兼容端点的单发响应用 `message`。
type chunkChoice struct {
	Delta        chunkDelta  `json:"delta"`
	Message      *chunkDelta `json:"message"`
	FinishReason *string     `json:"finish_reason"`
}

// chunkDelta 是增量（或单发 message）的内容载体。
type chunkDelta struct {
	Content          string          `json:"content"`
	ReasoningContent string          `json:"reasoning_content"` // DeepSeek / Qwen 等兼容扩展
	Reasoning        string          `json:"reasoning"`         // 少数网关的别名
	Thinking         string          `json:"thinking"`          // 少数代理的别名
	ToolCalls        []chunkToolCall `json:"tool_calls"`
}

// chunkToolCall 是增量工具调用：`index` 为流式期序号（非流式形态无该字段 → 单条按 index 0）。
type chunkToolCall struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// chunkError 是流内错误对象（部分兼容端点把错误放进 SSE 载荷）。
type chunkError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// chunkUsage 是 usage 载荷（`stream_options.include_usage` 时出现在末片）。
type chunkUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// body 返回该选择项实际的内容载体：流式 → `delta`；兼容端点单发 → `message`。
func (c chunkChoice) body() chunkDelta {
	if c.Message != nil {
		return *c.Message
	}
	return c.Delta
}

// reasoning 取思考链增量：兼容端点各写各的字段名 → 依次尝试 `reasoning_content`（DeepSeek/Qwen）、
// `reasoning`（部分网关）、`thinking`（部分代理）；都空 → 空串（不当正文）。
func (d chunkDelta) reasoning() string {
	switch {
	case d.ReasoningContent != "":
		return d.ReasoningContent
	case d.Reasoning != "":
		return d.Reasoning
	default:
		return d.Thinking
	}
}

// canon 归一 usage（nil = 本片无 usage）；字段映射与缺失语义见 `adaptor.NormalizeUsage`（LR-9）。
func (u *chunkUsage) canon() *canon.Usage {
	if u == nil {
		return nil
	}
	out := adaptor.NormalizeUsage(u.PromptTokens, u.CompletionTokens, u.TotalTokens,
		u.CompletionTokensDetails.ReasoningTokens, u.PromptTokensDetails.CachedTokens)
	return &out
}
