package models

// ToolDefinition defines a tool that can be called by the LLM.
type ToolDefinition struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Parameters  ToolParameterSchema `json:"parameters"`
	IsBuiltin   bool                `json:"is_builtin"`
	Command     string              `json:"command,omitempty"` // Shell command for custom tools
}

// ToolParameterSchema defines the parameter schema for a tool.
type ToolParameterSchema struct {
	Type       string                   `json:"type"`
	Required   []string                 `json:"required,omitempty"`
	Properties map[string]ToolParameter `json:"properties,omitempty"`
}

// ToolParameter defines a single parameter.
type ToolParameter struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// RunTasksParams holds parameters for the run_tasks tool.
type RunTasksParams struct {
	Async bool            `json:"async"`
	Items []ToolCallItem  `json:"items"`
}

// ToolCallItem represents a single tool call in a run_tasks item.
type ToolCallItem struct {
	Tool string                 `json:"tool"`
	Args map[string]interface{} `json:"args,omitempty"`
}

// Rule represents a project rule/specification stored in the rules table.
type Rule struct {
	Name      string `json:"name"`
	Category  string `json:"category"` // project / language / scenario
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Scenario represents a scenario template.
type Scenario struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}
