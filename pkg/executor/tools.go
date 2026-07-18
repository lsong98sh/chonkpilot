package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	icfg "github.com/chonkpilot/chonkpilot/internal/config"
	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/pkg/executor/discover"
	"github.com/chonkpilot/chonkpilot/pkg/executor/llm"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// loadAllToolDefs loads all tool definitions: builtin tools always included,
// plus user custom tools from --tools-file (highest priority), --tools, or .ide DB.
// chromeOK controls whether web browser tools are included.
func loadAllToolDefs(ea *ExecutorArgs, logger *zap.Logger, chromeOK bool) []llm.ToolDefinition {
	discoverer := discover.NewDiscoverer()
	builtinTools := discoverer.ListBuiltinTools()
	toolDefs := make([]llm.ToolDefinition, 0, len(builtinTools))
	for _, t := range builtinTools {
		// Skip web browser tools if Chrome not found (category set by directory path)
		if !chromeOK && t.Category == "web" {
			continue
		}
		// Sub-sessions should not have ask_user — delegate questions back to main LLM
		if isSubSession(ea) && t.Name == "ask_user" {
			continue
		}
		// DB-dependent tools are always available (temp DB or real .ide)
		toolDefs = append(toolDefs, llm.ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}

	// Load user tools (flat format: [{name, description, parameters}])
	var userTools []llm.ToolDefinition

	// 1. --tools-file (highest priority)
	if ea.ToolsFile != "" {
		data, err := os.ReadFile(ea.ToolsFile)
		if err != nil {
			logger.Warn("Failed to read --tools-file", zap.Error(err))
		} else if len(data) > 0 {
			var tools []llm.ToolDefinition
			if err := json.Unmarshal(data, &tools); err != nil {
				logger.Warn("Invalid JSON in --tools-file", zap.Error(err))
			} else {
				userTools = tools
				logger.Info("Loaded tools from --tools-file", zap.Int("count", len(tools)))
			}
		}
	} else if ea.Tools != "" {
		// 2. --tools (only if --tools-file not specified)
		var tools []llm.ToolDefinition
		if err := json.Unmarshal([]byte(ea.Tools), &tools); err != nil {
			logger.Warn("Invalid JSON in --tools", zap.Error(err))
		} else {
			userTools = tools
			logger.Info("Loaded tools from --tools", zap.Int("count", len(tools)))
		}
	} else if hasIDEConfig(ea) {
		// 3. .ide DB (only if neither --tools-file nor --tools specified)
		if t, err := loadToolsFromDB(ea.DBWorkDir()); err == nil && len(t) > 0 {
			userTools = t
			logger.Info("Loaded tools from .ide DB", zap.Int("count", len(t)))
		}
	}

	// 4. Auto-discover tools from globally enabled MCP servers (from ~/.chonkpilot/config.json)
	if mcpDefs := loadMCPTools(logger); len(mcpDefs) > 0 {
		toolDefs = append(toolDefs, mcpDefs...)
		logger.Info("Loaded tools from MCP servers", zap.Int("count", len(mcpDefs)))
	}

	// Append user tools after builtin tools
	toolDefs = append(toolDefs, userTools...)

	return toolDefs
}

// isDBTool returns true if the tool requires the .ide/ directory (ide.db or custom_tools.json).
// These tools are filtered out when running without a project directory.
func isDBTool(name string) bool {
	return name == "note_write" || name == "note_read" || name == "note_list" || name == "add_tool"
}

// loadToolsFromDB loads project-level custom tools from the project_tools table.
func loadToolsFromDB(workDir string) ([]llm.ToolDefinition, error) {
	sqlDB, err := db.Open(workDir)
	if err != nil {
		return nil, err
	}
	defer db.Close(sqlDB)

	configs, err := db.GetProjectTools(sqlDB)
	if err != nil {
		return nil, fmt.Errorf("no tools in DB: %w", err)
	}
	if len(configs) == 0 {
		return nil, fmt.Errorf("no tools in DB")
	}

	var tools []llm.ToolDefinition
	for _, tc := range configs {
		def := llm.ToolDefinition{
			Name:        tc.Name,
			Description: tc.Description,
		}
		// Convert ToolConfig.Parameters (interface{}) to a JSON schema map
		if tc.Parameters != nil {
			if paramsMap, ok := tc.Parameters.(map[string]interface{}); ok {
				def.Parameters = paramsMap
			}
		}
		// Wrap command/type info for runtime dispatch
		if def.Parameters == nil {
			def.Parameters = map[string]interface{}{}
		}
		if _, ok := def.Parameters.(map[string]interface{}); ok {
			pm := def.Parameters.(map[string]interface{})
			pm["_tool_type"] = tc.Type
			pm["_tool_command"] = tc.Command
			pm["_tool_mcp_id"] = tc.McpID
			if tc.Type == "mcp" {
				if params, ok2 := tc.Parameters.(map[string]interface{}); ok2 {
					if su, ok3 := params["server_url"]; ok3 {
						pm["server_url"] = su
					}
					if mn, ok3 := params["mcp_tool_name"]; ok3 {
						pm["mcp_tool_name"] = mn
					}
				}
			}
		}
		tools = append(tools, def)
	}
	return tools, nil
}

// loadMCPTools auto-discovers tools from globally enabled MCP servers (~/.chonkpilot/config.json).
// Tools are discovered at config-save time via tools/list (see app_config.go DiscoverMCPServerTools)
// and persisted in the config file. This function reads them from config without any HTTP calls.
func loadMCPTools(logger *zap.Logger) []llm.ToolDefinition {
	ucfg, err := icfg.EnsureUserConfig()
	if err != nil {
		logger.Debug("loadMCPTools: no user config", zap.Error(err))
		return nil
	}

	var defs []llm.ToolDefinition

	for _, srv := range ucfg.MCPServers {
		if !srv.Enabled || srv.Name == "" || srv.URL == "" {
			continue
		}
		name := srv.Name

		// Use pre-discovered tools from config (set at config-save time)
		if len(srv.DiscoveredTools) > 0 {
			for _, dt := range srv.DiscoveredTools {
				if dt.Name == "" {
					continue
				}
				toolName := fmt.Sprintf("mcp_%s_%s", name, dt.Name)
				params := map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
					"required":   []string{},
				}
				// Copy the MCP tool's inputSchema into our tool definition
				if dt.InputSchema != nil {
					if schema, ok := dt.InputSchema.(map[string]interface{}); ok {
						if props, ok := schema["properties"]; ok {
							params["properties"] = props
						}
						if req, ok := schema["required"]; ok {
							params["required"] = req
						}
					}
				}
				desc := dt.Description
				if desc == "" {
					desc = fmt.Sprintf("Call the '%s' tool on MCP server '%s'", dt.Name, name)
				}
				defs = append(defs, llm.ToolDefinition{
					Name:        toolName,
					Description: desc,
					Parameters:  params,
				})
			}
			logger.Debug("MCP loaded pre-discovered tools",
				zap.String("server", name),
				zap.Int("count", len(srv.DiscoveredTools)))
		} else {
			// No pre-discovered tools — create a generic fallback tool
			toolName := "mcp_" + name
			defs = append(defs, llm.ToolDefinition{
				Name:        toolName,
				Description: fmt.Sprintf("Call a tool on the MCP server '%s' (%s). Pass 'tool_name' (required) to specify which MCP tool to invoke, and 'arguments' (optional, object) as the tool's input parameters.", srv.Name, srv.Description),
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"tool_name": map[string]interface{}{
							"type":        "string",
							"description": "The name of the MCP tool to call on this server.",
						},
						"arguments": map[string]interface{}{
							"type":                 "object",
							"description":          "Optional arguments to pass to the MCP tool.",
							"additionalProperties": true,
						},
					},
					"required": []string{"tool_name"},
				},
			})
			logger.Debug("MCP no pre-discovered tools, using generic fallback",
				zap.String("server", name))
		}
	}

	if len(defs) > 0 {
		logger.Info("MCP loaded tools total", zap.Int("count", len(defs)))
	}
	return defs
}

// hasIDEConfig checks if .ide/ide.db exists, either in the real work directory
// or in the temporary directory (when running standalone with a temp DB).
func hasIDEConfig(ea *ExecutorArgs) bool {
	if ea.TempIDEDir != "" {
		return true
	}
	ideDBPath := filepath.Join(ea.WorkDir, ".ide", "ide.db")
	_, err := os.Stat(ideDBPath)
	return err == nil
}

// createTempDB creates a temporary .ide directory for standalone sessions.
// Sets ea.TempIDEDir so all subsequent DB operations use this temp DB.
// If no real .ide exists, this ensures a unified DB code path.
func createTempDB(ea *ExecutorArgs) error {
	id := uuid.New().String()
	tempRoot := filepath.Join(os.TempDir(), "chonkpilot", id)
	if err := os.MkdirAll(tempRoot, 0755); err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	sqlDB, err := db.Open(tempRoot)
	if err != nil {
		os.RemoveAll(tempRoot)
		return fmt.Errorf("failed to create temp DB: %w", err)
	}
	db.Close(sqlDB)
	ea.TempIDEDir = tempRoot

	// Set TEMP env var for this session so temp files go to .ide/tmp
	tmpDir := filepath.Join(tempRoot, ".ide", "tmp")
	os.MkdirAll(tmpDir, 0755)
	os.Setenv("TEMP", tmpDir)
	os.Setenv("TMP", tmpDir)

	// Generate session+turn IDs so ensureSessionAndTurn creates records
	if ea.SessionID == "" {
		ea.SessionID = uuid.New().String()
	}
	if ea.TurnID == "" {
		ea.TurnID = uuid.New().String()
	}
	return nil
}

// setTempDir sets the TEMP/TMP environment variable to point to .ide/tmp.
// When using a real .ide directory, it creates the tmp dir under workDir/.ide/tmp.
func setTempDir(workDir, tempIDEDir string) {
	var base string
	if tempIDEDir != "" {
		base = tempIDEDir
	} else {
		base = workDir
	}
	tmpDir := filepath.Join(base, ".ide", "tmp")
	if err := os.MkdirAll(tmpDir, 0755); err == nil {
		os.Setenv("TEMP", tmpDir)
		os.Setenv("TMP", tmpDir)
	}
}

// cleanupTempDB removes the temporary .ide directory if one was created.
func cleanupTempDB(ea *ExecutorArgs) {
	if ea.TempIDEDir != "" {
		os.RemoveAll(ea.TempIDEDir)
		ea.TempIDEDir = ""
	}
}

// eventWithCtx injects session_id and turn_id into an event payload if not already present.
// Sub-executors (batch_llm, call_llm) set their own session_id/turn_id before calling writeEvent,
// so eventWithCtx must not overwrite them.
func eventWithCtx(ea *ExecutorArgs, payload map[string]interface{}) map[string]interface{} {
	if payload == nil {
		payload = make(map[string]interface{})
	}
	if _, ok := payload["session_id"]; !ok && ea.SessionID != "" {
		payload["session_id"] = ea.SessionID
	}
	if _, ok := payload["turn_id"]; !ok && ea.TurnID != "" {
		payload["turn_id"] = ea.TurnID
	}
	return payload
}

// isSubSession checks if the current session has a parent (i.e., it's a sub-session).
func isSubSession(ea *ExecutorArgs) bool {
	if ea.SessionID == "" {
		return false
	}
	sqlDB, err := db.Open(ea.DBWorkDir())
	if err != nil {
		return false
	}
	defer db.Close(sqlDB)

	sess, err := db.GetSession(sqlDB, ea.SessionID)
	if err != nil {
		return false
	}
	return sess.ParentID != ""
}
