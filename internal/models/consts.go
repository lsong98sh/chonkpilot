package models

// Role constants for Message.Role
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Type constants for Message.Type
const (
	TypeText       = "text"
	TypeReasoning  = "reasoning"
	TypeToolCall   = "tool_call"
	TypeToolResult = "tool_result"
)

// Rule category constants for Rule.Category
const (
	RuleCategoryProject  = "project"
	RuleCategoryLanguage = "language"
	RuleCategoryScenario = "scenario"
)

// Transport constants for MCPServerConfig.Transport
const (
	TransportDirect = "direct"
	TransportSSE    = "sse"
)

// Source constants for ToolConfig.Source and AgentConfig.Source
const (
	SourceUser   = "user"
	SourceLLM    = "llm"
	SourceSystem = "system"
)

// Prompt key constants
const (
	PromptKeySystemPrompt  = "system_prompt"
	PromptKeyToolUsage     = "tool_usage_prompt"
	PromptKeySummaryPrompt = "summary_prompt"
)
