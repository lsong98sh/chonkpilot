package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"go.uber.org/zap"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// HandleAddTool registers a custom tool by writing to project_tools table.
func HandleAddTool(dbDir string, args map[string]interface{}) *types.ToolResult {
	name, _ := args["name"].(string)
	description, _ := args["description"].(string)
	command, _ := args["command"].(string)
	toolType, _ := args["type"].(string)

	if name == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "name is required",
			Tool:    "add_tool",
		}
	}
	if description == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "description is required",
			Tool:    "add_tool",
		}
	}

	// Validate tool name: snake_case, alphanumeric only
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("invalid tool name '%s': must be lowercase snake_case (a-z, 0-9, _)", name),
				Tool:    "add_tool",
			}
		}
	}

	// Determine tool type
	switch toolType {
	case "mcp":
		params, ok := args["parameters"].(map[string]interface{})
		if !ok {
			return &types.ToolResult{
				Success: false,
				Tool:    "add_tool",
				Error:   "mcp tool requires parameters with 'server_url' and 'mcp_tool_name'",
			}
		}
		if _, hasURL := params["server_url"]; !hasURL {
			return &types.ToolResult{
				Success: false,
				Tool:    "add_tool",
				Error:   "mcp tool requires 'server_url' in parameters (e.g. http://localhost:8081/mcp)",
			}
		}
		if _, hasName := params["mcp_tool_name"]; !hasName {
			return &types.ToolResult{
				Success: false,
				Tool:    "add_tool",
				Error:   "mcp tool requires 'mcp_tool_name' in parameters (the tool name exposed by the MCP server)",
			}
		}

	default:
		// command/meta tool: use the command field
	}

	// Open DB
	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "add_tool"}
	}
	defer db.Close(sqlDB)

	// Read existing tools
	tools, err := db.GetProjectTools(sqlDB)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to load tools: %s", err.Error()), Tool: "add_tool"}
	}

	// Build tool config
	tool := models.ToolConfig{
		Name:        name,
		Description: description,
		Source:      "llm",
	}

	switch toolType {
	case "mcp":
		tool.Type = "mcp"
		tool.Parameters = args["parameters"]
	default:
		tool.Type = "command"
		tool.Command = command
		if params, ok := args["parameters"]; ok && params != nil {
			tool.Parameters = params
			if command == "" {
				tool.Type = "meta"
			}
		}
	}

	// Check for duplicate name - replace if exists, otherwise append
	replaced := false
	for i, t := range tools {
		if t.Name == name {
			tools[i] = tool
			replaced = true
			break
		}
	}
	if !replaced {
		tools = append(tools, tool)
	}

	// Save back to DB
	if err := db.SaveProjectTools(sqlDB, tools); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to save tools: %s", err.Error()), Tool: "add_tool"}
	}

	msg := fmt.Sprintf("registered custom tool '%s'", name)
	if replaced {
		msg = fmt.Sprintf("updated custom tool '%s'", name)
	}

	zap.S().Infof("Custom tool registered via add_tool: name=%s", name)

	return &types.ToolResult{
		Success: true,
		Output:  msg,
		Tool:    "add_tool",
		RawResult: map[string]interface{}{
			"name":        name,
			"description": description,
		},
	}
}

// HandleListTool lists all custom tools registered via add_tool.
func HandleListTool(dbDir string, args map[string]interface{}) *types.ToolResult {
	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "list_tool"}
	}
	defer db.Close(sqlDB)

	tools, err := db.GetProjectTools(sqlDB)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to load tools: %s", err.Error()), Tool: "list_tool"}
	}

	if len(tools) == 0 {
		return &types.ToolResult{Success: true, Output: "no custom tools found", Tool: "list_tool"}
	}

	var buf strings.Builder
	for _, t := range tools {
		meta := "command"
		if t.Type == "meta" {
			meta = "meta"
		} else if t.Type == "mcp" {
			meta = "mcp"
		}
		buf.WriteString(fmt.Sprintf("- %s (%s): %s\n", t.Name, meta, t.Description))
	}

	return &types.ToolResult{
		Success: true,
		Output:  strings.TrimSpace(buf.String()),
		Tool:    "list_tool",
	}
}

// HandleGetTool shows details of a specific custom tool.
func HandleGetTool(dbDir string, args map[string]interface{}) *types.ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return &types.ToolResult{Success: false, Error: "name is required", Tool: "get_tool"}
	}

	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "get_tool"}
	}
	defer db.Close(sqlDB)

	tools, err := db.GetProjectTools(sqlDB)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to load tools: %s", err.Error()), Tool: "get_tool"}
	}

	var tool *models.ToolConfig
	for _, t := range tools {
		if t.Name == name {
			tool = &t
			break
		}
	}
	if tool == nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("custom tool not found: %s", name), Tool: "get_tool"}
	}

	paramsStr := ""
	if tool.Parameters != nil {
		p, _ := json.MarshalIndent(tool.Parameters, "", "  ")
		paramsStr = string(p)
	}

	output := fmt.Sprintf("name: %s\ntype: %s\ndescription: %s\nsource: %s\n", tool.Name, tool.Type, tool.Description, tool.Source)
	if tool.Command != "" {
		output += fmt.Sprintf("command: %s\n", tool.Command)
	}
	if paramsStr != "" {
		output += fmt.Sprintf("parameters:\n%s\n", paramsStr)
	}

	return &types.ToolResult{
		Success: true,
		Output:  strings.TrimSpace(output),
		Tool:    "get_tool",
	}
}

// HandleDeleteTool deletes a custom tool by name, only if created by LLM (source == "llm").
func HandleDeleteTool(dbDir string, args map[string]interface{}) *types.ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return &types.ToolResult{Success: false, Error: "name is required", Tool: "delete_tool"}
	}

	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "delete_tool"}
	}
	defer db.Close(sqlDB)

	tools, err := db.GetProjectTools(sqlDB)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to load tools: %s", err.Error()), Tool: "delete_tool"}
	}

	// Find and check ownership
	found := false
	var filtered []models.ToolConfig
	for _, t := range tools {
		if t.Name == name {
			found = true
			if t.Source != "llm" {
				return &types.ToolResult{Success: false, Error: fmt.Sprintf("tool '%s' was not created by LLM and cannot be deleted", name), Tool: "delete_tool"}
			}
			continue
		}
		filtered = append(filtered, t)
	}

	if !found {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("custom tool not found: %s", name), Tool: "delete_tool"}
	}

	if err := db.SaveProjectTools(sqlDB, filtered); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to save tools: %s", err.Error()), Tool: "delete_tool"}
	}

	zap.S().Infof("Custom tool deleted: %s", name)

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("custom tool deleted: %s", name),
		Tool:    "delete_tool",
	}
}

func init() {
	types.RegisterSimplify("add_tool", types.SimpleAction("add_tool"))
	types.RegisterSimplify("list_tool", types.SimpleAction("list_tool"))
	types.RegisterSimplify("get_tool", types.SimpleAction("get_tool"))
	types.RegisterSimplify("delete_tool", types.SimpleAction("delete_tool"))
}
