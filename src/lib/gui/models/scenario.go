package models

// ScenarioAgent holds a sub-agent definition embedded in a scenario (agents_json).
// It has no persistent id — the frontend uses temporary row keys, and the whole
// agents array is replaced atomically on every scenario save.
type ScenarioAgent struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	RoleTag      string   `json:"roleTag,omitempty"`
	Prompt       string   `json:"prompt,omitempty"`
	Tools        []string `json:"tools,omitempty"`        // whitelist: empty/nil = all tools
	LLMRef       string   `json:"llmRef,omitempty"`       // LLM name override; empty = inherit
	DelegateCond string   `json:"delegateCond,omitempty"` // when to delegate (LLM-facing hint)
	IsMain       bool     `json:"isMain"`
}

// DefaultScenarioKey 是默认场景的固定标识 key。固定 key 的场景（新建/首次运行
// 自动 seed 的默认场景）在编辑界面显示"还原默认"按钮：删除其当前数据并还原为
// embed 的默认值。其余场景 key 为空。
const DefaultScenarioKey = "default"

// ScenarioConfig holds a scenario definition stored in scenario.db.
type ScenarioConfig struct {
	ID           int64           `json:"id"`
	Key          string          `json:"key,omitempty"` // 固定标识：默认场景为 DefaultScenarioKey，其余为空
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	SystemPrompt string          `json:"systemPrompt"`
	Agents       []ScenarioAgent `json:"agents,omitempty"`
	CreatedAt    string          `json:"createdAt,omitempty"`
	UpdatedAt    string          `json:"updatedAt,omitempty"`
}
