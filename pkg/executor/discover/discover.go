package discover

import (
	"embed"
	"encoding/json"
	"path/filepath"
	"strings"
)

//go:embed tools/*/*.json
var toolsFS embed.FS

// ToolDefinition represents a tool definition for LLM function calling.
type ToolDefinition struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  ToolParameters    `json:"parameters"`
	Category    string            `json:"-"` // set dynamically from directory path: core/web/desktop
}

// ToolParameters represents the JSON Schema parameters of a tool.
type ToolParameters struct {
	Type       string                  `json:"type"`
	Properties map[string]ToolProperty `json:"properties,omitempty"`
	Required   []string                `json:"required,omitempty"`
}

// ToolProperty represents a single property in the tool parameters.
type ToolProperty struct {
	Type        string                  `json:"type"`
	Description string                  `json:"description"`
	Enum        []string                `json:"enum,omitempty"`
	Items       *ToolProperty           `json:"items,omitempty"`
	Properties  map[string]ToolProperty `json:"properties,omitempty"`
	Required    []string                `json:"required,omitempty"`
}

// Discoverer discovers and lists available tools.
type Discoverer struct {
	builtinTools []ToolDefinition
}

// NewDiscoverer creates a new Discoverer with builtin tools.
func NewDiscoverer() *Discoverer {
	d := &Discoverer{}
	d.builtinTools = generateBuiltinTools()
	return d
}

// ListBuiltinTools returns the list of builtin tool definitions.
func (d *Discoverer) ListBuiltinTools() []ToolDefinition {
	result := make([]ToolDefinition, len(d.builtinTools))
	copy(result, d.builtinTools)
	return result
}

// ListAllTools returns all available tools (builtin + user-defined).
func (d *Discoverer) ListAllTools() []ToolDefinition {
	return d.ListBuiltinTools()
}

func generateBuiltinTools() []ToolDefinition {
	entries, err := toolsFS.ReadDir("tools")
	if err != nil {
		panic("failed to read embedded tools directory: " + err.Error())
	}

	var tools []ToolDefinition
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		category := entry.Name()
		files, err := toolsFS.ReadDir("tools/" + category)
		if err != nil {
			panic("failed to read tools/" + category + ": " + err.Error())
		}
		for _, file := range files {
			if file.IsDir() {
				continue
			}
			if !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			data, err := toolsFS.ReadFile("tools/" + category + "/" + file.Name())
			if err != nil {
				panic("failed to read tools/" + category + "/" + file.Name() + ": " + err.Error())
			}
			var t ToolDefinition
			if err := json.Unmarshal(data, &t); err != nil {
				panic("failed to parse tools/" + category + "/" + file.Name() + ": " + err.Error())
			}
			// Infer category from directory name
			t.Category = category

			// Sanity check: filename stem must match tool name
			stem := strings.TrimSuffix(file.Name(), ".json")
			if stem != t.Name {
				panic(filepath.Join("tools", category, file.Name()) +
					": filename stem '" + stem + "' does not match tool name '" + t.Name + "'")
			}

			tools = append(tools, t)
		}
	}
	return tools
}
