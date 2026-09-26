package models

// ToolCallItem represents a single tool call in a run_tasks/foreach item.
type ToolCallItem struct {
	Tool string                 `json:"tool"`
	Args map[string]interface{} `json:"args,omitempty"`
}
