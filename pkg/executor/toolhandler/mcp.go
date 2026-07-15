package toolhandler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	icfg "github.com/chonkpilot/chonkpilot/internal/config"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/file"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// mcpRequestTimeout is the HTTP client timeout for all MCP server calls.
// Default 60s. Override via SetMCPTimeout.
var mcpRequestTimeout = 60 * time.Second

// SetMCPTimeout sets the global MCP request timeout.
func SetMCPTimeout(seconds int) {
	if seconds > 0 {
		mcpRequestTimeout = time.Duration(seconds) * time.Second
	}
}

// mcpRequest represents a JSON-RPC 2.0 request for MCP tool calls.
type mcpRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// mcpResponse represents a JSON-RPC 2.0 response from an MCP server.
type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

// mcpError represents a JSON-RPC error object.
type mcpError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// mcpToolCallPayload is the params for an MCP tools/call request.
type mcpToolCallPayload struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// handleMCPTool dispatches a tool call to an MCP server via JSON-RPC.
func (h *Handler) handleMCPTool(tool models.ToolConfig, args map[string]interface{}) *types.ToolResult {
	// Extract MCP connection info from tool parameters
	params, ok := tool.Parameters.(map[string]interface{})
	if !ok {
		return &types.ToolResult{
			Success: false,
			Tool:    tool.Name,
			Error:   "mcp tool has invalid parameters (expected object with server_url and mcp_tool_name)",
		}
	}

	serverURL, _ := params["server_url"].(string)
	mcpToolName, _ := params["mcp_tool_name"].(string)
	if serverURL == "" || mcpToolName == "" {
		return &types.ToolResult{
			Success: false,
			Tool:    tool.Name,
			Error:   "mcp tool missing server_url or mcp_tool_name in parameters",
		}
	}

	// Build JSON-RPC request
	reqBody := mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mcpToolCallPayload{
			Name:      mcpToolName,
			Arguments: args,
		},
	}

	reqData, err := json.Marshal(reqBody)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Tool:    tool.Name,
			Error:   fmt.Sprintf("failed to marshal MCP request: %s", err.Error()),
		}
	}

	// Send HTTP POST to MCP server
	client := &http.Client{Timeout: mcpRequestTimeout}
	resp, err := client.Post(serverURL, "application/json", bytes.NewReader(reqData))
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Tool:    tool.Name,
			Error:   fmt.Sprintf("MCP server unreachable (%s): %s", serverURL, err.Error()),
		}
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Tool:    tool.Name,
			Error:   fmt.Sprintf("failed to read MCP response: %s", err.Error()),
		}
	}

	// Parse JSON-RPC response
	var mcpResp mcpResponse
	if err := json.Unmarshal(respData, &mcpResp); err != nil {
		return &types.ToolResult{
			Success: false,
			Tool:    tool.Name,
			Error:   fmt.Sprintf("invalid MCP response (not JSON-RPC): %s", err.Error()),
		}
	}

	if mcpResp.Error != nil {
		return &types.ToolResult{
			Success: false,
			Tool:    tool.Name,
			Error:   fmt.Sprintf("MCP error [%d]: %s", mcpResp.Error.Code, mcpResp.Error.Message),
		}
	}

	// Try to extract meaningful output from the result
	output := string(mcpResp.Result)
	var resultObj map[string]interface{}
	if json.Unmarshal(mcpResp.Result, &resultObj) == nil {
		if content, ok := resultObj["content"]; ok {
			switch c := content.(type) {
			case string:
				output = c
			default:
				if b, err := json.MarshalIndent(c, "", "  "); err == nil {
					output = string(b)
				}
			}
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Tool:    tool.Name,
		RawResult: map[string]interface{}{
			"server_url":    serverURL,
			"mcp_tool_name": mcpToolName,
		},
	}
}

// handleAutoMCPTool dispatches a tool call to an MCP server discovered from global config.
// Two formats are supported:
//
//	Format 1 (specific, preferred): "mcp_<serverName>_<toolName>" — calls the specific tool with proper args.
//	Format 2 (generic, fallback):   "mcp_<serverName>" — calls any tool, requires 'tool_name' arg.
func (h *Handler) handleAutoMCPTool(toolName string, args map[string]interface{}) *types.ToolResult {
	// Strip "mcp_" prefix
	remainder := strings.TrimPrefix(toolName, "mcp_")
	if remainder == "" {
		return &types.ToolResult{
			Success: false,
			Tool:    toolName,
			Error:   fmt.Sprintf("invalid MCP tool name: %s", toolName),
		}
	}

	// Parse server name and MCP tool name
	serverName, mcpToolName := parseMCPToolName(remainder)
	if serverName == "" {
		return &types.ToolResult{
			Success: false,
			Tool:    toolName,
			Error:   fmt.Sprintf("unknown MCP server in tool name: %s", toolName),
		}
	}

	// Look up server URL from user config
	serverURL := lookupMCPServerURL(serverName)
	if serverURL == "" {
		return &types.ToolResult{
			Success: false,
			Tool:    toolName,
			Error:   fmt.Sprintf("MCP server '%s' not found in global config", serverName),
		}
	}

	if mcpToolName == "" {
		// Format 2 (generic): extract tool_name and arguments from LLM args
		mcpToolName, _ = args["tool_name"].(string)
		if mcpToolName == "" {
			return &types.ToolResult{
				Success: false,
				Tool:    toolName,
				Error:   "MCP call requires 'tool_name' parameter for generic server tool",
			}
		}

		// Build the actual MCP tool arguments
		mcpArgs := make(map[string]interface{})
		if argsMap, ok := args["arguments"].(map[string]interface{}); ok {
			mcpArgs = argsMap
		} else {
			for k, v := range args {
				if k != "tool_name" && k != "arguments" {
					mcpArgs[k] = v
				}
			}
		}
		args = mcpArgs
	}

	// Reuse the existing handleMCPTool logic via models.ToolConfig
	tool := models.ToolConfig{
		Name: toolName,
		Type: "mcp",
		Parameters: map[string]interface{}{
			"server_url":    serverURL,
			"mcp_tool_name": mcpToolName,
		},
	}
	return h.handleMCPTool(tool, args)
}

// parseMCPToolName splits a remainder (after "mcp_" prefix) into server name and tool name.
// It checks known MCP servers from user config to correctly identify the boundary.
// If remainder is just a server name, toolName will be empty (generic fallback).
func parseMCPToolName(remainder string) (serverName, toolName string) {
	ucfg, err := icfg.EnsureUserConfig()
	if err != nil {
		// Fallback: split on first underscore
		parts := strings.SplitN(remainder, "_", 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
		return remainder, ""
	}

	// Try to match the longest known server name prefix
	for _, srv := range ucfg.MCPServers {
		if srv.Name == "" || !srv.Enabled {
			continue
		}
		if remainder == srv.Name {
			return srv.Name, "" // server name only, generic call
		}
		if strings.HasPrefix(remainder, srv.Name+"_") {
			return srv.Name, strings.TrimPrefix(remainder, srv.Name+"_")
		}
	}

	// No known server found — return remainder as server name (will fail lookup)
	return remainder, ""
}

// lookupMCPServerURL finds the URL for a named MCP server in user config.
func lookupMCPServerURL(name string) string {
	ucfg, err := icfg.EnsureUserConfig()
	if err != nil {
		return ""
	}
	for _, srv := range ucfg.MCPServers {
		if srv.Name == name && srv.Enabled {
			return srv.URL
		}
	}
	return ""
}

// SetFetchTimeout sets the HTTP fetch timeout (re-export from file package).
func SetFetchTimeout(seconds int) {
	file.SetFetchTimeout(seconds)
}
