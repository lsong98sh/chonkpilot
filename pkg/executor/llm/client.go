package llm

import (
	"time"

	"go.uber.org/zap"
)

// Message represents a chat message.
type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"` // deepseek thinking chain
	ToolCallID       string     `json:"tool_call_id,omitempty"`      // for role="tool"
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`        // for role="assistant" with tool calls
}

// ChatOptions holds options for chat completion.
type ChatOptions struct {
	Stream            bool
	Model             string
	Tools             []ToolDefinition // tool definitions to pass to LLM
	Temperature       float64          // 0~2, works in non-thinking mode
	MaxTokens         int              // max output tokens
	Thinking          bool             // enable thinking mode (extra_body thinking.type)
	ReasoningEffort   string           // low/medium/high/max (DeepSeek reasoning_effort)
	ResponseTimeout time.Duration    // max time to wait for first response token (0 = no timeout)
	StreamTimeout     time.Duration    // max idle time between stream chunks (0 = no timeout)
}

// ToolDefinition is a tool definition for LLM function calling requests.
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  interface{} // JSON schema
}

// ToolCall represents a tool call from LLM.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON string
	} `json:"function"`
}

// StreamEvent represents a streaming response chunk.
type StreamEvent struct {
	Content          string
	ReasoningContent string // deepseek thinking chain
	Index            int
	Done             bool
	Error            error
	ToolCall         *ToolCall // non-nil when LLM requests a tool call
}

// Provider is the interface for LLM providers.
type Provider interface {
	Chat(messages []Message, options ChatOptions) (<-chan StreamEvent, error)
}

// Client manages LLM provider interactions.
type Client struct {
	provider Provider
	logger   *zap.Logger
}

// NewClient creates a new LLM client.
func NewClient(protocol, model, apiKey, apiURL string, logger *zap.Logger) *Client {
	var provider Provider

	switch protocol {
	case "openai":
		provider = &OpenAIProvider{
			Model:   model,
			APIKey:  apiKey,
			BaseURL: apiURL,
		}
	case "claude":
		provider = &ClaudeProvider{
			Model:   model,
			APIKey:  apiKey,
			BaseURL: apiURL,
		}
	default:
		// Default to OpenAI-compatible
		if apiURL == "" {
			apiURL = "https://api.openai.com/v1"
		}
		provider = &OpenAIProvider{
			Model:   model,
			APIKey:  apiKey,
			BaseURL: apiURL,
		}
	}

	return &Client{
		provider: provider,
		logger:   logger,
	}
}

// Chat sends a chat completion request.
func (c *Client) Chat(messages []Message, options ChatOptions) (<-chan StreamEvent, error) {
	return c.provider.Chat(messages, options)
}
