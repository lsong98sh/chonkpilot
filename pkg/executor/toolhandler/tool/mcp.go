package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// mcpRequestTimeout is the HTTP client timeout for manual MCP tool calls.
// Default 60s. Override via SetMCPTimeout.
var mcpRequestTimeout = 60 * time.Second

// SetMCPTimeout sets the MCP request timeout for manual MCP tool calls.
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

// HandleMCPTool dispatches a tool call to an MCP server via JSON-RPC.
func HandleMCPTool(tool models.ToolConfig, args map[string]interface{}) *types.ToolResult {
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
