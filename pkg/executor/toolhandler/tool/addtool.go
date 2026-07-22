package tool

import (
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
			Success:   false,
			Error:     "name is required",
			Tool:      "add_tool",
			RawResult: map[string]interface{}{"name": name},
		}
	}
	if description == "" {
		return &types.ToolResult{
			Success:   false,
			Error:     "description is required",
			Tool:      "add_tool",
			RawResult: map[string]interface{}{"name": name},
		}
	}

	// Validate tool name: snake_case, alphanumeric only
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return &types.ToolResult{
				Success:   false,
				Error:     fmt.Sprintf("invalid tool name '%s': must be lowercase snake_case (a-z, 0-9, _)", name),
				Tool:      "add_tool",
				RawResult: map[string]interface{}{"name": name},
			}
		}
	}

	// Determine tool type
	switch toolType {
	case "mcp":
		params, ok := args["parameters"].(map[string]interface{})
		if !ok {
			return &types.ToolResult{
				Success:   false,
				Tool:      "add_tool",
				Error:     "mcp tool requires parameters with 'server_url' and 'mcp_tool_name'",
				RawResult: map[string]interface{}{"name": name},
			}
		}
		if _, hasURL := params["server_url"]; !hasURL {
			return &types.ToolResult{
				Success:   false,
				Tool:      "add_tool",
				Error:     "mcp tool requires 'server_url' in parameters (e.g. http://localhost:8081/mcp)",
				RawResult: map[string]interface{}{"name": name},
			}
		}
		if _, hasName := params["mcp_tool_name"]; !hasName {
			return &types.ToolResult{
				Success:   false,
				Tool:      "add_tool",
				Error:     "mcp tool requires 'mcp_tool_name' in parameters (the tool name exposed by the MCP server)",
				RawResult: map[string]interface{}{"name": name},
			}
		}

	default:
		// command/meta tool: use the command field
	}

	// Open DB
	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "add_tool", RawResult: map[string]interface{}{"name": name}}
	}
	defer db.Close(sqlDB)

	// Read existing tools
	tools, err := db.GetProjectTools(sqlDB)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to load tools: %s", err.Error()), Tool: "add_tool", RawResult: map[string]interface{}{"name": name}}
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
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to save tools: %s", err.Error()), Tool: "add_tool", RawResult: map[string]interface{}{"name": name}}
	}

	zap.S().Infof("Custom tool registered via add_tool: name=%s", name)

	category := tool.Type
	if category == "" {
		category = "command"
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("🔧 已添加工具: %s", name),
		Tool:    "add_tool",
		RawResult: map[string]interface{}{
			"name":       name,
			"type":       tool.Type,
			"category":   category,
			"replaced":   replaced,
			"parameters": args["parameters"],
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
		return &types.ToolResult{Success: true, Output: "📋 0 个工具", Tool: "list_tool", RawResult: map[string]interface{}{"tools": []interface{}{}}}
	}

	var buf strings.Builder
	var toolItems []map[string]interface{}
	for _, t := range tools {
		category := "command"
		if t.Type == "meta" {
			category = "meta"
		} else if t.Type == "mcp" {
			category = "mcp"
		}
		buf.WriteString(fmt.Sprintf("- %s (%s): %s\n", t.Name, category, t.Description))
		toolItems = append(toolItems, map[string]interface{}{
			"name":        t.Name,
			"description": t.Description,
			"category":    category,
		})
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("📋 %d 个工具", len(tools)),
		Tool:    "list_tool",
		RawResult: map[string]interface{}{
			"tools": toolItems,
		},
	}
}

// HandleGetTool shows details of a specific custom tool.
func HandleGetTool(dbDir string, args map[string]interface{}) *types.ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return &types.ToolResult{Success: false, Error: "name is required", Tool: "get_tool", RawResult: map[string]interface{}{"name": name}}
	}

	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "get_tool", RawResult: map[string]interface{}{"name": name}}
	}
	defer db.Close(sqlDB)

	tools, err := db.GetProjectTools(sqlDB)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to load tools: %s", err.Error()), Tool: "get_tool", RawResult: map[string]interface{}{"name": name}}
	}

	var tool *models.ToolConfig
	for _, t := range tools {
		if t.Name == name {
			tool = &t
			break
		}
	}
	if tool == nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("custom tool not found: %s", name), Tool: "get_tool", RawResult: map[string]interface{}{"name": name}}
	}

	category := tool.Type
	if category == "" {
		category = "command"
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("🔧 工具: %s", name),
		Tool:    "get_tool",
		RawResult: map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.Parameters,
			"category":    category,
			"type":        tool.Type,
			"command":     tool.Command,
			"source":      tool.Source,
		},
	}
}

// HandleDeleteTool deletes a custom tool by name, only if created by LLM (source == "llm").
func HandleDeleteTool(dbDir string, args map[string]interface{}) *types.ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return &types.ToolResult{Success: false, Error: "name is required", Tool: "delete_tool", RawResult: map[string]interface{}{"name": name}}
	}

	sqlDB, err := db.Open(dbDir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("db open failed: %s", err.Error()), Tool: "delete_tool", RawResult: map[string]interface{}{"name": name}}
	}
	defer db.Close(sqlDB)

	tools, err := db.GetProjectTools(sqlDB)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to load tools: %s", err.Error()), Tool: "delete_tool", RawResult: map[string]interface{}{"name": name}}
	}

	// Find and check ownership
	found := false
	var filtered []models.ToolConfig
	for _, t := range tools {
		if t.Name == name {
			found = true
			if t.Source != "llm" {
				return &types.ToolResult{Success: false, Error: fmt.Sprintf("tool '%s' was not created by LLM and cannot be deleted", name), Tool: "delete_tool", RawResult: map[string]interface{}{"name": name}}
			}
			continue
		}
		filtered = append(filtered, t)
	}

	if !found {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("custom tool not found: %s", name), Tool: "delete_tool", RawResult: map[string]interface{}{"name": name}}
	}

	if err := db.SaveProjectTools(sqlDB, filtered); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to save tools: %s", err.Error()), Tool: "delete_tool", RawResult: map[string]interface{}{"name": name}}
	}

	zap.S().Infof("Custom tool deleted: %s", name)

	return &types.ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("🗑️ 已删除工具: %s", name),
		Tool:      "delete_tool",
		RawResult: map[string]interface{}{"name": name},
	}
}

func init() {
	types.RegisterSimplify("add_tool", types.SimpleAction("add_tool"))
	types.RegisterSimplify("list_tool", types.SimpleAction("list_tool"))
	types.RegisterSimplify("get_tool", types.SimpleAction("get_tool"))
	types.RegisterSimplify("delete_tool", types.SimpleAction("delete_tool"))
}
