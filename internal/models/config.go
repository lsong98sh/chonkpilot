package models

import "time"

// Config represents a key-value configuration entry.
type Config struct {
	Key       string `json:"key"`
	Value     string `json:"value"` // JSON-encoded
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// TrustedDirConfig holds trusted/reference directory configuration.
type TrustedDirConfig struct {
	Path  string `json:"path"`
	Perms string `json:"perms"` // read / write / create
	IsRef bool   `json:"is_ref,omitempty"` // reference directory
}

// NewConfig creates a new Config entry.
func NewConfig(key, value string) *Config {
	now := time.Now().UTC().Format(time.RFC3339)
	return &Config{
		Key:       key,
		Value:     value,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// LLMProvider holds a single LLM provider configuration as shown in the UI.
type LLMProvider struct {
	Name            string  `json:"name"`
	Protocol        string  `json:"protocol"`
	APIKey          string  `json:"apiKey"`
	Model           string  `json:"model"`
	BaseURL         string  `json:"baseUrl"`
	Temperature     float64 `json:"temperature"`
	MaxTokens       int     `json:"maxTokens"`
	Thinking        bool    `json:"thinking"`                  // enable thinking mode (DeepSeek extra_body thinking.type)
	ReasoningEffort string  `json:"reasoningEffort,omitempty"` // low/medium/high/max (default high)
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
	CreatedAt   string      `json:"created_at,omitempty"`
	UpdatedAt   string      `json:"updated_at,omitempty"`
}

// UserConfig holds all user-level configuration stored at ~/.chonkpilot/config.json.
type UserConfig struct {
	LLMs              []LLMProvider      `json:"llms"`
	DefaultLLM        int                `json:"defaultLLM"`
	MCPServers        []MCPServerConfig  `json:"mcpServers,omitempty"`
	Theme             string             `json:"theme"`
	LastWorkDir       string             `json:"lastWorkDir"`
	ActiveSessionID   string             `json:"activeSessionID,omitempty"`
	ChromePath        string             `json:"chromePath,omitempty"`        // cached Chrome path from auto-discovery
	MaxToolIterations int                `json:"maxToolIterations,omitempty"` // 0=unlimited, default 800
	ResponseTimeout   int                `json:"responseTimeout,omitempty"`   // seconds: time-to-first-token timeout, default 180
	StreamTimeout     int                `json:"streamTimeout,omitempty"`     // seconds: idle timeout between stream chunks, default 180
	LogLevel          string             `json:"logLevel,omitempty"`          // debug/info/warn/error
	RetryCount        int                `json:"retryCount,omitempty"`        // LLM retry attempts
	RetryDelay        int                `json:"retryDelay,omitempty"`        // seconds between retries
	// Context compression
	KeepFullTurns            int `json:"keepFullTurns,omitempty"`            // turns to keep fully raw (default 5)
	KeepSimplifiedTurns      int `json:"keepSimplifiedTurns,omitempty"`      // turns to keep simplified (default 15)
	ForceCompressThreshold   int `json:"forceCompressThreshold,omitempty"`   // token threshold for forced compression (default 80000)
	// Background tasks
	ForeachConcurrency int `json:"foreachConcurrency,omitempty"` // parallel goroutines for foreach (1-10, default 5)
	ForeachMaxDepth    int `json:"foreachMaxDepth,omitempty"`    // max nested depth for foreach (1-10, default 5)
	// Timeouts
	FetchTimeout   int `json:"fetchTimeout,omitempty"`   // HTTP fetch timeout in seconds (default 300)
	MCPTimeout     int `json:"mcpTimeout,omitempty"`     // MCP HTTP request timeout in seconds (default 60)
	AskUserTimeout int `json:"askUserTimeout,omitempty"` // ask_user prompt timeout in seconds (default 300)

	// Runtime environments (auto-detected on IDE startup)
	JavaPath   string `json:"javaPath,omitempty"`
	PythonPath string `json:"pythonPath,omitempty"`
	NodePath   string `json:"nodePath,omitempty"`
}

// AgentConfig holds a single agent configuration as shown in the UI.
type AgentConfig struct {
	ID        int64  `json:"id,omitempty"`
	Title     string `json:"title"`
	UseCase   string `json:"useCase"`
	Prompt    string `json:"prompt"`
	LLMRef    string `json:"llmRef,omitempty"` // name of the LLM to use; empty = inherit from parent
	Source    string `json:"_source,omitempty"` // "system" | "llm" | "" (user-managed)
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// MCPServerConfig holds a single MCP server configuration.
type MCPServerConfig struct {
	Name            string               `json:"name"`
	URL             string               `json:"url"`
	Enabled         bool                 `json:"enabled"`
	Description     string               `json:"description,omitempty"`
	DiscoveredTools []MCPServerToolConfig `json:"discoveredTools,omitempty"`
}

// MCPServerToolConfig holds a tool definition discovered from an MCP server.
type MCPServerToolConfig struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema interface{} `json:"inputSchema,omitempty"`
}
