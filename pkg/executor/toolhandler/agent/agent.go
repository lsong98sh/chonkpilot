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
		return &types.ToolResult{Success: false, Error: "name is required", Tool: "add_agent"}
	}
	if prompt == "" {
		return &types.ToolResult{Success: false, Error: "prompt is required", Tool: "add_agent"}
	}

	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "add_agent"}
	}
	defer db.Close(sqlDB)

	agents, err := db.GetProjectAgents(sqlDB)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to load agents: %s", err.Error()), Tool: "add_agent"}
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
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to save agents: %s", err.Error()), Tool: "add_agent"}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("agent saved: %s. Use call_llm(agent=\"%s\", ...) to invoke it", agentName, agentName),
		Tool:    "add_agent",
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
		return &types.ToolResult{Success: true, Output: "no agents found", Tool: "list_agent"}
	}

	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Title < agents[j].Title
	})

	var buf strings.Builder
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
	}

	return &types.ToolResult{
		Success: true,
		Output:  strings.TrimSpace(buf.String()),
		Tool:    "list_agent",
	}
}

// HandleDeleteAgent deletes an agent.
func HandleDeleteAgent(dbDir string, args map[string]interface{}) *types.ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return &types.ToolResult{Success: false, Error: "name is required", Tool: "delete_agent"}
	}

	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "delete_agent"}
	}
	defer db.Close(sqlDB)

	agents, err := db.GetProjectAgents(sqlDB)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to load agents: %s", err.Error()), Tool: "delete_agent"}
	}

	// Find and check ownership
	found := false
	var filtered []models.AgentConfig
	for _, a := range agents {
		if a.Title == name {
			found = true
			if a.Source != "llm" {
				return &types.ToolResult{
					Success: false,
					Error:   fmt.Sprintf("agent '%s' was not created by LLM and cannot be deleted", name),
					Tool:    "delete_agent",
				}
			}
			continue
		}
		filtered = append(filtered, a)
	}

	if !found {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("agent not found: %s", name),
			Tool:    "delete_agent",
		}
	}

	if err := db.SaveProjectAgents(sqlDB, filtered); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to save agents: %s", err.Error()), Tool: "delete_agent"}
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("agent deleted: %s", name),
		Tool:    "delete_agent",
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
