package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleAddAgent creates or updates an agent (agent role).
func HandleAddAgent(dbDir string, args map[string]interface{}) *types.ToolResult {
	agentName, _ := args["name"].(string)
	prompt, _ := args["prompt"].(string)
	llmRef, _ := args["llm-ref"].(string)
	if agentName == "" {
		return &types.ToolResult{Success: false, Error: "name is required", Tool: "add_agent", RawResult: map[string]interface{}{"name": agentName}}
	}
	if prompt == "" {
		return &types.ToolResult{Success: false, Error: "prompt is required", Tool: "add_agent", RawResult: map[string]interface{}{"name": agentName}}
	}

	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "add_agent", RawResult: map[string]interface{}{"name": agentName}}
	}
	defer db.Close(sqlDB)

	agents, err := db.GetProjectAgents(sqlDB)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to load agents: %s", err.Error()), Tool: "add_agent", RawResult: map[string]interface{}{"name": agentName}}
	}

	newAgent := models.AgentConfig{
		Title:  agentName,
		Prompt: prompt,
		LLMRef: llmRef,
		Source: "llm",
	}

	// Replace if exists, otherwise append
	replaced := false
	for i, a := range agents {
		if a.Title == agentName {
			agents[i] = newAgent
			replaced = true
			break
		}
	}
	if !replaced {
		agents = append(agents, newAgent)
	}

	if err := db.SaveProjectAgents(sqlDB, agents); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to save agents: %s", err.Error()), Tool: "add_agent", RawResult: map[string]interface{}{"name": agentName}}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("已添加 agent: %s", agentName),
		Tool:    "add_agent",
		RawResult: map[string]interface{}{
			"name": agentName,
		},
	}
}

// HandleListAgent lists all saved agents.
func HandleListAgent(dbDir string, args map[string]interface{}) *types.ToolResult {
	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "list_agent"}
	}
	defer db.Close(sqlDB)

	agents, err := db.GetProjectAgents(sqlDB)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to list agents: %s", err.Error()), Tool: "list_agent"}
	}

	if len(agents) == 0 {
		return &types.ToolResult{Success: true, Output: "📋 0 个 agent", Tool: "list_agent", RawResult: map[string]interface{}{"agents": []interface{}{}}}
	}

	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Title < agents[j].Title
	})

	var buf strings.Builder
	var agentItems []map[string]interface{}
	for _, a := range agents {
		preview := a.Prompt
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		source := a.Source
		if source == "" {
			source = "user"
		}
		if a.UseCase != "" {
			buf.WriteString(fmt.Sprintf("[%s] (%s) - %s\n  %s\n", a.Title, source, a.UseCase, preview))
		} else {
			buf.WriteString(fmt.Sprintf("[%s] (%s)\n  %s\n", a.Title, source, preview))
		}
		agentItems = append(agentItems, map[string]interface{}{
			"name":   a.Title,
			"source": source,
		})
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("📋 %d 个 agent", len(agents)),
		Tool:    "list_agent",
		RawResult: map[string]interface{}{
			"agents": agentItems,
		},
	}
}

// HandleDeleteAgent deletes an agent.
func HandleDeleteAgent(dbDir string, args map[string]interface{}) *types.ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return &types.ToolResult{Success: false, Error: "name is required", Tool: "delete_agent", RawResult: map[string]interface{}{"name": name}}
	}

	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "delete_agent", RawResult: map[string]interface{}{"name": name}}
	}
	defer db.Close(sqlDB)

	agents, err := db.GetProjectAgents(sqlDB)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to load agents: %s", err.Error()), Tool: "delete_agent", RawResult: map[string]interface{}{"name": name}}
	}

	// Find and check ownership
	found := false
	var filtered []models.AgentConfig
	for _, a := range agents {
		if a.Title == name {
			found = true
			if a.Source != "llm" {
				return &types.ToolResult{
					Success:   false,
					Error:     fmt.Sprintf("agent '%s' was not created by LLM and cannot be deleted", name),
					Tool:      "delete_agent",
					RawResult: map[string]interface{}{"name": name},
				}
			}
			continue
		}
		filtered = append(filtered, a)
	}

	if !found {
		return &types.ToolResult{
			Success:   false,
			Error:     fmt.Sprintf("agent not found: %s", name),
			Tool:      "delete_agent",
			RawResult: map[string]interface{}{"name": name},
		}
	}

	if err := db.SaveProjectAgents(sqlDB, filtered); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to save agents: %s", err.Error()), Tool: "delete_agent", RawResult: map[string]interface{}{"name": name}}
	}

	return &types.ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("🗑️ 已删除 agent: %s", name),
		Tool:      "delete_agent",
		RawResult: map[string]interface{}{"name": name},
	}
}

// ─── Simplify Functions ───

func init() {
	types.RegisterSimplify("add_agent", simplifyAddAgent)
	types.RegisterSimplify("list_agent", types.SimpleAction("list_agent"))
	types.RegisterSimplify("delete_agent", simplifyDeleteAgent)
}

type getNameArg struct {
	Name string `json:"name"`
}

func simplifyAddAgent(argsJSON json.RawMessage, result string) string {
	var a getNameArg
	if err := json.Unmarshal(argsJSON, &a); err != nil || a.Name == "" {
		return "add_agent"
	}
	return fmt.Sprintf("add_agent(%s)", a.Name)
}

func simplifyDeleteAgent(argsJSON json.RawMessage, result string) string {
	var a getNameArg
	if err := json.Unmarshal(argsJSON, &a); err != nil || a.Name == "" {
		return "delete_agent"
	}
	return fmt.Sprintf("delete_agent(%s)", a.Name)
}
