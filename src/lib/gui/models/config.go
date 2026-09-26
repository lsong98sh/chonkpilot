package models

// LLMProvider holds a single LLM provider configuration as shown in the UI.
type LLMProvider struct {
	Name string `json:"name"`
	// Protocol 协议类型；openai = OpenAI 兼容 Chat Completions（默认）；responses = OpenAI
	// Responses API（/responses）；空/缺省视为 openai。
	Protocol    string  `json:"protocol,omitempty"`
	APIKey      string  `json:"apiKey"`
	Model       string  `json:"model"`
	BaseURL     string  `json:"baseUrl"`
	Temperature float64 `json:"temperature"`
	// MaxOutputToken 最大输出 token（请求体 `max_tokens`）；旧名 `maxTokens` 由读侧兼容（口径 Z1，2026-09-25）。
	MaxOutputToken int `json:"maxOutputToken"`
	// MaxContextToken 上下文窗口大小（兜底归并的窗口来源）；0 = 不启用兜底（口径 Z1/Z3，2026-09-25）。
	MaxContextToken int    `json:"maxContextToken"`
	Thinking        bool   `json:"thinking"`                  // enable thinking mode (DeepSeek extra_body thinking.type)
	ReasoningEffort string `json:"reasoningEffort,omitempty"` // low/medium/high/max (default high)
	// MaxToolIterations 单轮工具循环上限（随 provider 定义，系统/用户两级）；0/缺省 = 20。
	MaxToolIterations int `json:"maxToolIterations,omitempty"`
}

// ToolConfig holds a single tool configuration as shown in the UI.
type ToolConfig struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Command     string      `json:"command"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
	Source      string      `json:"_source,omitempty"` // "user" | "llm"
	McpID       string      `json:"mcpId,omitempty"`   // links to global MCP server name (auto-discovered)
	CreatedAt   string      `json:"createdAt,omitempty"`
	UpdatedAt   string      `json:"updatedAt,omitempty"`
}

// AgentConfig removed (D1): project_agents table is deprecated. Scenario
// sub-agents now use models.ScenarioAgent (see scenario.go).
